package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildLintFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"clean.md":          "---\ncreated: 2025-01-01\nstatus: WIP\ntags:\n  - go\n---\nClean note.\n",
		"missing_status.md": "---\ncreated: 2025-01-01\ntags:\n  - go\n---\nMissing status.\n",
		"wrong_type.md":     "---\ncreated: 2025-01-01\nstatus: Done\ntags: not-a-list\n---\nWrong type.\n",
		"bad_value.md":      "---\ncreated: 2025-01-01\nstatus: wip\ntags:\n  - go\n---\nBad value.\n",
		"no_frontmatter.md": "Just a body, no frontmatter.\n",
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

func TestLint_CleanVault(t *testing.T) {
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

	out, err := runCLI(t, "", "lint")
	if err != nil {
		t.Fatalf("expected exit 0, got error: %v\n%s", err, out)
	}
	if out != "" {
		t.Fatalf("expected no output, got %q", out)
	}
}

func TestLint_MissingRequired(t *testing.T) {
	buildLintFixture(t)

	out, err := runCLI(t, "", "lint")
	if err == nil {
		t.Fatal("expected exit 1 (violations found), got nil")
	}

	if !strings.Contains(out, "status: missing in") {
		t.Errorf("expected status missing violation, got:\n%s", out)
	}
	if !strings.Contains(out, "missing_status.md") {
		t.Errorf("expected missing_status.md listed, got:\n%s", out)
	}
	if !strings.Contains(out, "no_frontmatter.md") {
		t.Errorf("expected no_frontmatter.md listed for missing required props, got:\n%s", out)
	}
}

func TestLint_WrongType(t *testing.T) {
	buildLintFixture(t)

	out, err := runCLI(t, "", "lint")
	if err == nil {
		t.Fatal("expected exit 1")
	}

	if !strings.Contains(out, "tags: wrong type in") {
		t.Errorf("expected tags wrong type violation, got:\n%s", out)
	}
	if !strings.Contains(out, "wrong_type.md") {
		t.Errorf("expected wrong_type.md listed, got:\n%s", out)
	}
}

func TestLint_DisallowedValue(t *testing.T) {
	buildLintFixture(t)

	out, err := runCLI(t, "", "lint")
	if err == nil {
		t.Fatal("expected exit 1")
	}

	if !strings.Contains(out, `invalid value "wip"`) {
		t.Errorf("expected invalid value violation for wip, got:\n%s", out)
	}
	if !strings.Contains(out, "bad_value.md") {
		t.Errorf("expected bad_value.md listed, got:\n%s", out)
	}
}

func TestLint_PropertyFlag(t *testing.T) {
	buildLintFixture(t)

	out, err := runCLI(t, "", "lint", "--property", "status")
	if err == nil {
		t.Fatal("expected exit 1")
	}

	if strings.Contains(out, "tags:") {
		t.Errorf("--property status should not show tags violations, got:\n%s", out)
	}
	if strings.Contains(out, "created:") {
		t.Errorf("--property status should not show created violations, got:\n%s", out)
	}
	if !strings.Contains(out, "status:") {
		t.Errorf("expected status violations, got:\n%s", out)
	}
}

func TestLint_PropertyFlagUnknown(t *testing.T) {
	buildLintFixture(t)

	_, err := runCLI(t, "", "lint", "--property", "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown property")
	}
	if !strings.Contains(err.Error(), "not in schema") {
		t.Errorf("expected 'not in schema' error, got: %v", err)
	}
}

func TestLint_FolderScoping(t *testing.T) {
	buildLintFixture(t)

	out, err := runCLI(t, "", "lint", "--folder", "subfolder", "--property", "status")
	if err == nil {
		t.Fatal("expected exit 1")
	}

	if !strings.Contains(out, "subfolder/scoped.md") {
		t.Errorf("expected subfolder/scoped.md in output, got:\n%s", out)
	}
	if strings.Contains(out, "bad_value.md") {
		t.Errorf("should not list root-level bad_value.md under --folder subfolder, got:\n%s", out)
	}
	if strings.Contains(out, "missing_status.md") {
		t.Errorf("should not list root-level missing_status.md under --folder subfolder, got:\n%s", out)
	}
}

func TestLint_NoSchema(t *testing.T) {
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

	out, err := runCLI(t, "", "lint")
	if err != nil {
		t.Fatalf("expected exit 0 with no schema, got error: %v", err)
	}
	if !strings.Contains(out, "no schema defined") {
		t.Errorf("expected 'no schema defined' message, got %q", out)
	}
}

func TestLint_MultipleViolationsPerNote(t *testing.T) {
	buildLintFixture(t)

	out, err := runCLI(t, "", "lint")
	if err == nil {
		t.Fatal("expected exit 1")
	}

	// no_frontmatter.md should appear in missing violations for all 3 required props
	count := strings.Count(out, "no_frontmatter.md")
	if count < 3 {
		t.Errorf("no_frontmatter.md should appear in at least 3 violation groups (one per required prop), got %d occurrences:\n%s", count, out)
	}
}
