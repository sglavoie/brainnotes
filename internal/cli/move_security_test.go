package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMoveRejectsOutsideVault(t *testing.T) {
	root := buildMoveFixture(t)
	src := filepath.Join(root, "Books", "Dune.md")
	_, err := runCLI(t, "", "move", src, "../escape")
	if err == nil {
		t.Fatal("expected error for out-of-vault destination")
	}
	if !strings.Contains(err.Error(), "outside the vault") {
		t.Fatalf("expected outside-the-vault error, got %v", err)
	}
}

func TestMoveRejectsSymlinkDestinationOutsideVault(t *testing.T) {
	root := buildMoveFixture(t)
	outside := t.TempDir()
	symlinkDir := filepath.Join(root, "escaped")
	if err := os.Symlink(outside, symlinkDir); err != nil {
		t.Skip("cannot create symlinks:", err)
	}

	src := filepath.Join(root, "Books", "Dune.md")
	sourceBefore := readNote(t, src)
	reviewBefore := readNote(t, filepath.Join(root, "Reviews", "Dune Review.md"))
	_, err := runCLI(t, "", "move", src, "escaped/subdir")
	if err == nil {
		t.Fatal("expected error for symlink-escaped destination")
	}
	if !strings.Contains(err.Error(), "outside the vault") {
		t.Fatalf("expected outside-the-vault error, got %v", err)
	}
	if got := readNote(t, src); got != sourceBefore {
		t.Fatalf("source note changed after rejected move:\nbefore=%s\nafter=%s", sourceBefore, got)
	}
	if got := readNote(t, filepath.Join(root, "Reviews", "Dune Review.md")); got != reviewBefore {
		t.Fatalf("referencing note changed after rejected move:\nbefore=%s\nafter=%s", reviewBefore, got)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "subdir", "Dune.md")); statErr == nil {
		t.Fatalf("move leaked note outside vault at %s", filepath.Join(outside, "subdir", "Dune.md"))
	}
}

func TestMoveDryRunWritesNothing(t *testing.T) {
	root := buildMoveFixture(t)

	before := snapshotVault(t, root)

	src := filepath.Join(root, "Books", "Dune.md")
	out, err := runCLI(t, "", "move", src, "References/Books", "--dry-run")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}

	after := snapshotVault(t, root)
	if !mapsEqual(before, after) {
		t.Fatalf("dry-run modified files:\nbefore=%v\nafter=%v", before, after)
	}
	if !strings.Contains(out, "path: Books/Dune.md -> References/Books/Dune.md") {
		t.Errorf("dry-run output missing canonical path change line:\n%s", out)
	}
	if !strings.Contains(out, "--- Reviews/Dune Review.md") {
		t.Errorf("dry-run output missing reviewer file block:\n%s", out)
	}
	if !strings.Contains(out, "frontmatter[categories]") {
		t.Errorf("dry-run output missing frontmatter change:\n%s", out)
	}
}

func TestMoveDryRunOmitsBareReferences(t *testing.T) {
	root := buildMoveFixture(t)

	beforeMtimes := snapshotMtimes(t, root)

	src := filepath.Join(root, "Books", "Dune.md")
	out, err := runCLI(t, "", "move", src, "References/Books", "--dry-run")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}

	wantSubstrings := []string{
		"Read [[Books/Dune]] last week. -> Read [[References/Books/Dune]] last week.",
		"Also see [[Books/Dune|the book]]. -> Also see [[References/Books/Dune|the book]].",
		"Jump to [[Books/Dune#Sequels]]. -> Jump to [[References/Books/Dune#Sequels]].",
	}
	for _, w := range wantSubstrings {
		if !strings.Contains(out, w) {
			t.Errorf("expected %q in dry-run output:\n%s", w, out)
		}
	}

	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Bare:") || strings.Contains(line, "Bare self:") {
			t.Errorf("bare-only line leaked into move dry-run output: %q\nfull output:\n%s", line, out)
		}
	}

	afterMtimes := snapshotMtimes(t, root)
	if !mtimesEqual(beforeMtimes, afterMtimes) {
		t.Fatalf("dry-run modified file mtimes:\nbefore=%v\nafter=%v", beforeMtimes, afterMtimes)
	}
}

func TestMoveRejectsSameFolder(t *testing.T) {
	root := buildMoveFixture(t)

	src := filepath.Join(root, "Books", "Dune.md")
	_, err := runCLI(t, "", "move", src, "Books")
	if err == nil {
		t.Fatal("expected error when destination folder equals source folder")
	}
	if !strings.Contains(err.Error(), "identical to current path") && !strings.Contains(err.Error(), "same as source") {
		t.Fatalf("expected identical/same-as-source error, got %v", err)
	}
}

func TestMoveRejectsAbsoluteFolderPath(t *testing.T) {
	root := buildMoveFixture(t)
	src := filepath.Join(root, "Books", "Dune.md")
	before := snapshotVault(t, root)

	_, err := runCLI(t, "", "move", src, filepath.Join(root, "References"))
	if err == nil {
		t.Fatal("expected error for absolute destination folder")
	}
	if !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("expected absolute-path error, got %v", err)
	}
	if after := snapshotVault(t, root); !mapsEqual(before, after) {
		t.Fatalf("move with absolute folder rewrote files:\nbefore=%v\nafter=%v", before, after)
	}
}

func TestMoveRejectsWindowsAbsoluteFolderPath(t *testing.T) {
	root := buildMoveFixture(t)
	src := filepath.Join(root, "Books", "Dune.md")
	before := snapshotVault(t, root)

	_, err := runCLI(t, "", "move", src, `C:\References`)
	if err == nil {
		t.Fatal("expected error for Windows absolute destination folder")
	}
	if !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("expected absolute-path error, got %v", err)
	}
	if after := snapshotVault(t, root); !mapsEqual(before, after) {
		t.Fatalf("move with Windows absolute folder rewrote files:\nbefore=%v\nafter=%v", before, after)
	}
}

func TestMoveRejectsFoldersWithWikilinkReservedChars(t *testing.T) {
	cases := []string{"References#Books", "References|Books", "References[Books", "References]Books"}
	for _, folder := range cases {
		t.Run(folder, func(t *testing.T) {
			root := buildMoveFixture(t)
			src := filepath.Join(root, "Books", "Dune.md")
			before := snapshotVault(t, root)

			_, err := runCLI(t, "", "move", src, folder)
			if err == nil {
				t.Fatalf("expected error for reserved-char folder %q", folder)
			}
			if !strings.Contains(err.Error(), "wikilink") && !strings.Contains(err.Error(), "may not contain") {
				t.Fatalf("expected wikilink-safe folder error for %q, got %v", folder, err)
			}
			if after := snapshotVault(t, root); !mapsEqual(before, after) {
				t.Fatalf("move rewrote files for rejected folder %q:\nbefore=%v\nafter=%v", folder, before, after)
			}
		})
	}
}

func TestMoveAllowsExistingReservedCharTitleWhenFolderIsSafe(t *testing.T) {
	root := buildMoveFixture(t)
	src := filepath.Join(root, "Books", "Dune#1.md")
	if err := os.WriteFile(src, []byte("---\ntitle: Dune#1\n---\nOriginal body.\n"), 0o644); err != nil {
		t.Fatalf("write weird-title note: %v", err)
	}

	out, err := runCLI(t, "", "move", src, "References/Books")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("expected old path gone, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "References", "Books", "Dune#1.md")); err != nil {
		t.Fatalf("expected moved note with reserved-char title, stat err=%v", err)
	}
}
