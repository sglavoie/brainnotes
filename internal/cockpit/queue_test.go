package cockpit

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// newTestVault writes files into a temp dir and opens a vault over it. The
// config is constructed directly so DefaultIgnore is not merged, letting tests
// place fixtures in folders like Archive without them being skipped.
func newTestVault(t *testing.T, files map[string]string) *vault.Vault {
	t.Helper()

	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir parent of %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}

	v, err := vault.Open(&config.Config{Vault: root})
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	return v
}

func schemaConfig() *config.Config {
	return &config.Config{
		Frontmatter: config.FrontmatterConfig{
			Properties: map[string]config.PropertyDefinition{
				"status": {Type: config.KindString, Required: true, Allowed: []string{"active", "done"}},
				"type":   {Type: config.KindString, Required: true},
			},
		},
	}
}

func findTask(t *testing.T, q Queue, kind RuleKind, detail string) Task {
	t.Helper()
	for _, task := range q.Tasks {
		if task.Kind == kind && task.Detail == detail {
			return task
		}
	}
	t.Fatalf("no task with kind=%q detail=%q; tasks=%#v", kind, detail, taskDetails(q.Tasks))
	return Task{}
}

func taskDetails(tasks []Task) []string {
	out := make([]string, len(tasks))
	for i, task := range tasks {
		out[i] = string(task.Kind) + ":" + task.Detail
	}
	return out
}

func taskKinds(tasks []Task) []RuleKind {
	out := make([]RuleKind, len(tasks))
	for i, task := range tasks {
		out[i] = task.Kind
	}
	return out
}

func affectedRels(affected []AffectedNote) []string {
	out := make([]string, len(affected))
	for i, note := range affected {
		out[i] = note.RelPath
	}
	return out
}

func TestBuildQueueGroupsOrdersAndMetadata(t *testing.T) {
	v := newTestVault(t, map[string]string{
		"Work/Source.md":    "---\nstatus: active\ntype: note\n---\n[[Target]] [[Missing]]\n",
		"Work/Target.md":    "---\nstatus: active\ntype: note\n---\nNo links.\n",
		"Archive/Target.md": "---\nstatus: someday\ntype: note\n---\nNo links.\n",
		"Island.md":         "---\nstatus: someday\n---\nNo links.\n",
	})

	got, err := BuildQueue(v, schemaConfig())
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}

	if got.Counts.Active != len(got.Tasks) || got.Counts.TotalGenerated != len(got.Tasks) {
		t.Fatalf("counts = %#v, tasks=%d", got.Counts, len(got.Tasks))
	}

	// Schema violations sort before broken targets before duplicate titles.
	wantPrefix := []RuleKind{
		RuleSchemaViolation,
		RuleSchemaViolation,
		RuleBrokenTarget,
		RuleDuplicateTitle,
	}
	if kinds := taskKinds(got.Tasks[:len(wantPrefix)]); !reflect.DeepEqual(kinds, wantPrefix) {
		t.Fatalf("task order prefix = %#v, want %#v; details=%#v", kinds, wantPrefix, taskDetails(got.Tasks))
	}

	broken := findTask(t, got, RuleBrokenTarget, "missing")
	if broken.Count != 1 || broken.Title != "Broken target: missing" {
		t.Fatalf("broken target task = %#v", broken)
	}
	if len(broken.Affected) != 1 {
		t.Fatalf("broken target affected = %#v, want one source", broken.Affected)
	}
	source := broken.Affected[0]
	if source.RelPath != "Work/Source.md" || source.Folder != "Work" {
		t.Fatalf("broken source metadata = %#v", source)
	}
	if source.Location != "line 5" {
		t.Fatalf("broken source location = %q, want line 5", source.Location)
	}
	if source.FileHash == "" {
		t.Fatalf("broken source file hash should be populated: %#v", source)
	}

	duplicate := findTask(t, got, RuleDuplicateTitle, "target")
	if duplicate.Count != 2 {
		t.Fatalf("duplicate title task = %#v, want count 2", duplicate)
	}
	if rels := affectedRels(duplicate.Affected); !reflect.DeepEqual(rels, []string{"Archive/Target.md", "Work/Target.md"}) {
		t.Fatalf("duplicate affected rels = %#v", rels)
	}

	status := findTask(t, got, RuleSchemaViolation, `status invalid value "someday" (allowed: active, done)`)
	if status.Count != 2 {
		t.Fatalf("status schema task = %#v, want count 2", status)
	}
	if status.Affected[0].Location != "frontmatter.status" {
		t.Fatalf("schema affected location = %#v", status.Affected)
	}
}

func TestBuildQueueIncludesFrontmatterParseErrors(t *testing.T) {
	v := newTestVault(t, map[string]string{
		"Good.md": "---\nstatus: active\n---\n[[Missing]]\n",
		"Bad.md":  "---\nstatus: [unterminated\n---\nBody\n",
	})

	got, err := BuildQueue(v, &config.Config{})
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}

	kinds := taskKinds(got.Tasks)
	if len(kinds) == 0 || kinds[0] != RuleFrontmatterError {
		t.Fatalf("frontmatter error should sort first; kinds=%#v", kinds)
	}
	fm := got.Tasks[0]
	if fm.Count != 1 || len(fm.Affected) != 1 || fm.Affected[0].RelPath != "Bad.md" {
		t.Fatalf("frontmatter task = %#v", fm)
	}
	if fm.Affected[0].Location != "frontmatter" || fm.Affected[0].FileHash == "" {
		t.Fatalf("frontmatter affected metadata = %#v", fm.Affected[0])
	}
}

func TestBuildQueueTableDrivenCounts(t *testing.T) {
	cases := []struct {
		name       string
		files      map[string]string
		cfg        *config.Config
		wantKinds  []RuleKind
		wantActive int
	}{
		{
			name: "linked cycle leaves only a long-tail value task",
			// A 4-note cycle: every note has one inbound and one outbound link,
			// so no orphan/dead-end/broken tasks. status=active is the dominant
			// head (count 3) and status=someday is a lone rare value (count 1)
			// that qualifies for consolidation, leaving exactly one task.
			files: map[string]string{
				"A.md": "---\nstatus: active\n---\n[[B]]\n",
				"B.md": "---\nstatus: active\n---\n[[C]]\n",
				"C.md": "---\nstatus: active\n---\n[[D]]\n",
				"D.md": "---\nstatus: someday\n---\n[[A]]\n",
			},
			cfg:        &config.Config{},
			wantKinds:  []RuleKind{RuleLongTailValue},
			wantActive: 1,
		},
		{
			name: "single broken target is also an orphan and dead end",
			files: map[string]string{
				"A.md": "---\n---\n[[Nowhere]]\n",
			},
			cfg:        &config.Config{},
			wantKinds:  []RuleKind{RuleBrokenTarget, RuleOrphanNote, RuleDeadEndNote},
			wantActive: 3,
		},
		{
			name: "duplicate titles across folders",
			files: map[string]string{
				"One/Dup.md": "---\n---\nx\n",
				"Two/Dup.md": "---\n---\ny\n",
			},
			cfg: &config.Config{},
			wantKinds: []RuleKind{
				RuleDuplicateTitle,
				RuleOrphanNote,
				RuleOrphanNote,
				RuleDeadEndNote,
				RuleDeadEndNote,
			},
			wantActive: 5,
		},
		{
			name: "schema violation with orphan, dead end, and long-tail value",
			// A is an isolated note with the rare status=bogus; B->C->D->B form a
			// linked cycle carrying the dominant head status=active (count 3), so
			// bogus (count 1) qualifies as a long-tail value while the cycle nodes
			// produce no orphan/dead-end tasks.
			files: map[string]string{
				"A.md": "---\nstatus: bogus\n---\nx\n",
				"B.md": "---\nstatus: active\n---\n[[C]]\n",
				"C.md": "---\nstatus: active\n---\n[[D]]\n",
				"D.md": "---\nstatus: active\n---\n[[B]]\n",
			},
			cfg: &config.Config{Frontmatter: config.FrontmatterConfig{
				Properties: map[string]config.PropertyDefinition{
					"status": {Type: config.KindString, Allowed: []string{"active"}},
				},
			}},
			wantKinds: []RuleKind{
				RuleSchemaViolation,
				RuleOrphanNote,
				RuleDeadEndNote,
				RuleLongTailValue,
			},
			wantActive: 4,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newTestVault(t, tc.files)
			got, err := BuildQueue(v, tc.cfg)
			if err != nil {
				t.Fatalf("BuildQueue: %v", err)
			}
			if got.Counts.Active != tc.wantActive {
				t.Fatalf("active = %d, want %d; details=%#v", got.Counts.Active, tc.wantActive, taskDetails(got.Tasks))
			}
			if kinds := taskKinds(got.Tasks); !reflect.DeepEqual(kinds, tc.wantKinds) {
				t.Fatalf("kinds = %#v, want %#v", kinds, tc.wantKinds)
			}
		})
	}
}

// brokenTripletVault yields a single note whose only link is broken, producing
// a broken-target, an orphan, and a dead-end task (built-in order 30/50/60).
func brokenTripletVault(t *testing.T) *vault.Vault {
	t.Helper()
	return newTestVault(t, map[string]string{
		"A.md": "---\n---\n[[Nowhere]]\n",
	})
}

func TestBuildQueueDisabledRulesFilter(t *testing.T) {
	cfg := &config.Config{Cockpit: config.CockpitConfig{
		DisabledRules: []string{string(RuleOrphanNote)},
	}}

	got, err := BuildQueue(brokenTripletVault(t), cfg)
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}

	wantKinds := []RuleKind{RuleBrokenTarget, RuleDeadEndNote}
	if kinds := taskKinds(got.Tasks); !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("kinds = %#v, want %#v", kinds, wantKinds)
	}
	if got.Counts.Active != 2 || got.Counts.TotalGenerated != 2 {
		t.Fatalf("counts = %#v, want active/total 2 after filter", got.Counts)
	}
}

func TestBuildQueuePriorityOverride(t *testing.T) {
	// Name only dead_end_note: it must surface first, ahead of the unnamed
	// kinds, whose relative order (broken_target then orphan_note) is unchanged.
	cfg := &config.Config{Cockpit: config.CockpitConfig{
		Priority: []string{string(RuleDeadEndNote)},
	}}

	got, err := BuildQueue(brokenTripletVault(t), cfg)
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}

	wantKinds := []RuleKind{RuleDeadEndNote, RuleBrokenTarget, RuleOrphanNote}
	if kinds := taskKinds(got.Tasks); !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("kinds = %#v, want %#v", kinds, wantKinds)
	}
}

func TestBuildQueuePriorityOverrideMultipleNamedKeepsListedOrder(t *testing.T) {
	// Named kinds surface first, in listed order, even when that reverses their
	// built-in weights.
	cfg := &config.Config{Cockpit: config.CockpitConfig{
		Priority: []string{string(RuleDeadEndNote), string(RuleOrphanNote)},
	}}

	got, err := BuildQueue(brokenTripletVault(t), cfg)
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}

	wantKinds := []RuleKind{RuleDeadEndNote, RuleOrphanNote, RuleBrokenTarget}
	if kinds := taskKinds(got.Tasks); !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("kinds = %#v, want %#v", kinds, wantKinds)
	}
}

func TestBuildQueueUnknownRuleNamesIgnored(t *testing.T) {
	baseline, err := BuildQueue(brokenTripletVault(t), &config.Config{})
	if err != nil {
		t.Fatalf("BuildQueue baseline: %v", err)
	}

	cfg := &config.Config{Cockpit: config.CockpitConfig{
		DisabledRules: []string{"bogus_rule"},
		Priority:      []string{"also_bogus"},
	}}
	got, err := BuildQueue(brokenTripletVault(t), cfg)
	if err != nil {
		t.Fatalf("BuildQueue with unknown names: %v", err)
	}

	if kinds := taskKinds(got.Tasks); !reflect.DeepEqual(kinds, taskKinds(baseline.Tasks)) {
		t.Fatalf("unknown names changed order: got %#v, baseline %#v", kinds, taskKinds(baseline.Tasks))
	}
	if got.Counts != baseline.Counts {
		t.Fatalf("unknown names changed counts: got %#v, baseline %#v", got.Counts, baseline.Counts)
	}
}

func TestBuildQueueEmptyCockpitListsPreserveOrdering(t *testing.T) {
	// Regression guard: absent lists must leave the built-in ordering intact.
	got, err := BuildQueue(brokenTripletVault(t), &config.Config{})
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}

	wantKinds := []RuleKind{RuleBrokenTarget, RuleOrphanNote, RuleDeadEndNote}
	if kinds := taskKinds(got.Tasks); !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("kinds = %#v, want %#v", kinds, wantKinds)
	}
}

func TestBuildQueueDeterministicAndStableSignatures(t *testing.T) {
	files := map[string]string{
		"Work/Source.md":    "---\nstatus: active\ntype: note\n---\n[[Target]] [[Missing]]\n",
		"Work/Target.md":    "---\nstatus: active\ntype: note\n---\nNo links.\n",
		"Archive/Target.md": "---\nstatus: someday\ntype: note\n---\nNo links.\n",
	}

	first, err := BuildQueue(newTestVault(t, files), schemaConfig())
	if err != nil {
		t.Fatalf("BuildQueue first: %v", err)
	}
	second, err := BuildQueue(newTestVault(t, files), schemaConfig())
	if err != nil {
		t.Fatalf("BuildQueue second: %v", err)
	}

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("queue not deterministic across identical vaults:\n first=%#v\nsecond=%#v", first, second)
	}
	for _, task := range first.Tasks {
		if task.Signature == "" || task.ID != taskIDPrefix+task.Signature {
			t.Fatalf("task id/signature mismatch: %#v", task)
		}
	}
}
