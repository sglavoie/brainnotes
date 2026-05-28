package cli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func buildSearchIntegrationFixture(t *testing.T) (vaultRoot string) {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"Notes/Physics.md":   "---\ntitle: Physics\ntags:\n  - science\n---\nactivation energy is key\nthermodynamics relates here\n",
		"Notes/Chemistry.md": "---\ntitle: Chemistry\ntags:\n  - science\n---\nactivation energy barrier\ncatalyst lowers the barrier\n",
		"Notes/History.md":   "---\ntitle: History\ntags:\n  - humanities\n---\nno matching content here\n",
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

func pathsToRels(t *testing.T, root, stdout string) []string {
	t.Helper()
	var rels []string
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		rel, err := filepath.Rel(root, line)
		if err != nil {
			t.Fatalf("rel %q: %v", line, err)
		}
		rels = append(rels, filepath.ToSlash(rel))
	}
	sort.Strings(rels)
	return rels
}

func TestIntegration_SearchBasic(t *testing.T) {
	root := buildSearchIntegrationFixture(t)

	stdout, err := runCLI(t, "", "search", "activation energy")
	if err != nil {
		t.Fatalf("search: %v\n%s", err, stdout)
	}
	got := pathsToRels(t, root, stdout)
	want := []string{"Notes/Chemistry.md", "Notes/Physics.md"}
	if !equalStrings(got, want) {
		t.Fatalf("basic search:\n got=%v\nwant=%v", got, want)
	}
}

func TestIntegration_SearchWithFindFlags(t *testing.T) {
	root := buildSearchIntegrationFixture(t)

	stdout, err := runCLI(t, "", "search", "--tag", "science", "barrier")
	if err != nil {
		t.Fatalf("search with find flags: %v\n%s", err, stdout)
	}
	got := pathsToRels(t, root, stdout)
	want := []string{"Notes/Chemistry.md"}
	if !equalStrings(got, want) {
		t.Fatalf("search --tag science barrier:\n got=%v\nwant=%v", got, want)
	}
}

func TestIntegration_SearchContext(t *testing.T) {
	buildSearchIntegrationFixture(t)

	stdout, err := runCLI(t, "", "search", "--context", "1", "barrier")
	if err != nil {
		t.Fatalf("search --context: %v\n%s", err, stdout)
	}
	if stdout == "" {
		t.Fatal("expected context output, got empty")
	}
	// Context output should contain a match line (with ':') and a context line (with '-').
	relPath := "Notes/Chemistry.md"
	if !strings.Contains(stdout, relPath+":") {
		t.Fatalf("expected match line with %q, got:\n%s", relPath, stdout)
	}
	// Should contain "barrier" in a match line.
	if !strings.Contains(stdout, "barrier") {
		t.Fatalf("expected 'barrier' in context output, got:\n%s", stdout)
	}
}

func TestIntegration_SearchCount(t *testing.T) {
	buildSearchIntegrationFixture(t)

	stdout, err := runCLI(t, "", "search", "--count", "activation energy")
	if err != nil {
		t.Fatalf("search --count: %v\n%s", err, stdout)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 count lines, got %d:\n%s", len(lines), stdout)
	}
	// Each line should be "<count>\t<relpath>".
	for _, l := range lines {
		parts := strings.SplitN(l, "\t", 2)
		if len(parts) != 2 {
			t.Fatalf("count line missing tab separator: %q", l)
		}
	}
}

func TestIntegration_SearchCount_SortOrder(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"A.md": "---\ntitle: A\n---\nword first line\nword second line\nword third line\n",
		"B.md": "---\ntitle: B\n---\nword only line\n",
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

	stdout, err := runCLI(t, "", "search", "--count", "word")
	if err != nil {
		t.Fatalf("search --count: %v\n%s", err, stdout)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), stdout)
	}
	// First line should have the higher count (A.md has 3, B.md has 1).
	if !strings.HasPrefix(lines[0], "3\t") {
		t.Fatalf("expected first line to be count=3 (A.md), got: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "1\t") {
		t.Fatalf("expected second line to be count=1 (B.md), got: %q", lines[1])
	}
}
