package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenameBodyWikilinks(t *testing.T) {
	root := buildRenameFixture(t)

	out, err := runCLI(t, "", "rename", "Dune", "Sand Worms")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}

	if _, err := os.Stat(filepath.Join(root, "Dune.md")); !os.IsNotExist(err) {
		t.Fatalf("expected old path gone, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Sand Worms.md")); err != nil {
		t.Fatalf("expected new path present, stat err=%v", err)
	}

	got := readNote(t, filepath.Join(root, "Reviews", "Dune Review.md"))
	wantSubstrings := []string{
		"Read [[Sand Worms]] last week.",
		"Also see [[Sand Worms|the book]].",
		"Jump to [[Sand Worms#Sequels]].",
		"Full path: [[Books/Sand Worms]].",
		"[[Something Else]]",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "[[Dune") {
		t.Errorf("expected no remaining [[Dune... refs:\n%s", got)
	}
}

func TestRenameFrontmatterWikilinks(t *testing.T) {
	root := buildRenameFixture(t)

	if _, err := runCLI(t, "", "rename", "Dune", "Sand Worms"); err != nil {
		t.Fatalf("execute: %v", err)
	}

	categorized := readNote(t, filepath.Join(root, "categorized.md"))
	if !strings.Contains(categorized, "[[Sand Worms]]") {
		t.Errorf("expected categories list rewritten to [[Sand Worms]] in:\n%s", categorized)
	}
	if !strings.Contains(categorized, "[[Books]]") {
		t.Errorf("expected unrelated [[Books]] preserved in:\n%s", categorized)
	}
	if strings.Contains(categorized, "[[Dune]]") {
		t.Errorf("expected no [[Dune]] left in:\n%s", categorized)
	}

	parent := readNote(t, filepath.Join(root, "parent.md"))
	if !strings.Contains(parent, "[[Sand Worms]]") {
		t.Errorf("expected parent rewritten in:\n%s", parent)
	}
}

func TestRenameSelfReferences(t *testing.T) {
	root := buildRenameFixture(t)

	if _, err := runCLI(t, "", "rename", "Dune", "Sand Worms"); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := readNote(t, filepath.Join(root, "Sand Worms.md"))
	if !strings.Contains(got, "Self reference: [[Sand Worms]] and [[Sand Worms|alias]].") {
		t.Errorf("self-reference not rewritten in:\n%s", got)
	}
}

func TestRenameCaseOnly(t *testing.T) {
	root := buildRenameFixture(t)

	out, err := runCLI(t, "", "rename", "Dune", "dune")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}

	got := readNote(t, filepath.Join(root, "dune.md"))
	if !strings.Contains(got, "[[dune]]") {
		t.Errorf("expected lowercase self-reference, got:\n%s", got)
	}
}

func TestRenameByExplicitPath(t *testing.T) {
	root := buildRenameFixture(t)

	target := filepath.Join(root, "Dune.md")
	if _, err := runCLI(t, "", "rename", target, "Sand Worms"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Sand Worms.md")); err != nil {
		t.Fatalf("expected renamed file, got %v", err)
	}
}

func TestRenameByRelativePathOutsideVaultCWD(t *testing.T) {
	root := buildRenameFixture(t)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	outside := t.TempDir()
	if err := os.Chdir(outside); err != nil {
		t.Fatalf("chdir(%q): %v", outside, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	if _, err := runCLI(t, "", "rename", "Dune.md", "Sand Worms"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Sand Worms.md")); err != nil {
		t.Fatalf("expected renamed file, got %v", err)
	}
}
