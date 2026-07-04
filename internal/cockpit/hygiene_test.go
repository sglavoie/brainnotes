package cockpit

import (
	"reflect"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// tasksOfKind returns the queue tasks matching kind, preserving queue order.
func tasksOfKind(q Queue, kind RuleKind) []Task {
	out := make([]Task, 0)
	for _, task := range q.Tasks {
		if task.Kind == kind {
			out = append(out, task)
		}
	}
	return out
}

func TestRulePriorityAndTriageOnly(t *testing.T) {
	cases := []struct {
		kind       RuleKind
		priority   int
		triageOnly bool
	}{
		{RuleFrontmatterError, 10, true},
		{RuleSchemaViolation, 20, false},
		{RuleBrokenTarget, 30, false},
		{RuleDuplicateTitle, 40, true},
		{RuleOrphanNote, 50, true},
		{RuleDeadEndNote, 60, true},
		{RuleLongTailValue, 70, true},
		{RuleKind("unknown_rule"), 100, true},
	}
	for _, tc := range cases {
		if got := rulePriority(tc.kind); got != tc.priority {
			t.Errorf("rulePriority(%q) = %d, want %d", tc.kind, got, tc.priority)
		}
		if got := ruleTriageOnly(tc.kind); got != tc.triageOnly {
			t.Errorf("ruleTriageOnly(%q) = %v, want %v", tc.kind, got, tc.triageOnly)
		}
	}
}

func TestBuildQueueOrphanAndDeadEndTasks(t *testing.T) {
	// Hub links to Leaf: Hub has no inbound link (orphan) but has an outgoing
	// link (not dead-end); Leaf has an inbound link (not orphan) but no
	// outgoing link (dead-end).
	v := newTestVault(t, map[string]string{
		"Hub.md":  "---\n---\n[[Leaf]]\n",
		"Leaf.md": "---\n---\nJust text, no links.\n",
	})

	got, err := BuildQueue(v, &config.Config{})
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}

	orphans := tasksOfKind(got, RuleOrphanNote)
	if len(orphans) != 1 {
		t.Fatalf("orphan tasks = %#v, want exactly one", orphans)
	}
	orphan := orphans[0]
	if orphan.Title != "Orphan note: Hub" || orphan.Summary != "Hub has no inbound links" {
		t.Fatalf("orphan task title/summary = %q / %q", orphan.Title, orphan.Summary)
	}
	if orphan.Priority != 50 || !orphan.TriageOnly {
		t.Fatalf("orphan priority/triage = %d / %v, want 50 / true", orphan.Priority, orphan.TriageOnly)
	}
	if orphan.Count != 1 || !reflect.DeepEqual(affectedRels(orphan.Affected), []string{"Hub.md"}) {
		t.Fatalf("orphan affected = %#v", orphan.Affected)
	}
	if orphan.Affected[0].FileHash == "" {
		t.Fatalf("orphan affected file hash should be populated: %#v", orphan.Affected[0])
	}

	deadEnds := tasksOfKind(got, RuleDeadEndNote)
	if len(deadEnds) != 1 {
		t.Fatalf("dead-end tasks = %#v, want exactly one", deadEnds)
	}
	deadEnd := deadEnds[0]
	if deadEnd.Title != "Dead-end note: Leaf" || deadEnd.Summary != "Leaf has no outgoing links" {
		t.Fatalf("dead-end task title/summary = %q / %q", deadEnd.Title, deadEnd.Summary)
	}
	if deadEnd.Priority != 60 || !deadEnd.TriageOnly {
		t.Fatalf("dead-end priority/triage = %d / %v, want 60 / true", deadEnd.Priority, deadEnd.TriageOnly)
	}
	if !reflect.DeepEqual(affectedRels(deadEnd.Affected), []string{"Leaf.md"}) {
		t.Fatalf("dead-end affected = %#v", deadEnd.Affected)
	}
}

func TestBuildQueueLongTailValueTasks(t *testing.T) {
	// type=note appears twice, type=idea once. Both are among the rarest five
	// values, so both become long-tail triage tasks.
	v := newTestVault(t, map[string]string{
		"A.md": "---\ntype: note\n---\n[[B]]\n",
		"B.md": "---\ntype: note\n---\n[[A]]\n",
		"C.md": "---\ntype: idea\n---\n[[A]]\n",
	})

	got, err := BuildQueue(v, &config.Config{})
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}

	idea := findTask(t, got, RuleLongTailValue, "type = idea")
	if idea.Title != "Long-tail value: type = idea" {
		t.Fatalf("long-tail title = %q", idea.Title)
	}
	if idea.Priority != 70 || !idea.TriageOnly {
		t.Fatalf("long-tail priority/triage = %d / %v, want 70 / true", idea.Priority, idea.TriageOnly)
	}
	if idea.Count != 1 || !reflect.DeepEqual(affectedRels(idea.Affected), []string{"C.md"}) {
		t.Fatalf("long-tail idea affected = %#v", idea.Affected)
	}
	if idea.Affected[0].Location != "frontmatter.type" {
		t.Fatalf("long-tail affected location = %q, want frontmatter.type", idea.Affected[0].Location)
	}

	note := findTask(t, got, RuleLongTailValue, "type = note")
	if note.Count != 2 || !reflect.DeepEqual(affectedRels(note.Affected), []string{"A.md", "B.md"}) {
		t.Fatalf("long-tail note affected = %#v", note.Affected)
	}
}

func TestBuildQueueLongTailHonorsLimit(t *testing.T) {
	// Six distinct single-use values for one property; only the rarest five
	// survive the long-tail cap. With equal counts the tie-breaker is value
	// ascending, so the first value ("aaa") is dropped.
	v := newTestVault(t, map[string]string{
		"1.md": "---\ncategory: aaa\n---\nx\n",
		"2.md": "---\ncategory: bbb\n---\nx\n",
		"3.md": "---\ncategory: ccc\n---\nx\n",
		"4.md": "---\ncategory: ddd\n---\nx\n",
		"5.md": "---\ncategory: eee\n---\nx\n",
		"6.md": "---\ncategory: fff\n---\nx\n",
	})

	got, err := BuildQueue(v, &config.Config{})
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}

	details := make([]string, 0)
	for _, task := range tasksOfKind(got, RuleLongTailValue) {
		details = append(details, task.Detail)
	}
	want := []string{
		"category = bbb",
		"category = ccc",
		"category = ddd",
		"category = eee",
		"category = fff",
	}
	if !reflect.DeepEqual(details, want) {
		t.Fatalf("long-tail details = %#v, want %#v", details, want)
	}
}

func TestBuildQueueHygieneTasksSortAfterCoreRules(t *testing.T) {
	// A broken target (priority 30) must sort before orphan (50), dead-end
	// (60), and long-tail (70) tasks in the assembled queue.
	v := newTestVault(t, map[string]string{
		"Hub.md":  "---\ntype: note\n---\n[[Leaf]] [[Missing]]\n",
		"Leaf.md": "---\ntype: idea\n---\nNo links.\n",
	})

	got, err := BuildQueue(v, &config.Config{})
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}

	lastPriority := -1
	for _, task := range got.Tasks {
		if task.Priority < lastPriority {
			t.Fatalf("tasks not sorted by priority: %#v", taskKinds(got.Tasks))
		}
		lastPriority = task.Priority
	}

	// Sanity: the queue actually contains the hygiene kinds under test.
	for _, kind := range []RuleKind{RuleBrokenTarget, RuleOrphanNote, RuleDeadEndNote, RuleLongTailValue} {
		if len(tasksOfKind(got, kind)) == 0 {
			t.Fatalf("expected at least one %q task; kinds=%#v", kind, taskKinds(got.Tasks))
		}
	}
}
