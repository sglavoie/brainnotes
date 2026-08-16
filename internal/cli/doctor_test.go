package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
)

// buildDoctorFixture writes a small vault whose diagnostics are deterministic:
// Projects/Alpha.md carries a broken [[Ghost]] link but opts out of dead-end
// classification; Beta.md links to Alpha (so Beta is an orphan). It returns the
// vault-root config path so callers can rewrite the config if needed.
func buildDoctorFixture(t *testing.T) (root, cfgPath string) {
	t.Helper()
	root = t.TempDir()

	files := map[string]string{
		"Projects/Alpha.md": "---\ntitle: Alpha\ndead_end: false\n---\nSee [[Ghost]].\n",
		"Beta.md":           "---\ntitle: Beta\n---\nLinks to [[Alpha]].\n",
	}
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	cfgPath = useVaultRoot(t, root, "")
	return root, cfgPath
}

func findTaskByTitle(tasks []cockpit.Task, title string) (cockpit.Task, bool) {
	for _, task := range tasks {
		if task.Title == title {
			return task, true
		}
	}
	return cockpit.Task{}, false
}

func TestDoctor_JSONReportsBrokenTarget(t *testing.T) {
	buildDoctorFixture(t)

	out, err := runCLI(t, "", "doctor", "--json")
	if err != nil {
		t.Fatalf("doctor --json: %v", err)
	}

	var queue cockpit.Queue
	if err := json.Unmarshal([]byte(out), &queue); err != nil {
		t.Fatalf("decode queue JSON: %v\noutput:\n%s", err, out)
	}

	task, ok := findTaskByTitle(queue.Tasks, "Broken target: ghost")
	if !ok {
		t.Fatalf("expected broken-target task for ghost, got tasks: %+v", queue.Tasks)
	}
	if task.Kind != cockpit.RuleBrokenTarget {
		t.Errorf("kind = %q, want %q", task.Kind, cockpit.RuleBrokenTarget)
	}
	if task.TriageOnly {
		t.Errorf("broken-target task should not be triage-only")
	}

	var kinds []string
	for _, a := range task.Actions {
		kinds = append(kinds, string(a.Kind))
	}
	joined := strings.Join(kinds, ",")
	for _, want := range []string{"open", "relink", "create_stub", "snooze", "mark_done"} {
		if !strings.Contains(joined, want) {
			t.Errorf("actions %q missing %q", joined, want)
		}
	}

	found := false
	for _, note := range task.Affected {
		if note.RelPath == "Projects/Alpha.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Projects/Alpha.md among affected notes, got %+v", task.Affected)
	}
	if _, ok := findTaskByTitle(queue.Tasks, "Dead-end note: Alpha"); ok {
		t.Errorf("dead_end: false should suppress Alpha's doctor dead-end task")
	}
}

func TestDoctor_HumanOutput(t *testing.T) {
	buildDoctorFixture(t)

	out, err := runCLI(t, "", "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}

	for _, want := range []string{
		"broken_target: Broken target: ghost",
		"Projects/Alpha.md",
		"actions: open, relink, create_stub, snooze, mark_done",
		"active task",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("human output missing %q\noutput:\n%s", want, out)
		}
	}
}

func TestDoctor_FolderScope(t *testing.T) {
	buildDoctorFixture(t)

	out, err := runCLI(t, "", "doctor", "--json", "--folder", "Projects")
	if err != nil {
		t.Fatalf("doctor --folder: %v", err)
	}

	var queue cockpit.Queue
	if err := json.Unmarshal([]byte(out), &queue); err != nil {
		t.Fatalf("decode queue JSON: %v", err)
	}

	if _, ok := findTaskByTitle(queue.Tasks, "Broken target: ghost"); !ok {
		t.Errorf("folder scope should keep the broken-target task under Projects/")
	}
	// Beta.md is at the vault root, so its orphan task must be filtered out.
	for _, task := range queue.Tasks {
		for _, note := range task.Affected {
			if note.RelPath == "Beta.md" {
				t.Errorf("folder scope should drop tasks that only touch Beta.md, got %q", task.Title)
			}
		}
	}
	if queue.Counts.Active != len(queue.Tasks) {
		t.Errorf("Active count %d != len(tasks) %d after scoping", queue.Counts.Active, len(queue.Tasks))
	}
}

// TestDoctor_FolderScopeCountsAllFour verifies that --folder scopes every count
// (Active, DoneHidden, SnoozedHidden, TotalGenerated) to the folder, so
// active+done+snoozed==total and no hidden count leaks from out-of-folder tasks.
// It also guards the persistence trap: scoping must not delete stored done/snoozed
// state for tasks outside the requested folder.
func TestDoctor_FolderScopeCountsAllFour(t *testing.T) {
	root, _ := buildDoctorFixture(t)
	// Add distinct broken-target sources so each note yields its own broken-target
	// task with a single affected note: two under Projects/, two at the root.
	extra := map[string]string{
		"Projects/P1.md": "---\ntitle: P1\n---\nSee [[GhostP1]].\n",
		"Projects/P2.md": "---\ntitle: P2\n---\nSee [[GhostP2]].\n",
		"Root1.md":       "---\ntitle: Root1\n---\nSee [[GhostR1]].\n",
		"Root2.md":       "---\ntitle: Root2\n---\nSee [[GhostR2]].\n",
	}
	for rel, body := range extra {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	// Snapshot the raw queue via the CLI so we learn each task's id + signature.
	rawOut, err := runCLI(t, "", "doctor", "--json")
	if err != nil {
		t.Fatalf("doctor --json: %v", err)
	}
	var rawQueue cockpit.Queue
	if err := json.Unmarshal([]byte(rawOut), &rawQueue); err != nil {
		t.Fatalf("decode raw queue: %v", err)
	}

	mustTask := func(title string) cockpit.Task {
		task, ok := findTaskByTitle(rawQueue.Tasks, title)
		if !ok {
			t.Fatalf("expected task %q in raw queue, got %+v", title, rawQueue.Tasks)
		}
		return task
	}
	inFolderDone := mustTask("Broken target: ghostp1")
	inFolderSnoozed := mustTask("Broken target: ghostp2")
	outFolderDone := mustTask("Broken target: ghostr1")
	outFolderSnoozed := mustTask("Broken target: ghostr2")

	// Mark two Projects tasks and two root tasks so the store holds both in- and
	// out-of-folder suppressions.
	store := cockpit.NewStateStore(root)
	for _, task := range []cockpit.Task{inFolderDone, outFolderDone} {
		if err := store.MarkDone(task); err != nil {
			t.Fatalf("MarkDone %q: %v", task.Title, err)
		}
	}
	for _, task := range []cockpit.Task{inFolderSnoozed, outFolderSnoozed} {
		if err := store.Snooze(task, cockpit.SnoozeTomorrow); err != nil {
			t.Fatalf("Snooze %q: %v", task.Title, err)
		}
	}

	scopedOut, err := runCLI(t, "", "doctor", "--json", "--folder", "Projects")
	if err != nil {
		t.Fatalf("doctor --folder: %v", err)
	}
	var scoped cockpit.Queue
	if err := json.Unmarshal([]byte(scopedOut), &scoped); err != nil {
		t.Fatalf("decode scoped queue: %v", err)
	}

	// Only the two Projects suppressions count; the two root suppressions do not
	// leak into the scoped hidden counts.
	if scoped.Counts.DoneHidden != 1 {
		t.Errorf("DoneHidden = %d, want 1 (Projects only)", scoped.Counts.DoneHidden)
	}
	if scoped.Counts.SnoozedHidden != 1 {
		t.Errorf("SnoozedHidden = %d, want 1 (Projects only)", scoped.Counts.SnoozedHidden)
	}
	if scoped.Counts.Active != len(scoped.Tasks) {
		t.Errorf("Active = %d, want len(tasks) = %d", scoped.Counts.Active, len(scoped.Tasks))
	}
	if got, want := scoped.Counts.TotalGenerated, scoped.Counts.Active+scoped.Counts.DoneHidden+scoped.Counts.SnoozedHidden; got != want {
		t.Errorf("TotalGenerated = %d, want active+done+snoozed = %d", got, want)
	}
	// The suppressed Projects tasks must be gone from the active list.
	for _, title := range []string{"Broken target: ghostp1", "Broken target: ghostp2"} {
		if _, ok := findTaskByTitle(scoped.Tasks, title); ok {
			t.Errorf("suppressed task %q should not appear in scoped active list", title)
		}
	}
	// No out-of-folder task may appear in the scoped active list either.
	for _, task := range scoped.Tasks {
		for _, note := range task.Affected {
			rel := filepath.ToSlash(note.RelPath)
			if rel != "Projects" && !strings.HasPrefix(rel, "Projects/") {
				t.Errorf("scoped active task %q touches out-of-folder note %q", task.Title, rel)
			}
		}
	}

	// Regression: scoping must NOT delete the out-of-folder done/snoozed records.
	statePath := filepath.Join(root, ".bnotes", "cockpit.json")
	stateData, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read cockpit state: %v", err)
	}
	var state struct {
		Items map[string]struct {
			State string `json:"state"`
		} `json:"items"`
	}
	if err := json.Unmarshal(stateData, &state); err != nil {
		t.Fatalf("decode cockpit state: %v", err)
	}
	for _, task := range []cockpit.Task{outFolderDone, outFolderSnoozed} {
		if _, ok := state.Items[task.ID]; !ok {
			t.Errorf("scoping deleted out-of-folder stored state for %q (id %s)", task.Title, task.ID)
		}
	}
	if len(state.Items) != 4 {
		t.Errorf("cockpit state should still hold all 4 suppressions, got %d", len(state.Items))
	}
}

// TestDoctor_DailyLimitsTasks proves --daily caps the surfaced tasks to
// cockpit.daily_limit while the default view returns the full queue. The daily
// slice is the top of the full queue (no reorder) and the counts still report
// the full totals so the footer stays accurate.
func TestDoctor_DailyLimitsTasks(t *testing.T) {
	root, cfgPath := buildDoctorFixture(t)
	// Add extra broken-target sources so the full queue clearly exceeds the
	// daily limit set below.
	extra := map[string]string{
		"G1.md": "---\ntitle: G1\n---\nSee [[GhostG1]].\n",
		"G2.md": "---\ntitle: G2\n---\nSee [[GhostG2]].\n",
		"G3.md": "---\ntitle: G3\n---\nSee [[GhostG3]].\n",
	}
	for rel, body := range extra {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	// Cap the daily view to 2 tasks.
	cfg := "[cockpit]\ndaily_limit = 2\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("rewrite config: %v", err)
	}

	fullOut, err := runCLI(t, "", "doctor", "--json")
	if err != nil {
		t.Fatalf("doctor --json: %v", err)
	}
	var full cockpit.Queue
	if err := json.Unmarshal([]byte(fullOut), &full); err != nil {
		t.Fatalf("decode full queue: %v", err)
	}
	if len(full.Tasks) <= 2 {
		t.Fatalf("expected full queue to exceed the daily limit, got %d tasks", len(full.Tasks))
	}

	dailyOut, err := runCLI(t, "", "doctor", "--json", "--daily")
	if err != nil {
		t.Fatalf("doctor --json --daily: %v", err)
	}
	var daily cockpit.Queue
	if err := json.Unmarshal([]byte(dailyOut), &daily); err != nil {
		t.Fatalf("decode daily queue: %v", err)
	}
	if len(daily.Tasks) != 2 {
		t.Fatalf("daily queue = %d tasks, want 2 (daily_limit)", len(daily.Tasks))
	}
	if daily.Counts.Active != full.Counts.Active {
		t.Errorf("daily Active = %d, want full Active %d (counts stay intact)", daily.Counts.Active, full.Counts.Active)
	}
	if daily.Counts.TotalGenerated != full.Counts.TotalGenerated {
		t.Errorf("daily TotalGenerated = %d, want %d", daily.Counts.TotalGenerated, full.Counts.TotalGenerated)
	}
	for i, task := range daily.Tasks {
		if task.ID != full.Tasks[i].ID {
			t.Errorf("daily task %d id = %q, want %q (top of full queue)", i, task.ID, full.Tasks[i].ID)
		}
	}
}

func TestDoctor_ReviewContext(t *testing.T) {
	root, cfgPath := buildDoctorFixture(t)
	// Add extra broken-target sources so the full queue exceeds the daily cap
	// and we can assert --review-context applies the daily limit.
	extra := map[string]string{
		"G1.md": "---\ntitle: G1\n---\nSee [[GhostG1]].\n",
		"G2.md": "---\ntitle: G2\n---\nSee [[GhostG2]].\n",
		"G3.md": "---\ntitle: G3\n---\nSee [[GhostG3]].\n",
	}
	for rel, body := range extra {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	cfg := "[cockpit]\ndaily_limit = 2\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("rewrite config: %v", err)
	}

	out, err := runCLI(t, "", "doctor", "--review-context")
	if err != nil {
		t.Fatalf("doctor --review-context: %v", err)
	}

	var ctx cockpit.ReviewContext
	if err := json.Unmarshal([]byte(out), &ctx); err != nil {
		t.Fatalf("decode review context: %v\noutput:\n%s", err, out)
	}

	// --review-context implies --daily, so only daily_limit tasks surface while
	// the counts stay the full active totals.
	if ctx.TaskCount != 2 {
		t.Errorf("TaskCount = %d, want 2 (daily_limit)", ctx.TaskCount)
	}
	if len(ctx.Items) != 2 {
		t.Errorf("Items = %d, want 2", len(ctx.Items))
	}
	if ctx.Counts.Active <= 2 {
		t.Errorf("Counts.Active = %d, want the full active total > 2", ctx.Counts.Active)
	}
	for _, item := range ctx.Items {
		if item.Label == "" {
			t.Errorf("item %q missing a kind label", item.Title)
		}
	}
}

func TestDoctor_CleanVault(t *testing.T) {
	root := t.TempDir()
	// Two mutually linked, frontmatter-free notes: neither is an orphan or dead
	// end, and with no frontmatter properties there are no long-tail values.
	files := map[string]string{
		"One.md": "See [[Two]].\n",
		"Two.md": "See [[One]].\n",
	}
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	useVaultRoot(t, root, "")

	out, err := runCLI(t, "", "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, "No hygiene tasks. Vault is clean.") {
		t.Errorf("expected clean-vault message, got:\n%s", out)
	}
}
