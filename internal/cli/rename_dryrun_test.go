package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenameDryRunWritesNothing(t *testing.T) {
	root := buildRenameFixture(t)

	before := snapshotVault(t, root)

	out, err := runCLI(t, "", "rename", "Dune", "Sand Worms", "--dry-run")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}

	after := snapshotVault(t, root)
	if !mapsEqual(before, after) {
		t.Fatalf("dry-run modified files:\nbefore=%v\nafter=%v", before, after)
	}
	if !strings.Contains(out, "rename: Dune.md -> Sand Worms.md") {
		t.Errorf("dry-run output missing rename header:\n%s", out)
	}
	if !strings.Contains(out, "Reviews/Dune Review.md:") {
		t.Errorf("dry-run output missing reviewer file change:\n%s", out)
	}
	if !strings.Contains(out, "frontmatter[categories]") {
		t.Errorf("dry-run output missing frontmatter change:\n%s", out)
	}
}

// TestRenameDryRunMasksCodeBlockReferences asserts that wikilinks inside a
// fenced code block do not appear in the rename dry-run output, while bare,
// aliased, and heading references in prose do.
func TestRenameDryRunMasksCodeBlockReferences(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"Dune.md":      "---\ntitle: Dune\n---\nOriginal body.\n",
		"bare.md":      "---\ntitle: Bare\n---\nPlain prose link: [[Dune]] in a sentence.\n",
		"alias.md":     "---\ntitle: Alias\n---\nWith alias: [[Dune|the book]].\n",
		"heading.md":   "---\ntitle: Heading\n---\nAt heading: [[Dune#Sequels]].\n",
		"codeblock.md": "---\ntitle: Code\n---\nDocumentation example:\n\n```\nUse [[Dune]] like this.\n```\n\nDone.\n",
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
	if err := os.WriteFile(cfgPath, []byte("vault = \""+root+"\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("BNOTES_CONFIG", cfgPath)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_VAULT", "")

	beforeMtimes := snapshotMtimes(t, root)

	out, err := runCLI(t, "", "rename", "Dune", "Sand Worms", "--dry-run")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}

	for _, want := range []string{"bare.md:", "alias.md:", "heading.md:"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in dry-run output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "codeblock.md:") {
		t.Errorf("expected codeblock.md to be absent from dry-run output (link is fenced):\n%s", out)
	}

	afterMtimes := snapshotMtimes(t, root)
	if !mtimesEqual(beforeMtimes, afterMtimes) {
		t.Fatalf("dry-run modified file mtimes:\nbefore=%v\nafter=%v", beforeMtimes, afterMtimes)
	}
}
