package web

import (
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

func TestBuildReviewQueueGroupsOrdersAndRouteMetadata(t *testing.T) {
	v, _, _ := newFilterTestSnapshot(t, map[string]string{
		"Work/Source.md":    "---\nstatus: active\ntype: note\n---\n[[Target]] [[Missing]]\n",
		"Work/Target.md":    "---\nstatus: active\ntype: note\n---\nNo links.\n",
		"Archive/Target.md": "---\nstatus: someday\ntype: note\n---\nNo links.\n",
		"Island.md":         "---\nstatus: someday\n---\nNo links.\n",
	})
	cfg := &config.Config{
		Frontmatter: config.FrontmatterConfig{
			Schema: map[string]config.PropertySchema{
				"status": {Type: "string", Required: true, Allowed: []string{"active", "done"}},
				"type":   {Type: "string", Required: true},
			},
		},
	}

	got, err := buildReviewQueue(v, cfg)
	if err != nil {
		t.Fatalf("buildReviewQueue: %v", err)
	}

	if got.Counts.Active != len(got.Items) || got.Counts.TotalGenerated != len(got.Items) {
		t.Fatalf("counts = %#v, items=%d", got.Counts, len(got.Items))
	}
	if got.Counts.DoneHidden != 0 || got.Counts.SnoozedHidden != 0 {
		t.Fatalf("session 1 queue should not hide stateful items yet: %#v", got.Counts)
	}

	wantPrefix := []reviewItemKind{
		reviewKindSchemaViolation,
		reviewKindSchemaViolation,
		reviewKindBrokenTarget,
		reviewKindDuplicateTitle,
	}
	if kinds := reviewItemKinds(got.Items[:len(wantPrefix)]); !sameReviewKinds(kinds, wantPrefix) {
		t.Fatalf("review order prefix = %#v, want %#v; items=%#v", kinds, wantPrefix, reviewItemTitles(got.Items))
	}

	broken := findReviewItem(t, got, reviewKindBrokenTarget, "missing")
	if broken.Count != 1 || broken.Title != "Broken target: missing" {
		t.Fatalf("broken target item = %#v", broken)
	}
	if len(broken.Affected) != 1 {
		t.Fatalf("broken target affected = %#v, want one source", broken.Affected)
	}
	source := broken.Affected[0]
	if source.RelPath != "Work/Source.md" || source.Path != "/n/work/source" || source.FolderPath != "/?folder=Work" {
		t.Fatalf("broken source metadata = %#v", source)
	}
	if source.Location != "line 5" {
		t.Fatalf("broken source location = %q, want line 5", source.Location)
	}
	if source.FileHash == "" {
		t.Fatalf("broken source file hash should be populated: %#v", source)
	}

	duplicate := findReviewItem(t, got, reviewKindDuplicateTitle, "target")
	if duplicate.Count != 2 {
		t.Fatalf("duplicate title item = %#v, want count 2", duplicate)
	}
	if rels := reviewAffectedRelPaths(duplicate.Affected); !sameStrings(rels, []string{"Archive/Target.md", "Work/Target.md"}) {
		t.Fatalf("duplicate affected rels = %#v", rels)
	}

	status := findReviewItem(t, got, reviewKindSchemaViolation, `status invalid value "someday" (allowed: active, done)`)
	if status.Count != 2 {
		t.Fatalf("status schema item = %#v, want count 2", status)
	}
	if status.Affected[0].Location != "frontmatter.status" {
		t.Fatalf("schema affected location = %#v", status.Affected)
	}
}

func TestBuildReviewQueueIncludesFrontmatterParseErrors(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "Good.md", "---\nstatus: active\n---\n[[Missing]]\n")
	writeTestFile(t, root, "Bad.md", "---\n: bad\n---\nBody\n")
	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")

	got, err := buildReviewQueue(v, cfg)
	if err != nil {
		t.Fatalf("buildReviewQueue: %v", err)
	}

	frontmatter := findFirstReviewItemByKind(t, got, reviewKindFrontmatterError)
	if frontmatter.Count != 1 || !strings.Contains(frontmatter.Detail, "Bad.md") {
		t.Fatalf("frontmatter parse item = %#v", frontmatter)
	}
	affected := frontmatter.Affected[0]
	if affected.RelPath != "Bad.md" || affected.Path != "/n/Bad" || affected.Location != "frontmatter" {
		t.Fatalf("frontmatter affected metadata = %#v", affected)
	}
	if affected.FileHash == "" {
		t.Fatalf("frontmatter affected file hash should be populated: %#v", affected)
	}

	broken := findReviewItem(t, got, reviewKindBrokenTarget, "missing")
	if broken.Affected[0].RelPath != "Good.md" {
		t.Fatalf("valid notes should still be reviewed after parse error: %#v", broken)
	}
}

func TestBuildReviewQueueIDsAreDeterministicAndContentAware(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "Source.md", "---\nstatus: active\n---\n[[Missing]]\n")
	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")

	first, err := buildReviewQueue(v, cfg)
	if err != nil {
		t.Fatalf("build first queue: %v", err)
	}
	second, err := buildReviewQueue(v, cfg)
	if err != nil {
		t.Fatalf("build second queue: %v", err)
	}

	firstBroken := findReviewItem(t, first, reviewKindBrokenTarget, "missing")
	secondBroken := findReviewItem(t, second, reviewKindBrokenTarget, "missing")
	if firstBroken.ID != secondBroken.ID || firstBroken.Signature != secondBroken.Signature {
		t.Fatalf("same content produced different IDs:\nfirst=%#v\nsecond=%#v", firstBroken, secondBroken)
	}
	if !strings.HasPrefix(firstBroken.ID, reviewItemIDPrefix) || len(firstBroken.Signature) != 64 {
		t.Fatalf("unexpected id/signature shape: id=%q signature=%q", firstBroken.ID, firstBroken.Signature)
	}

	writeTestFile(t, root, "Source.md", "---\nstatus: active\n---\nChanged text, same issue: [[Missing]]\n")
	_, reopened := testVaultOpen(t, root, "127.0.0.1:0")
	changed, err := buildReviewQueue(reopened, cfg)
	if err != nil {
		t.Fatalf("build changed queue: %v", err)
	}
	changedBroken := findReviewItem(t, changed, reviewKindBrokenTarget, "missing")
	if changedBroken.ID == firstBroken.ID || changedBroken.Signature == firstBroken.Signature {
		t.Fatalf("content change did not rotate review identity:\nfirst=%#v\nchanged=%#v", firstBroken, changedBroken)
	}
}

func findReviewItem(t *testing.T, queue reviewQueue, kind reviewItemKind, detail string) reviewItem {
	t.Helper()
	for _, item := range queue.Items {
		if item.Kind == kind && item.Detail == detail {
			return item
		}
	}
	t.Fatalf("missing review item kind=%s detail=%q in %#v", kind, detail, queue.Items)
	return reviewItem{}
}

func findFirstReviewItemByKind(t *testing.T, queue reviewQueue, kind reviewItemKind) reviewItem {
	t.Helper()
	for _, item := range queue.Items {
		if item.Kind == kind {
			return item
		}
	}
	t.Fatalf("missing review item kind=%s in %#v", kind, queue.Items)
	return reviewItem{}
}

func reviewItemKinds(items []reviewItem) []reviewItemKind {
	kinds := make([]reviewItemKind, 0, len(items))
	for _, item := range items {
		kinds = append(kinds, item.Kind)
	}
	return kinds
}

func reviewItemTitles(items []reviewItem) []string {
	titles := make([]string, 0, len(items))
	for _, item := range items {
		titles = append(titles, item.Title)
	}
	return titles
}

func reviewAffectedRelPaths(affected []reviewAffectedNote) []string {
	rels := make([]string, 0, len(affected))
	for _, note := range affected {
		rels = append(rels, note.RelPath)
	}
	return rels
}

func sameReviewKinds(got, want []reviewItemKind) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
