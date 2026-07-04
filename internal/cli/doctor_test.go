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
// Projects/Alpha.md carries a broken [[Ghost]] link (so it is a broken-target
// source and a dead end) and Beta.md links to Alpha (so Beta is an orphan). It
// wires BNOTES_CONFIG at cfgPath so callers can rewrite the config if needed.
func buildDoctorFixture(t *testing.T) (root, cfgPath string) {
	t.Helper()
	root = t.TempDir()

	files := map[string]string{
		"Projects/Alpha.md": "---\ntitle: Alpha\n---\nSee [[Ghost]].\n",
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

	cfgDir := t.TempDir()
	cfgPath = filepath.Join(cfgDir, "config.toml")
	cfg := "vault = \"" + root + "\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("BNOTES_CONFIG", cfgPath)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_VAULT", "")
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
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("vault = \""+root+"\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("BNOTES_CONFIG", cfgPath)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_VAULT", "")

	out, err := runCLI(t, "", "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, "No hygiene tasks. Vault is clean.") {
		t.Errorf("expected clean-vault message, got:\n%s", out)
	}
}
