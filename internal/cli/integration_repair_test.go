package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEndToEndScenario_RepairWorkflow exercises the intended lint → repair
// --dry-run → repair pipeline against a vault with schema violations, verifying
// that lint detects issues, dry-run previews without writing, and apply fixes
// the safe ones while skipping the unfixable.
func TestEndToEndScenario_RepairWorkflow(t *testing.T) {
	root := t.TempDir()

	files := map[string]string{
		"clean.md":     "---\ncreated: 2025-01-01\nstatus: WIP\ntags:\n  - go\n---\nClean note.\n",
		"bad_case.md":  "---\ncreated: 2025-01-01\nstatus: wip\ntags:\n  - go\n---\nCase drift.\n",
		"dup_tags.md":  "---\ncreated: 2025-01-01\nstatus: Done\ntags:\n  - go\n  - go\n---\nDup tags.\n",
		"unfixable.md": "---\ncreated: 2025-01-01\nstatus: bananas\ntags:\n  - go\n---\nBad value.\n",
	}
	for rel, body := range files {
		full := filepath.Join(root, rel)
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")
	cfg := `vault = "` + root + `"

[frontmatter.schema.status]
type     = "string"
required = true
allowed  = ["WIP", "Done", "Archived"]

[frontmatter.schema.tags]
type     = "list"
required = true

[frontmatter.schema.created]
type     = "date"
required = true
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("BNOTES_CONFIG", cfgPath)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_VAULT", "")

	_, lintErr := runCLI(t, "", "lint")
	if lintErr == nil {
		t.Fatal("expected lint to report violations (non-zero exit)")
	}

	dryOut, err := runCLI(t, "", "repair", "--dry-run")
	if err != nil {
		t.Fatalf("repair --dry-run: %v\n%s", err, dryOut)
	}
	if !strings.Contains(dryOut, "--- bad_case.md") {
		t.Errorf("expected bad_case.md in dry-run preview, got:\n%s", dryOut)
	}
	if !strings.Contains(dryOut, "--- dup_tags.md") {
		t.Errorf("expected dup_tags.md in dry-run preview, got:\n%s", dryOut)
	}
	if strings.Contains(dryOut, "--- clean.md") {
		t.Errorf("clean.md should not appear in dry-run preview, got:\n%s", dryOut)
	}
	badCaseAfterDry := readNote(t, filepath.Join(root, "bad_case.md"))
	if strings.Contains(badCaseAfterDry, "status: WIP") {
		t.Fatal("dry-run should not have modified bad_case.md")
	}

	applyOut, err := runCLI(t, "", "repair")
	if err != nil {
		t.Fatalf("repair: %v\n%s", err, applyOut)
	}
	if !strings.Contains(applyOut, "repaired:") {
		t.Errorf("expected 'repaired:' lines, got:\n%s", applyOut)
	}
	if !strings.Contains(applyOut, "SKIP:") {
		t.Errorf("expected SKIP lines for unfixable issues, got:\n%s", applyOut)
	}
	if !strings.Contains(applyOut, "skipped") {
		t.Errorf("expected skip count in summary, got:\n%s", applyOut)
	}

	got := readNote(t, filepath.Join(root, "bad_case.md"))
	if !strings.Contains(got, "status: WIP") {
		t.Errorf("bad_case.md should have status: WIP after repair, got:\n%s", got)
	}
	assertAlphabeticalKeys(t, got)

	got = readNote(t, filepath.Join(root, "dup_tags.md"))
	if strings.Count(got, "- go") != 1 {
		t.Errorf("dup_tags.md should have one 'go' tag after repair, got:\n%s", got)
	}
	assertAlphabeticalKeys(t, got)

	got = readNote(t, filepath.Join(root, "clean.md"))
	if !strings.Contains(got, "status: WIP") {
		t.Errorf("clean.md should be unchanged, got:\n%s", got)
	}

	got = readNote(t, filepath.Join(root, "unfixable.md"))
	if !strings.Contains(got, "status: bananas") {
		t.Errorf("unfixable.md status should still be 'bananas', got:\n%s", got)
	}

	_, lintErr = runCLI(t, "", "lint")
	if lintErr == nil {
		t.Fatal("expected lint to still report unfixable violations")
	}
}
