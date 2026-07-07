package cockpit

import (
	"reflect"
	"testing"
)

func TestKindLabelMatchesWebVocabulary(t *testing.T) {
	// These labels must stay in lockstep with fixKindLabel in
	// internal/web/static_src/daily-fixes.ts so CLI, web, and review context
	// name each rule identically.
	want := map[RuleKind]string{
		RuleFrontmatterError:  "Frontmatter",
		RuleSchemaViolation:   "Schema",
		RuleBrokenTarget:      "Broken target",
		RuleDuplicateTitle:    "Duplicate title",
		RuleOrphanNote:        "Orphan",
		RuleDeadEndNote:       "Dead end",
		RuleLongTailValue:     "Long-tail value",
		RuleSensitiveContent:  "Sensitive content",
		RuleMissingReviewDate: "Missing review date",
		RuleStaleStatus:       "Stale status",
		RuleWIPLimit:          "WIP limit",
		RuleHubNeedsSynthesis: "Hub needs synthesis",
		RuleAIDailyReview:     "AI daily review",
	}
	for kind, label := range want {
		if got := KindLabel(kind); got != label {
			t.Errorf("KindLabel(%q) = %q, want %q", kind, got, label)
		}
	}
	if got := KindLabel(RuleKind("nope")); got != "Fix" {
		t.Errorf("KindLabel(unknown) = %q, want %q", got, "Fix")
	}
}

// reviewQueue builds a small deterministic queue for review-context tests.
func reviewQueue() Queue {
	return Queue{
		Tasks: []Task{
			{
				Kind:       RuleSensitiveContent,
				Priority:   15,
				Title:      "Possible sensitive content: Secrets",
				Summary:    "1 possible secret",
				Detail:     "Notes/Secrets.md",
				Count:      1,
				TriageOnly: true,
				Affected: []AffectedNote{{
					Title:    "Secrets",
					RelPath:  "Notes/Secrets.md",
					Folder:   "Notes",
					Location: "line 4: AWS access key ID",
					FileHash: "deadbeef",
				}},
			},
			{
				Kind:       RuleBrokenTarget,
				Priority:   30,
				Title:      "Broken target: missing",
				Summary:    "1 unresolved link to missing",
				Detail:     "missing",
				Count:      1,
				TriageOnly: false,
				Affected: []AffectedNote{{
					Title:    "Alpha",
					RelPath:  "Notes/Alpha.md",
					Location: "line 2",
					FileHash: "abc123",
				}},
			},
			{
				Kind:       RuleBrokenTarget,
				Priority:   30,
				Title:      "Broken target: elsewhere",
				Summary:    "1 unresolved link to elsewhere",
				Detail:     "elsewhere",
				Count:      1,
				TriageOnly: false,
				Affected: []AffectedNote{{
					Title:   "Beta",
					RelPath: "Notes/Beta.md",
				}},
			},
		},
		Counts: Counts{Active: 3, DoneHidden: 2, SnoozedHidden: 1, TotalGenerated: 6},
	}
}

func TestBuildReviewContext(t *testing.T) {
	ctx := BuildReviewContext(reviewQueue())

	if ctx.Counts != (Counts{Active: 3, DoneHidden: 2, SnoozedHidden: 1, TotalGenerated: 6}) {
		t.Errorf("Counts = %+v, want the queue's full counts", ctx.Counts)
	}
	if ctx.TaskCount != 3 {
		t.Errorf("TaskCount = %d, want 3", ctx.TaskCount)
	}

	// Groups collapse by kind in first-appearance (priority) order.
	wantGroups := []ReviewGroup{
		{Kind: RuleSensitiveContent, Label: "Sensitive content", Count: 1},
		{Kind: RuleBrokenTarget, Label: "Broken target", Count: 2},
	}
	if !reflect.DeepEqual(ctx.Groups, wantGroups) {
		t.Errorf("Groups = %+v, want %+v", ctx.Groups, wantGroups)
	}

	// Item order mirrors the queue; labels are populated; redacted location is
	// carried verbatim.
	if len(ctx.Items) != 3 {
		t.Fatalf("Items len = %d, want 3", len(ctx.Items))
	}
	first := ctx.Items[0]
	if first.Kind != RuleSensitiveContent || first.Label != "Sensitive content" {
		t.Errorf("first item kind/label = %q/%q", first.Kind, first.Label)
	}
	if first.Affected[0].Location != "line 4: AWS access key ID" {
		t.Errorf("location = %q, want redacted detector string", first.Affected[0].Location)
	}
}

func TestBuildReviewContextNeverLeaksFileHash(t *testing.T) {
	// The review context is handed to agents; it must never carry the on-disk
	// file hash that anchors a task signature. ReviewNote has no such field, so
	// a struct-shape assertion is the cheapest regression guard.
	if _, ok := reflect.TypeOf(ReviewNote{}).FieldByName("FileHash"); ok {
		t.Fatal("ReviewNote must not expose a FileHash field")
	}
	ctx := BuildReviewContext(reviewQueue())
	for _, item := range ctx.Items {
		for _, note := range item.Affected {
			if note.RelPath == "" {
				t.Error("expected a rel path on every affected note")
			}
		}
	}
}

func TestBuildReviewContextEmptyQueue(t *testing.T) {
	ctx := BuildReviewContext(Queue{})
	if ctx.TaskCount != 0 {
		t.Errorf("TaskCount = %d, want 0", ctx.TaskCount)
	}
	if len(ctx.Groups) != 0 {
		t.Errorf("Groups = %+v, want empty", ctx.Groups)
	}
	if len(ctx.Items) != 0 {
		t.Errorf("Items = %+v, want empty", ctx.Items)
	}
}

func TestBuildReviewContextDeterministic(t *testing.T) {
	a := BuildReviewContext(reviewQueue())
	b := BuildReviewContext(reviewQueue())
	if !reflect.DeepEqual(a, b) {
		t.Error("BuildReviewContext is not deterministic for identical input")
	}
}
