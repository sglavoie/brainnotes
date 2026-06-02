package noteops

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCleanRelPathRejectsEscapes(t *testing.T) {
	t.Parallel()

	for _, rel := range []string{"../A.md", "/A.md", `..\A.md`} {
		if _, err := CleanRelPath(rel); err == nil {
			t.Fatalf("CleanRelPath(%q) succeeded, want error", rel)
		}
	}

	got, err := CleanRelPath("./Folder//A.md")
	if err != nil {
		t.Fatalf("CleanRelPath valid: %v", err)
	}
	if got != "Folder/A.md" {
		t.Fatalf("CleanRelPath valid = %q, want Folder/A.md", got)
	}
}

func TestResolveExistingPathRejectsSymlinkOutsideVault(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	outsideNote := filepath.Join(outside, "Outside.md")
	if err := os.WriteFile(outsideNote, []byte("outside\n"), 0o644); err != nil {
		t.Fatalf("write outside note: %v", err)
	}
	link := filepath.Join(root, "Linked.md")
	if err := os.Symlink(outsideNote, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	_, err := ResolveExistingPath(root, link)
	if !errors.Is(err, ErrOutsideVault) {
		t.Fatalf("ResolveExistingPath err = %v, want ErrOutsideVault", err)
	}
	if CodeOf(err) != CodeForbidden {
		t.Fatalf("CodeOf(err) = %q, want %q", CodeOf(err), CodeForbidden)
	}
}

func TestResolveWritablePathRejectsSymlinkParentOutsideVault(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	linkDir := filepath.Join(root, "Linked")
	if err := os.Symlink(outside, linkDir); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	_, err := ResolveWritablePath(root, filepath.Join(linkDir, "New.md"))
	if !errors.Is(err, ErrOutsideVault) {
		t.Fatalf("ResolveWritablePath err = %v, want ErrOutsideVault", err)
	}
}

func TestPathForRelReturnsContainedPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	got, err := PathForRel(root, "Folder/A.md")
	if err != nil {
		t.Fatalf("PathForRel: %v", err)
	}
	if got.RelPath != "Folder/A.md" {
		t.Fatalf("RelPath = %q, want Folder/A.md", got.RelPath)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks(root): %v", err)
	}
	if got.AbsPath != filepath.Join(resolvedRoot, "Folder", "A.md") {
		t.Fatalf("AbsPath = %q, want path under root", got.AbsPath)
	}
}
