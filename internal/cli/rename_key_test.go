package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildRenameKeyFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"alpha.md":    "---\nkind: article\nstatus: WIP\ntags:\n  - go\n---\nAlpha body.\n",
		"beta.md":     "---\nkind: reference\nstatus: Done\ntags:\n  - rust\n---\nBeta body.\n",
		"conflict.md": "---\nkind: article\nstatus: WIP\ntype: note\n---\nConflict body.\n",
		"no_kind.md":  "---\nstatus: Done\n---\nNo kind.\n",
	}

	if err := os.MkdirAll(filepath.Join(root, "Projects"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	files["Projects/gamma.md"] = "---\nkind: project\nstatus: WIP\n---\nGamma body.\n"

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

[frontmatter]
list_properties = ["tags", "kind"]
wikilink_properties = ["categories"]

[frontmatter.schema.kind]
type = "string"
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

func TestRenameKey_BasicSingleFile(t *testing.T) {
	root := buildRenameKeyFixture(t)
	note := filepath.Join(root, "alpha.md")

	out, err := runCLI(t, "", "rename-key", "kind", "category", note)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "renamed kind -> category in 1 file(s)") {
		t.Fatalf("unexpected summary: %q", out)
	}

	got := readNote(t, note)
	if strings.Contains(got, "kind:") {
		t.Fatalf("old key should be gone:\n%s", got)
	}
	if !strings.Contains(got, "category: article") {
		t.Fatalf("new key should have old value:\n%s", got)
	}
}

func TestRenameKey_VaultWide(t *testing.T) {
	root := buildRenameKeyFixture(t)

	out, err := runCLI(t, "", "rename-key", "kind", "category")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}

	if !strings.Contains(out, "in 4 file(s)") {
		t.Fatalf("expected 4 files renamed, got: %q", out)
	}

	for _, name := range []string{"alpha.md", "beta.md", "conflict.md", "Projects/gamma.md"} {
		got := readNote(t, filepath.Join(root, name))
		if strings.Contains(got, "kind:") {
			t.Errorf("%s still has old key:\n%s", name, got)
		}
		if !strings.Contains(got, "category:") {
			t.Errorf("%s missing new key:\n%s", name, got)
		}
	}

	noKind := readNote(t, filepath.Join(root, "no_kind.md"))
	if strings.Contains(noKind, "category:") {
		t.Fatalf("no_kind.md should be untouched:\n%s", noKind)
	}
}

func TestRenameKey_ConflictSkipping(t *testing.T) {
	root := buildRenameKeyFixture(t)
	note := filepath.Join(root, "conflict.md")

	out, err := runCLI(t, "", "rename-key", "kind", "type", note)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "0 file(s)") {
		t.Fatalf("expected 0 files renamed, got: %q", out)
	}
	if !strings.Contains(out, "1 skipped (conflict)") {
		t.Fatalf("expected conflict skip, got: %q", out)
	}
	if !strings.Contains(out, "WARN") {
		t.Fatalf("expected WARN for conflict, got: %q", out)
	}

	got := readNote(t, note)
	if !strings.Contains(got, "kind: article") {
		t.Fatalf("old key should be preserved on conflict:\n%s", got)
	}
	if !strings.Contains(got, "type: note") {
		t.Fatalf("existing new key should be preserved on conflict:\n%s", got)
	}
}

func TestRenameKey_FolderScoping(t *testing.T) {
	root := buildRenameKeyFixture(t)

	out, err := runCLI(t, "", "rename-key", "kind", "category", "--folder", "Projects")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "in 1 file(s)") {
		t.Fatalf("expected 1 file renamed, got: %q", out)
	}

	gamma := readNote(t, filepath.Join(root, "Projects/gamma.md"))
	if !strings.Contains(gamma, "category: project") {
		t.Fatalf("gamma should have new key:\n%s", gamma)
	}

	alpha := readNote(t, filepath.Join(root, "alpha.md"))
	if !strings.Contains(alpha, "kind: article") {
		t.Fatalf("alpha should be untouched:\n%s", alpha)
	}
}

func TestRenameKey_DryRun(t *testing.T) {
	root := buildRenameKeyFixture(t)
	note := filepath.Join(root, "alpha.md")
	original := readNote(t, note)

	out, err := runCLI(t, "", "rename-key", "kind", "category", note, "--dry-run")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "- kind: article") {
		t.Fatalf("expected removed line in dry-run, got:\n%s", out)
	}
	if !strings.Contains(out, "+ category: article") {
		t.Fatalf("expected added line in dry-run, got:\n%s", out)
	}

	got := readNote(t, note)
	if got != original {
		t.Fatalf("file should be unchanged during dry-run:\n--- before ---\n%s--- after ---\n%s", original, got)
	}
}

func TestRenameKey_InvalidKeyName(t *testing.T) {
	buildRenameKeyFixture(t)

	_, err := runCLI(t, "", "rename-key", "bad key", "good")
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
	if !strings.Contains(err.Error(), "invalid key") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRenameKey_SameOldNew(t *testing.T) {
	buildRenameKeyFixture(t)

	_, err := runCLI(t, "", "rename-key", "kind", "kind")
	if err == nil {
		t.Fatal("expected error for same old/new key")
	}
	if !strings.Contains(err.Error(), "same") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRenameKey_ConfigWarnings(t *testing.T) {
	root := buildRenameKeyFixture(t)
	note := filepath.Join(root, "alpha.md")

	out, err := runCLI(t, "", "rename-key", "kind", "category", note)
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "list_properties") {
		t.Fatalf("expected list_properties warning, got:\n%s", out)
	}
	if !strings.Contains(out, "[frontmatter.schema]") {
		t.Fatalf("expected schema warning, got:\n%s", out)
	}
}

func TestRenameKey_MutualExclusion(t *testing.T) {
	root := buildRenameKeyFixture(t)
	note := filepath.Join(root, "alpha.md")

	_, err := runCLI(t, "", "rename-key", "kind", "category", note, "--tag", "go")
	if err == nil {
		t.Fatal("expected error combining notes and filter flags")
	}
	if !strings.Contains(err.Error(), "cannot combine") {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = root
}
