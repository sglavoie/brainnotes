package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMoveFolderPrefixedBodyWikilinks(t *testing.T) {
	root := buildMoveFixture(t)

	src := filepath.Join(root, "Books", "Dune.md")
	out, err := runCLI(t, "", "move", src, "References/Books")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}

	if _, err := os.Stat(filepath.Join(root, "Books", "Dune.md")); !os.IsNotExist(err) {
		t.Fatalf("expected old path gone, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "References", "Books", "Dune.md")); err != nil {
		t.Fatalf("expected new path present, stat err=%v", err)
	}

	got := readNote(t, filepath.Join(root, "Reviews", "Dune Review.md"))
	want := []string{
		"Read [[References/Books/Dune]] last week.",
		"Also see [[References/Books/Dune|the book]].",
		"Jump to [[References/Books/Dune#Sequels]].",
		"Bare: [[Dune]].", // untouched
		"[[Books/Other]]", // unrelated, untouched
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("expected %q in:\n%s", w, got)
		}
	}
	if strings.Contains(got, "[[Books/Dune") {
		t.Errorf("expected no remaining [[Books/Dune... refs in:\n%s", got)
	}
}

func TestMoveFolderPrefixedFrontmatter(t *testing.T) {
	root := buildMoveFixture(t)

	src := filepath.Join(root, "Books", "Dune.md")
	if _, err := runCLI(t, "", "move", src, "References/Books"); err != nil {
		t.Fatalf("execute: %v", err)
	}

	categorized := readNote(t, filepath.Join(root, "categorized.md"))
	if !strings.Contains(categorized, "[[References/Books/Dune]]") {
		t.Errorf("expected categories rewritten, got:\n%s", categorized)
	}
	if !strings.Contains(categorized, "[[Books/Other]]") {
		t.Errorf("expected unrelated [[Books/Other]] preserved, got:\n%s", categorized)
	}
	if strings.Contains(categorized, "[[Books/Dune]]") {
		t.Errorf("expected no [[Books/Dune]] left in:\n%s", categorized)
	}

	parent := readNote(t, filepath.Join(root, "parent.md"))
	if !strings.Contains(parent, "[[References/Books/Dune]]") {
		t.Errorf("expected parent rewritten, got:\n%s", parent)
	}
}

func TestMoveBareWikilinksUntouched(t *testing.T) {
	root := buildMoveFixture(t)

	src := filepath.Join(root, "Books", "Dune.md")
	if _, err := runCLI(t, "", "move", src, "References/Books"); err != nil {
		t.Fatalf("execute: %v", err)
	}

	review := readNote(t, filepath.Join(root, "Reviews", "Dune Review.md"))
	if !strings.Contains(review, "Bare: [[Dune]].") {
		t.Errorf("bare reference removed from review note:\n%s", review)
	}

	moved := readNote(t, filepath.Join(root, "References", "Books", "Dune.md"))
	if !strings.Contains(moved, "Bare self: [[Dune]].") {
		t.Errorf("bare self-reference removed from moved note:\n%s", moved)
	}
	if !strings.Contains(moved, "Folder self: [[References/Books/Dune]] and [[References/Books/Dune|alias]].") {
		t.Errorf("folder-prefixed self-references not rewritten in moved note:\n%s", moved)
	}
}

func TestMoveCreatesIntermediateFolders(t *testing.T) {
	root := buildMoveFixture(t)

	deep := "A/B/C"
	if _, err := os.Stat(filepath.Join(root, "A")); !os.IsNotExist(err) {
		t.Fatalf("precondition: A should not exist, got %v", err)
	}

	src := filepath.Join(root, "Books", "Dune.md")
	if _, err := runCLI(t, "", "move", src, deep); err != nil {
		t.Fatalf("execute: %v", err)
	}

	info, err := os.Stat(filepath.Join(root, "A", "B", "C"))
	if err != nil {
		t.Fatalf("intermediate folders not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected directory at A/B/C, got %v", info.Mode())
	}
	mode := info.Mode().Perm()
	if mode&0o755 != 0o755 {
		t.Errorf("expected mode 0755 bits set, got %o", mode)
	}

	if _, err := os.Stat(filepath.Join(root, "A", "B", "C", "Dune.md")); err != nil {
		t.Fatalf("expected moved note inside new folder, got %v", err)
	}
}
