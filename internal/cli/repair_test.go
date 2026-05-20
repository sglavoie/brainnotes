package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildRepairFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"clean.md":       "---\ncreated: 2025-01-01\nstatus: WIP\ntags:\n  - go\n---\nClean note.\n",
		"bad_case.md":    "---\ncreated: 2025-01-01\nstatus: wip\ntags:\n  - go\n---\nCase drift.\n",
		"dup_tags.md":    "---\ncreated: 2025-01-01\nstatus: Done\ntags:\n  - go\n  - go\n---\nDup tags.\n",
		"wrong_type.md":  "---\ncreated: \"2025-01-01\"\nstatus: Done\ntags:\n  - go\n---\nString date.\n",
		"unfixable.md":   "---\ncreated: 2025-01-01\nstatus: bananas\ntags:\n  - go\n---\nBad value.\n",
		"no_status.md":   "---\ncreated: 2025-01-01\ntags:\n  - go\n---\nMissing required.\n",
		"multi_issue.md": "---\ncreated: \"2025-01-01\"\nstatus: wip\ntags:\n  - go\n  - go\n---\nMultiple fixes.\n",
	}

	if err := os.MkdirAll(filepath.Join(root, "subfolder"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	files["subfolder/scoped.md"] = "---\ncreated: 2025-01-01\nstatus: wip\ntags:\n  - go\n---\nScoped note.\n"

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
	return root
}

func TestRepair_DryRun(t *testing.T) {
	buildRepairFixture(t)

	out, err := runCLI(t, "", "repair", "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}

	if !strings.Contains(out, "--- bad_case.md") {
		t.Errorf("expected bad_case.md in dry-run output, got:\n%s", out)
	}
	if !strings.Contains(out, "- status: wip") {
		t.Errorf("expected removed wip line, got:\n%s", out)
	}
	if !strings.Contains(out, "+ status: WIP") {
		t.Errorf("expected added WIP line, got:\n%s", out)
	}
	if !strings.Contains(out, "--- dup_tags.md") {
		t.Errorf("expected dup_tags.md in dry-run output, got:\n%s", out)
	}
	if strings.Contains(out, "--- clean.md") {
		t.Errorf("clean.md should not appear in dry-run output, got:\n%s", out)
	}
}

func TestRepair_Apply(t *testing.T) {
	root := buildRepairFixture(t)

	out, err := runCLI(t, "", "repair")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}

	if !strings.Contains(out, "repaired:") {
		t.Errorf("expected 'repaired:' lines, got:\n%s", out)
	}
	if !strings.Contains(out, "repaired") && !strings.Contains(out, "file(s)") {
		t.Errorf("expected summary line, got:\n%s", out)
	}

	got := readNote(t, filepath.Join(root, "bad_case.md"))
	if !strings.Contains(got, "status: WIP") {
		t.Errorf("bad_case.md should have status: WIP after repair, got:\n%s", got)
	}

	got = readNote(t, filepath.Join(root, "dup_tags.md"))
	if strings.Count(got, "- go") != 1 {
		t.Errorf("dup_tags.md should have single 'go' tag after repair, got:\n%s", got)
	}

	got = readNote(t, filepath.Join(root, "clean.md"))
	if !strings.Contains(got, "status: WIP") {
		t.Errorf("clean.md should be untouched, got:\n%s", got)
	}
}

func TestRepair_PropertyFlag(t *testing.T) {
	root := buildRepairFixture(t)

	out, err := runCLI(t, "", "repair", "--property", "status")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}

	got := readNote(t, filepath.Join(root, "bad_case.md"))
	if !strings.Contains(got, "status: WIP") {
		t.Errorf("bad_case.md status should be fixed, got:\n%s", got)
	}

	got = readNote(t, filepath.Join(root, "dup_tags.md"))
	if strings.Count(got, "- go") != 2 {
		t.Errorf("dup_tags.md tags should NOT be deduped when --property=status, got:\n%s", got)
	}
}

func TestRepair_PropertyFlagUnknown(t *testing.T) {
	buildRepairFixture(t)

	_, err := runCLI(t, "", "repair", "--property", "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown property")
	}
	if !strings.Contains(err.Error(), "not in schema") {
		t.Errorf("expected 'not in schema' error, got: %v", err)
	}
}

func TestRepair_FolderScoping(t *testing.T) {
	root := buildRepairFixture(t)

	out, err := runCLI(t, "", "repair", "--folder", "subfolder")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}

	got := readNote(t, filepath.Join(root, "subfolder", "scoped.md"))
	if !strings.Contains(got, "status: WIP") {
		t.Errorf("subfolder/scoped.md should be repaired, got:\n%s", got)
	}

	got = readNote(t, filepath.Join(root, "bad_case.md"))
	if strings.Contains(got, "status: WIP") {
		t.Errorf("bad_case.md should NOT be repaired under --folder subfolder, got:\n%s", got)
	}
}

func TestRepair_SkipOutput(t *testing.T) {
	buildRepairFixture(t)

	out, err := runCLI(t, "", "repair")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}

	if !strings.Contains(out, "SKIP:") {
		t.Errorf("expected SKIP lines for unfixable issues, got:\n%s", out)
	}
	if !strings.Contains(out, "unfixable.md") {
		t.Errorf("expected unfixable.md in skip output, got:\n%s", out)
	}
	if !strings.Contains(out, "skipped") {
		t.Errorf("expected skip count in summary, got:\n%s", out)
	}
	if !strings.Contains(out, "bn set <note> <property>=<value>") {
		t.Errorf("expected bn set hint for missing required, got:\n%s", out)
	}
}

func TestRepair_NoSchema(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, "note.md"),
		[]byte("---\nstatus: WIP\n---\nBody.\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")
	cfg := `vault = "` + root + `"` + "\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BNOTES_CONFIG", cfgPath)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_VAULT", "")

	out, err := runCLI(t, "", "repair")
	if err != nil {
		t.Fatalf("expected exit 0 with no schema, got error: %v", err)
	}
	if !strings.Contains(out, "no schema defined") {
		t.Errorf("expected 'no schema defined' message, got %q", out)
	}
	if !strings.Contains(out, "bn config edit") {
		t.Errorf("expected 'bn config edit' hint, got %q", out)
	}
	if !strings.Contains(out, "[frontmatter.schema") {
		t.Errorf("expected schema example, got %q", out)
	}
}

func TestRepair_PositionalNote(t *testing.T) {
	root := buildRepairFixture(t)

	notePath := filepath.Join(root, "bad_case.md")
	out, err := runCLI(t, "", "repair", notePath)
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}

	got := readNote(t, notePath)
	if !strings.Contains(got, "status: WIP") {
		t.Errorf("bad_case.md should be repaired, got:\n%s", got)
	}

	got = readNote(t, filepath.Join(root, "dup_tags.md"))
	if strings.Count(got, "- go") != 2 {
		t.Errorf("dup_tags.md should NOT be repaired when only bad_case.md is specified, got:\n%s", got)
	}

	if !strings.Contains(out, "repaired 1 file(s)") {
		t.Errorf("expected 'repaired 1 file(s)', got:\n%s", out)
	}
}

func TestRepair_PositionalAndFlagsMutualExclusion(t *testing.T) {
	root := buildRepairFixture(t)

	notePath := filepath.Join(root, "bad_case.md")
	_, err := runCLI(t, "", "repair", notePath, "--folder", "subfolder")
	if err == nil {
		t.Fatal("expected error when combining positional notes with find flags")
	}
	if !strings.Contains(err.Error(), "cannot combine") {
		t.Errorf("expected 'cannot combine' error, got: %v", err)
	}
}

func TestRepair_MultipleIssuesPerNote(t *testing.T) {
	root := buildRepairFixture(t)

	out, err := runCLI(t, "", "repair", "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}

	if !strings.Contains(out, "--- multi_issue.md") {
		t.Errorf("expected multi_issue.md in dry-run output, got:\n%s", out)
	}

	_, err = runCLI(t, "", "repair")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := readNote(t, filepath.Join(root, "multi_issue.md"))
	if !strings.Contains(got, "status: WIP") {
		t.Errorf("status should be canonicalized, got:\n%s", got)
	}
	if strings.Count(got, "- go") != 1 {
		t.Errorf("tags should be deduped, got:\n%s", got)
	}
}

func TestRepair_DryRunGolden_Canonicalize(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, "bad_case.md"),
		[]byte("---\nstatus: wip\n---\nBody.\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")
	cfg := `vault = "` + root + `"

[frontmatter.schema.status]
type    = "string"
allowed = ["WIP", "Done"]
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BNOTES_CONFIG", cfgPath)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_VAULT", "")

	notePath := filepath.Join(root, "bad_case.md")
	var out string
	assertMtimeUnchanged(t, []string{notePath}, func() {
		o, err := runCLI(t, "", "repair", "--dry-run")
		if err != nil {
			t.Fatalf("unexpected error: %v\n%s", err, o)
		}
		out = o
	})

	assertGolden(t, "repair_dryrun_canonicalize.golden", out)
}

func TestRepair_DryRunGolden_ListDedup(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, "dup_tags.md"),
		[]byte("---\ntags:\n  - go\n  - go\n---\nBody.\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")
	cfg := `vault = "` + root + `"

[frontmatter.schema.tags]
type = "list"
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BNOTES_CONFIG", cfgPath)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_VAULT", "")

	notePath := filepath.Join(root, "dup_tags.md")
	var out string
	assertMtimeUnchanged(t, []string{notePath}, func() {
		o, err := runCLI(t, "", "repair", "--dry-run")
		if err != nil {
			t.Fatalf("unexpected error: %v\n%s", err, o)
		}
		out = o
	})

	assertGolden(t, "repair_dryrun_dedup.golden", out)
}

func TestRepair_DryRunNoWrite(t *testing.T) {
	root := buildRepairFixture(t)
	paths := []string{
		filepath.Join(root, "bad_case.md"),
		filepath.Join(root, "dup_tags.md"),
		filepath.Join(root, "wrong_type.md"),
		filepath.Join(root, "multi_issue.md"),
	}

	assertMtimeUnchanged(t, paths, func() {
		_, err := runCLI(t, "", "repair", "--dry-run")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestRepair_CleanVaultNoChanges(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, "ok.md"),
		[]byte("---\ncreated: 2025-01-01\nstatus: WIP\ntags:\n  - go\n---\nBody.\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}
	t.Setenv("BNOTES_CONFIG", cfgPath)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_VAULT", "")

	out, err := runCLI(t, "", "repair")
	if err != nil {
		t.Fatalf("expected exit 0 for clean vault, got error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "repaired 0 file(s)") {
		t.Errorf("expected 'repaired 0 file(s)', got:\n%s", out)
	}
}
