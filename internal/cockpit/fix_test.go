package cockpit

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/properties"
	"github.com/sglavoie/sglavoie_brainnotes/internal/schema"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func actionKinds(actions []Action) []ActionKind {
	out := make([]ActionKind, len(actions))
	for i, a := range actions {
		out[i] = a.Kind
	}
	return out
}

func findTaskByKind(t *testing.T, q Queue, kind RuleKind) Task {
	t.Helper()
	for _, task := range q.Tasks {
		if task.Kind == kind {
			return task
		}
	}
	t.Fatalf("no task with kind=%q; tasks=%#v", kind, taskDetails(q.Tasks))
	return Task{}
}

func readVaultFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %q: %v", rel, err)
	}
	return string(data)
}

func TestActionsForRuleMetadata(t *testing.T) {
	cases := []struct {
		kind RuleKind
		want []ActionKind
	}{
		{RuleSchemaViolation, []ActionKind{ActionOpen, ActionSchemaRepair, ActionSnooze, ActionMarkDone}},
		{RuleBrokenTarget, []ActionKind{ActionOpen, ActionRelink, ActionCreateStub, ActionSnooze, ActionMarkDone}},
		{RuleDeadEndNote, []ActionKind{ActionOpen, ActionExemptDeadEnd, ActionSnooze, ActionMarkDone}},
		{RuleLongTailValue, []ActionKind{ActionOpen, ActionCleanLongTail, ActionSnooze, ActionMarkDone}},
		{RuleOrphanNote, []ActionKind{ActionOpen, ActionSnooze, ActionMarkDone}},
		{RuleFrontmatterError, []ActionKind{ActionOpen, ActionSnooze, ActionMarkDone}},
	}
	for _, tc := range cases {
		if got := actionKinds(actionsForRule(tc.kind)); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("actionsForRule(%q) = %#v, want %#v", tc.kind, got, tc.want)
		}
	}
}

func TestFixerSchemaRepairPreviewAndApply(t *testing.T) {
	cfg := &config.Config{Frontmatter: config.FrontmatterConfig{
		Properties: map[string]config.PropertyDefinition{
			"status": {Type: config.KindString, Allowed: []string{"active", "done"}},
		},
	}}
	v := newTestVault(t, map[string]string{
		"Note.md": "---\nstatus: Active\n---\nBody.\n",
	})

	q, err := BuildQueue(v, cfg)
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}
	task := findTaskByKind(t, q, RuleSchemaViolation)

	fixer := NewFixer(v, cfg, "bn test fix")

	plan, err := fixer.Plan(task, ActionSchemaRepair)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !plan.Changed() || len(plan.Previews) != 1 {
		t.Fatalf("plan = %#v, want one changed preview", plan)
	}
	change := plan.Previews[0].Changes[0]
	if change.Key != "status" || change.Old.Text != "Active" || change.New.Text != "active" {
		t.Fatalf("preview change = %#v, want status Active -> active", change)
	}
	// Preview must not touch the file.
	if got := readVaultFile(t, v.Root, "Note.md"); !strings.Contains(got, "status: Active") {
		t.Fatalf("preview mutated the note: %q", got)
	}

	out, err := fixer.Apply(task, ActionSchemaRepair)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !reflect.DeepEqual(out.Changed, []string{"Note.md"}) {
		t.Fatalf("out.Changed = %#v, want [Note.md]", out.Changed)
	}
	if out.Journal == nil {
		t.Fatal("apply journal = nil, want entry")
	}
	if got := readVaultFile(t, v.Root, "Note.md"); !strings.Contains(got, "status: active") || strings.Contains(got, "Active") {
		t.Fatalf("apply did not canonicalize status: %q", got)
	}
}

func TestCockpitRepairPreservesMidnightDatetimeSpelling(t *testing.T) {
	definitions := map[string]config.PropertyDefinition{
		"published": {Type: config.KindDatetime},
		"timeline":  {Type: config.KindList, Items: config.KindDatetime},
		"reviewed":  {Type: config.KindDate},
	}
	cfg := &config.Config{Frontmatter: config.FrontmatterConfig{Properties: definitions}}
	v := newTestVault(t, map[string]string{
		"Note.md": "---\n" +
			"published: '2026-03-01T00:00:00Z'\n" +
			"reviewed: '2026-03-05'\n" +
			"timeline:\n" +
			"  - '2026-03-02T00:00:00Z'\n" +
			"  - '2026-03-02T00:00:00Z'\n" +
			"---\nBody.\n",
	})

	for repaired := 0; repaired < len(definitions); repaired++ {
		queue, err := BuildQueue(v, cfg)
		if err != nil {
			t.Fatalf("BuildQueue: %v", err)
		}
		var task *Task
		for index := range queue.Tasks {
			if queue.Tasks[index].Kind == RuleSchemaViolation {
				task = &queue.Tasks[index]
				break
			}
		}
		if task == nil {
			t.Fatalf("only %d schema repairs were offered", repaired)
		}
		if _, err := NewFixer(v, cfg, "bn test fix").Apply(*task, ActionSchemaRepair); err != nil {
			t.Fatalf("apply schema repair: %v", err)
		}
	}

	data, err := os.ReadFile(filepath.Join(v.Root, "Note.md"))
	if err != nil {
		t.Fatalf("read repaired note: %v", err)
	}
	doc, err := frontmatter.Parse(data)
	if err != nil {
		t.Fatalf("reparse repaired note: %v", err)
	}
	published, _ := properties.ClassifyProperty(&doc.Front, "published")
	if published.Kind != config.KindDatetime {
		t.Fatalf("published kind = %q, want datetime", published.Kind)
	}
	timeline, _ := properties.ClassifyProperty(&doc.Front, "timeline")
	if len(timeline.Items) != 1 || timeline.Items[0].Kind != config.KindDatetime {
		t.Fatalf("timeline = %#v, want one datetime", timeline)
	}
	reviewed, _ := properties.ClassifyProperty(&doc.Front, "reviewed")
	if reviewed.Kind != config.KindDate {
		t.Fatalf("reviewed kind = %q, want date", reviewed.Kind)
	}
	if violations := schema.ValidateNote(&doc.Front, "Note.md", definitions); len(violations) != 0 {
		t.Fatalf("violations after repair = %#v", violations)
	}
	got := string(data)
	for _, want := range []string{
		"published: 2026-03-01T00:00:00Z",
		"- 2026-03-02T00:00:00Z",
		"reviewed: 2026-03-05\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("repaired note missing %q:\n%s", want, got)
		}
	}
}

// relinkVaultFiles reproduces the CLI relink fixture: a backslash-path link that
// resolves deterministically after normalization, and an ambiguous-title link
// that cannot.
func relinkVaultFiles() map[string]string {
	return map[string]string{
		"Projects/Roadmap.md": "---\ntitle: Roadmap\n---\nRoadmap body.\n",
		"Projects/Atlas.md":   "---\ntitle: Atlas\n---\nAtlas body.\n",
		"Areas/Atlas.md":      "---\ntitle: Atlas\n---\nArea Atlas body.\n",
		"Notes/Ref.md": "---\n" +
			"categories:\n" +
			"  - \"[[Projects\\\\Roadmap]]\"\n" +
			"  - \"[[Missing\\\\Atlas]]\"\n" +
			"---\n" +
			"See [[Projects\\Roadmap]] and [[Missing\\Atlas]].\n",
	}
}

func TestFixerRelinkDeterministicRewrite(t *testing.T) {
	cfg := &config.Config{}
	v := newTestVault(t, relinkVaultFiles())

	q, err := BuildQueue(v, cfg)
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}

	// The roadmap link resolves deterministically after backslash normalization.
	var roadmap, atlas Task
	for _, task := range q.Tasks {
		if task.Kind != RuleBrokenTarget {
			continue
		}
		if strings.Contains(task.Detail, "roadmap") {
			roadmap = task
		}
		if strings.Contains(task.Detail, "atlas") {
			atlas = task
		}
	}
	if roadmap.ID == "" || atlas.ID == "" {
		t.Fatalf("expected roadmap and atlas broken-target tasks; details=%#v", taskDetails(q.Tasks))
	}

	fixer := NewFixer(v, cfg, "bn test fix")

	plan, err := fixer.Plan(roadmap, ActionRelink)
	if err != nil {
		t.Fatalf("Plan roadmap: %v", err)
	}
	if !plan.Changed() {
		t.Fatalf("roadmap relink plan should change Ref.md: %#v", plan)
	}

	out, err := fixer.Apply(roadmap, ActionRelink)
	if err != nil {
		t.Fatalf("Apply roadmap: %v", err)
	}
	if !reflect.DeepEqual(out.Changed, []string{"Notes/Ref.md"}) {
		t.Fatalf("out.Changed = %#v, want [Notes/Ref.md]", out.Changed)
	}
	if out.Journal == nil {
		t.Fatal("relink journal = nil, want entry")
	}
	got := readVaultFile(t, v.Root, "Notes/Ref.md")
	if strings.Count(got, "[[Projects/Roadmap]]") != 2 {
		t.Fatalf("expected body + frontmatter rewrite to [[Projects/Roadmap]]:\n%s", got)
	}
	if strings.Contains(got, `[[Projects\Roadmap]]`) {
		t.Fatalf("backslash roadmap link should be rewritten:\n%s", got)
	}

	// The ambiguous Atlas link has no deterministic target and is skipped.
	atlasPlan, err := fixer.Plan(atlas, ActionRelink)
	if err != nil {
		t.Fatalf("Plan atlas: %v", err)
	}
	if atlasPlan.Changed() {
		t.Fatalf("atlas relink should find no deterministic target: %#v", atlasPlan)
	}
	if len(atlasPlan.Skipped) == 0 {
		t.Fatalf("atlas relink should record a skip: %#v", atlasPlan)
	}
}

func TestFixerCreateStubForBrokenTarget(t *testing.T) {
	cfg := &config.Config{}
	v := newTestVault(t, map[string]string{
		"Source.md": "---\ntitle: Source\n---\n[[Nowhere]]\n",
	})

	q, err := BuildQueue(v, cfg)
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}
	task := findTaskByKind(t, q, RuleBrokenTarget)

	fixer := NewFixer(v, cfg, "bn test fix")

	plan, err := fixer.Plan(task, ActionCreateStub)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Previews) != 1 || plan.Previews[0].RelPath != "Nowhere.md" {
		t.Fatalf("plan = %#v, want a Nowhere.md preview", plan)
	}

	out, err := fixer.Apply(task, ActionCreateStub)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !reflect.DeepEqual(out.Created, []string{"Nowhere.md"}) {
		t.Fatalf("out.Created = %#v, want [Nowhere.md]", out.Created)
	}
	if out.Journal == nil {
		t.Fatal("create-stub journal = nil, want entry")
	}
	if got := readVaultFile(t, v.Root, "Nowhere.md"); !strings.Contains(got, "title: Nowhere") {
		t.Fatalf("stub note missing title: %q", got)
	}
}

func TestFixerCreateStubConflictSurfacesError(t *testing.T) {
	// A folder-qualified broken target whose destination is occupied on disk by
	// an ignored (thus unresolvable) note must surface a conflict rather than
	// swallowing it into a successful no-op skip.
	root := t.TempDir()
	cfg := &config.Config{Vault: root, Ignore: []string{"Archive"}}
	writeFile := func(rel, body string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir parent of %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	writeFile("Source.md", "---\ntitle: Source\n---\n[[Archive/Nowhere]]\n")
	writeFile("Archive/Nowhere.md", "---\ntitle: Nowhere\n---\noccupied\n")

	v, err := vault.Open(cfg)
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}

	q, err := BuildQueue(v, cfg)
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}
	task := findTaskByKind(t, q, RuleBrokenTarget)

	fixer := NewFixer(v, cfg, "bn test fix")

	if _, err := fixer.Plan(task, ActionCreateStub); noteops.CodeOf(err) != noteops.CodeConflict {
		t.Fatalf("Plan conflict error = %v (code %q), want CodeConflict", err, noteops.CodeOf(err))
	}
	if _, err := fixer.Apply(task, ActionCreateStub); noteops.CodeOf(err) != noteops.CodeConflict {
		t.Fatalf("Apply conflict error = %v (code %q), want CodeConflict", err, noteops.CodeOf(err))
	}
	// The occupied destination must be left untouched.
	if got := readVaultFile(t, v.Root, "Archive/Nowhere.md"); !strings.Contains(got, "occupied") {
		t.Fatalf("occupied destination clobbered: %q", got)
	}
}

func TestFixerRejectsTriageAndUnofferedActions(t *testing.T) {
	cfg := &config.Config{}
	v := newTestVault(t, map[string]string{
		"Source.md": "---\ntitle: Source\n---\n[[Nowhere]]\n",
	})
	q, err := BuildQueue(v, cfg)
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}
	broken := findTaskByKind(t, q, RuleBrokenTarget)
	orphan := findTaskByKind(t, q, RuleOrphanNote)

	fixer := NewFixer(v, cfg, "")

	// A triage action has no planner even when the task offers it.
	if _, err := fixer.Plan(broken, ActionOpen); err == nil {
		t.Fatal("Plan(ActionOpen) should error: no planner")
	}
	// A fix action the task does not offer is rejected before loading.
	if _, err := fixer.Plan(orphan, ActionSchemaRepair); err == nil {
		t.Fatal("Plan(orphan, ActionSchemaRepair) should error: not offered")
	}
	if _, err := fixer.Apply(orphan, ActionRelink); err == nil {
		t.Fatal("Apply(orphan, ActionRelink) should error: not offered")
	}
}
