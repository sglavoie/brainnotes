package vault

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNoteAccessors(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"References/book.md": "---\ntitle: Book\n---\nbook body\n",
	})
	n, err := v.ResolveByTitle("book", "References")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if n.Title() != "book" {
		t.Fatalf("title: got %q want %q", n.Title(), "book")
	}
	folder, err := n.Folder(v.Root)
	if err != nil {
		t.Fatalf("folder: %v", err)
	}
	if folder != "References" {
		t.Fatalf("folder: got %q want %q", folder, "References")
	}

	body, err := n.Body()
	if err != nil {
		t.Fatalf("body: %v", err)
	}
	if !strings.Contains(body, "book body") {
		t.Fatalf("body content unexpected: %q", body)
	}

	front, err := n.Front()
	if err != nil {
		t.Fatalf("front: %v", err)
	}
	if v, _ := front.GetString("title"); v != "Book" {
		t.Fatalf("title fm: got %q want %q", v, "Book")
	}
}

func TestFolderForRootNote(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"root.md": "# root\n",
	})
	n, err := v.ResolveByTitle("root", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	folder, err := n.Folder(v.Root)
	if err != nil {
		t.Fatalf("folder: %v", err)
	}
	if folder != "" {
		t.Fatalf("root folder should be empty, got %q", folder)
	}
}

func TestNoteFolderRejectsEmptyPath(t *testing.T) {
	n := &Note{Path: ""}
	if _, err := n.Folder(t.TempDir()); err == nil {
		t.Fatal("Folder with empty note path should return an error")
	}
}

func TestNoteFolderRejectsRelativePath(t *testing.T) {
	n := &Note{Path: filepath.Join("relative", "note.md")}
	_, err := n.Folder(t.TempDir())
	if err == nil {
		t.Fatal("Folder with relative note path should return an error")
	}
	if !strings.Contains(err.Error(), "must be absolute") {
		t.Errorf("error should mention absolute path requirement: %v", err)
	}
}

func TestNoteFolderRejectsOutOfVaultPath(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	n := &Note{Path: filepath.Join(outside, "note.md")}
	_, err := n.Folder(root)
	if err == nil {
		t.Fatal("Folder for out-of-vault note should return an error")
	}
	if !strings.Contains(err.Error(), "outside root") {
		t.Errorf("error should mention outside root: %v", err)
	}
}

func TestNilNoteMethods(t *testing.T) {
	var n *Note

	t.Run("Title returns empty", func(t *testing.T) {
		if got := n.Title(); got != "" {
			t.Errorf("nil Title() = %q, want empty", got)
		}
	})

	t.Run("Folder returns error", func(t *testing.T) {
		_, err := n.Folder("/some/root")
		if err == nil {
			t.Error("nil Folder() should return an error")
		}
	})

	t.Run("Load returns error", func(t *testing.T) {
		if err := n.Load(); err == nil {
			t.Error("nil Load() should return an error")
		}
	})

	t.Run("Loaded returns false", func(t *testing.T) {
		if n.Loaded() {
			t.Error("nil Loaded() should return false")
		}
	})

	t.Run("Save returns error", func(t *testing.T) {
		if err := n.Save(nil); err == nil {
			t.Error("nil Save() should return an error")
		}
	})
}

// TestTitleEmptyPath verifies that Title returns "" when Path is empty, rather
// than the "." that filepath.Base("") produces. An empty-path note should
// behave like a nil note for callers that branch on an empty title.
func TestTitleEmptyPath(t *testing.T) {
	n := &Note{Path: ""}
	if got := n.Title(); got != "" {
		t.Errorf("Title() on empty path = %q, want empty", got)
	}
}

// TestNilNoteAccessors verifies that Front, Body, and SetBody on a nil *Note
// return errors rather than panicking.
func TestNilNoteAccessors(t *testing.T) {
	var n *Note

	if _, err := n.Front(); err == nil {
		t.Error("nil Front() should return an error")
	}
	if _, err := n.Body(); err == nil {
		t.Error("nil Body() should return an error")
	}
	if err := n.SetBody("x"); err == nil {
		t.Error("nil SetBody() should return an error")
	}
}

// TestTitleMultiDotFilename verifies that Title strips only the final
// extension, preserving interior dots in the stem. A file named
// "archive.tar.md" must have title "archive.tar", not "archive".
func TestTitleMultiDotFilename(t *testing.T) {
	cases := map[string]string{
		"archive.tar.md": "archive.tar",
		"v1.2.3.md":      "v1.2.3",
		"plain.md":       "plain",
		// filepath.Ext(".md") returns ".md" (no non-dot before the extension),
		// so TrimSuffix yields "". The file ".md" is also a hidden file that
		// Walk skips entirely, so this path is mostly for completeness.
		".md": "",
	}
	for path, wantTitle := range cases {
		n := &Note{Path: filepath.Join("/vault", path)}
		if got := n.Title(); got != wantTitle {
			t.Errorf("Title(%q) = %q, want %q", path, got, wantTitle)
		}
	}
}

// TestNoteFolderRejectsEscape verifies that Folder returns an error when the
// note's path is outside the supplied root. filepath.Rel happily emits a
// `../...` traversal in that case, and without this guard the result would
// be treated as a vault-relative folder by callers — silently leaking an
// escape path past the in-vault containment contract.
func TestNoteFolderRejectsEscape(t *testing.T) {
	dir := t.TempDir()
	// A path that is a sibling of root, not inside it.
	outside := filepath.Join(dir, "elsewhere", "note.md")
	root := filepath.Join(dir, "vault")
	n := &Note{Path: outside}
	_, err := n.Folder(root)
	if err == nil {
		t.Fatal("Folder should reject path outside root")
	}
	if !strings.Contains(err.Error(), "outside root") {
		t.Errorf("error should mention 'outside root': %v", err)
	}
}

// TestNoteFolderInsideRootStillWorks ensures the new containment check did not
// regress the normal case: a note whose path is a child of root must still
// return its slash-separated vault-relative folder.
func TestNoteFolderInsideRootStillWorks(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"Sub/Inner/x.md": "# x\n",
	})
	n, err := v.ResolveByTitle("x", "Sub/Inner")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	folder, err := n.Folder(v.Root)
	if err != nil {
		t.Fatalf("Folder: %v", err)
	}
	if folder != "Sub/Inner" {
		t.Errorf("got %q, want %q", folder, "Sub/Inner")
	}
}
