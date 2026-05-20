package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchema_ShowsProperties(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("---\ntitle: x\n---\n"), 0o644); err != nil {
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
required = false
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BNOTES_CONFIG", cfgPath)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_VAULT", "")

	out, err := runCLI(t, "", "schema")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}

	if !strings.Contains(out, "PROPERTY") {
		t.Errorf("expected header row, got:\n%s", out)
	}
	if !strings.Contains(out, "status") {
		t.Errorf("expected status property, got:\n%s", out)
	}
	if !strings.Contains(out, "WIP, Done, Archived") {
		t.Errorf("expected allowed values for status, got:\n%s", out)
	}
	if !strings.Contains(out, "tags") {
		t.Errorf("expected tags property, got:\n%s", out)
	}
}

func TestSchema_NoSchema(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("---\ntitle: x\n---\n"), 0o644); err != nil {
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

	out, err := runCLI(t, "", "schema")
	if err != nil {
		t.Fatalf("expected exit 0, got error: %v", err)
	}
	if !strings.Contains(out, "no schema defined") {
		t.Errorf("expected 'no schema defined' message, got %q", out)
	}
	if !strings.Contains(out, "bn config edit") {
		t.Errorf("expected 'bn config edit' hint, got %q", out)
	}
}
