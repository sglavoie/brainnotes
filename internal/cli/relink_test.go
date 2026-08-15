package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildRelinkFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"Projects/Roadmap.md": "---\ntitle: Roadmap\n---\nRoadmap body.\n",
		"Projects/Atlas.md":   "---\ntitle: Atlas\n---\nAtlas body.\n",
		"Areas/Atlas.md":      "---\ntitle: Atlas\n---\nArea Atlas body.\n",
		"Notes/Ref.md": "---\n" +
			"categories:\n" +
			"  - [[Projects\\Roadmap]]\n" +
			"  - [[Missing\\Atlas]]\n" +
			"---\n" +
			"See [[Projects\\Roadmap]] and [[Missing\\Atlas]].\n" +
			"```\n" +
			"[[Projects\\Roadmap]]\n" +
			"```\n",
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
	cfgPath := filepath.Join(cfgDir, "config.toml")
	cfg := "vault = \"" + root + "\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("BNOTES_CONFIG", cfgPath)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_VAULT", "")
	return root
}

func TestRelink_DryRun(t *testing.T) {
	root := buildRelinkFixture(t)
	ref := filepath.Join(root, "Notes", "Ref.md")
	original := readNote(t, ref)

	var out string
	assertMtimeUnchanged(t, []string{ref}, func() {
		o, err := runCLI(t, "", "relink", "--dry-run")
		if err != nil {
			t.Fatalf("execute: %v\n%s", err, o)
		}
		out = o
	})

	if !strings.Contains(out, "--- Notes/Ref.md") {
		t.Fatalf("expected Notes/Ref.md diff block, got:\n%s", out)
	}
	if !strings.Contains(out, "body:6: [[Projects\\Roadmap]] -> [[Projects/Roadmap]]") {
		t.Fatalf("expected canonical body change line, got:\n%s", out)
	}
	if !strings.Contains(out, "frontmatter[categories]: [[Projects\\Roadmap]] -> [[Projects/Roadmap]]") {
		t.Fatalf("expected canonical frontmatter change line, got:\n%s", out)
	}

	if got := readNote(t, ref); got != original {
		t.Fatalf("file changed during dry-run:\n--- before ---\n%s--- after ---\n%s", original, got)
	}
}

func TestRelink_Apply(t *testing.T) {
	root := buildRelinkFixture(t)
	ref := filepath.Join(root, "Notes", "Ref.md")

	out, err := runCLI(t, "", "relink")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "relinked: Notes/Ref.md") {
		t.Fatalf("expected relinked line, got:\n%s", out)
	}
	if !strings.Contains(out, "relinked 1 file(s)") {
		t.Fatalf("expected summary line, got:\n%s", out)
	}
	if !strings.Contains(out, "SKIP: Notes/Ref.md:6: [[Missing\\Atlas]]") {
		t.Fatalf("expected body skip line, got:\n%s", out)
	}
	if !strings.Contains(out, "SKIP: Notes/Ref.md:frontmatter[categories]: [[Missing\\Atlas]]") {
		t.Fatalf("expected frontmatter skip line, got:\n%s", out)
	}

	got := readNote(t, ref)
	if strings.Count(got, "[[Projects/Roadmap]]") != 2 {
		t.Fatalf("expected body and frontmatter rewrites to [[Projects/Roadmap]], got:\n%s", got)
	}
	if strings.Count(got, "[[Projects\\Roadmap]]") != 1 {
		t.Fatalf("expected code fence link untouched, got:\n%s", got)
	}
	if strings.Count(got, "[[Missing\\Atlas]]") != 2 {
		t.Fatalf("expected ambiguous links skipped, got:\n%s", got)
	}
}

func TestRelink_MutualExclusion(t *testing.T) {
	root := buildRelinkFixture(t)
	ref := filepath.Join(root, "Notes", "Ref.md")

	_, err := runCLI(t, "", "relink", ref, "--tag", "x")
	if err == nil {
		t.Fatal("expected error combining notes and filter flags")
	}
	if !strings.Contains(err.Error(), "cannot combine") {
		t.Fatalf("unexpected error: %v", err)
	}
}
