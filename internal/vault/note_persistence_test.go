package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveAtomic(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"a.md": "---\ntitle: A\n---\nbody\n",
	})

	n, err := v.ResolveByTitle("a", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := n.SetBody("changed\n"); err != nil {
		t.Fatalf("setbody: %v", err)
	}
	if err := n.Save(v.Frontmatter()); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Verify content round-trips.
	data, err := os.ReadFile(n.Path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "changed") {
		t.Fatalf("saved body missing 'changed': %q", string(data))
	}

	// Verify no .note-*.tmp temp files linger in the directory.
	entries, err := os.ReadDir(filepath.Dir(n.Path))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".note-") && strings.HasSuffix(name, ".tmp") {
			t.Fatalf("temp file lingered: %s", name)
		}
	}
}

func TestSaveRefusesUnloaded(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"a.md": "---\ntitle: A\n---\nbody\n",
	})
	var n *Note
	if err := v.Walk(WalkFilter{}, func(x *Note) error {
		n = x
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	if n.Loaded() {
		t.Fatalf("expected lazy note")
	}
	if err := n.Save(v.Frontmatter()); err == nil {
		t.Fatalf("expected error saving an unloaded note")
	}
}

// TestSaveCreatesParentDirs verifies that atomicWrite creates intermediate
// directories when saving a note to a path whose parent does not yet exist.
func TestSaveCreatesParentDirs(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"a.md": "---\ntitle: A\n---\nbody\n",
	})
	n, err := v.ResolveByTitle("a", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// Load the note before changing the path, so the content is in memory.
	if err := n.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	// Move the note to a not-yet-existing subdirectory.
	newPath := filepath.Join(v.Root, "NewDir", "SubDir", "moved.md")
	n.Path = newPath
	if err := n.Save(v.Frontmatter()); err != nil {
		t.Fatalf("save to new dir: %v", err)
	}
	data, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("read moved note: %v", err)
	}
	if !strings.Contains(string(data), "title: A") {
		t.Errorf("moved note should contain 'title: A', got: %q", string(data))
	}
}

func TestSaveRejectsSymlinkParentEscape(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"a.md": "---\ntitle: A\n---\nbody\n",
	})
	outside := t.TempDir()
	link := filepath.Join(v.Root, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	n, err := v.ResolveByTitle("a", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := n.SetBody("changed\n"); err != nil {
		t.Fatalf("setbody: %v", err)
	}
	outsidePath := filepath.Join(outside, "escape.md")
	n.Path = filepath.Join(link, "escape.md")

	err = n.Save(v.Frontmatter())
	if err == nil {
		t.Fatal("save through symlinked parent should fail")
	}
	if !strings.Contains(err.Error(), "outside vault root") {
		t.Fatalf("error should mention outside vault root: %v", err)
	}
	if _, statErr := os.Stat(outsidePath); !os.IsNotExist(statErr) {
		t.Fatalf("outside file should not be created, stat err = %v", statErr)
	}
}

// TestLoadEmptyPath verifies that Load on a Note with an empty Path returns
// an error rather than producing a confusing `open : no such file` from
// os.ReadFile. Callers that set Path lazily can otherwise leak the call site
// into a downstream error.
func TestLoadEmptyPath(t *testing.T) {
	n := &Note{Path: ""}
	err := n.Load()
	if err == nil {
		t.Fatal("Load with empty Path should return an error")
	}
	if !strings.Contains(err.Error(), "empty path") {
		t.Errorf("error should mention empty path: %v", err)
	}
}

func TestLoadRejectsRelativePath(t *testing.T) {
	n := &Note{Path: filepath.Join("relative", "note.md")}
	err := n.Load()
	if err == nil {
		t.Fatal("Load with relative Path should return an error")
	}
	if !strings.Contains(err.Error(), "must be absolute") {
		t.Errorf("error should mention absolute path requirement: %v", err)
	}
}

// TestSaveEmptyPath verifies that Save on a loaded Note with an empty Path
// errors instead of writing temp files to the process cwd and then failing the
// rename. atomicWrite is given a destination, so an empty Path used to call
// os.CreateTemp(".", ...) and dump a sibling next to whatever the cwd was.
func TestSaveEmptyPath(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"a.md": "---\ntitle: A\n---\nbody\n",
	})
	n, err := v.ResolveByTitle("a", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := n.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	n.Path = ""
	if err := n.Save(v.Frontmatter()); err == nil {
		t.Fatal("Save with empty Path should return an error")
	}
}

func TestSaveRejectsRelativePath(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"a.md": "---\ntitle: A\n---\nbody\n",
	})
	n, err := v.ResolveByTitle("a", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := n.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	n.Path = filepath.Join("relative", "saved.md")
	err = n.Save(v.Frontmatter())
	if err == nil {
		t.Fatal("Save with relative Path should return an error")
	}
	if !strings.Contains(err.Error(), "must be absolute") {
		t.Errorf("error should mention absolute path requirement: %v", err)
	}
}

// TestSaveRejectsAStaleRevision covers the lost-update gap. Save carries no
// caller-supplied precondition, so without its own check it would happily write a
// document planned against content another writer has already replaced.
func TestSaveRejectsAStaleRevision(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"a.md": "---\ntitle: A\n---\nfirst\n",
	})
	notePath := filepath.Join(v.Root, "a.md")
	n, err := v.ResolveByTitle("a", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := n.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := n.SetBody("mine\n"); err != nil {
		t.Fatalf("setbody: %v", err)
	}

	const theirs = "---\ntitle: A\n---\ntheirs\n"
	if err := os.WriteFile(notePath, []byte(theirs), 0o644); err != nil {
		t.Fatalf("concurrent write: %v", err)
	}

	err = n.Save(v.Frontmatter())
	if !IsTxnConflict(err) {
		t.Fatalf("save err = %v, want *TxnConflictError", err)
	}
	if got := readFileString(t, notePath); got != theirs {
		t.Fatalf("a.md = %q, want the concurrent write intact", got)
	}
}

// TestSaveTwiceOnOneHandleSucceeds guards the hash refresh. Save's precondition
// pins the revision Load read, so without re-pinning after a write the second
// Save on the same handle would conflict with the first one's own output.
func TestSaveTwiceOnOneHandleSucceeds(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"a.md": "---\ntitle: A\n---\nfirst\n",
	})
	n, err := v.ResolveByTitle("a", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := n.SetBody("second\n"); err != nil {
		t.Fatalf("setbody: %v", err)
	}
	if err := n.Save(v.Frontmatter()); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := n.SetBody("third\n"); err != nil {
		t.Fatalf("setbody: %v", err)
	}
	if err := n.Save(v.Frontmatter()); err != nil {
		t.Fatalf("second save: %v", err)
	}
	if got := readFileString(t, filepath.Join(v.Root, "a.md")); !strings.Contains(got, "third") {
		t.Fatalf("a.md = %q, want the second save applied", got)
	}
}

// TestSaveRefusesANoteWithNoRoot keeps the rootless case loud. A Note built as a
// bare literal has no root, and Save needs one to name a lock file and to check
// containment — skipping both silently is how the path lock came to be
// unreachable from every production Save in the first place.
func TestSaveRefusesANoteWithNoRoot(t *testing.T) {
	dir := t.TempDir()
	notePath := filepath.Join(dir, "a.md")
	if err := os.WriteFile(notePath, []byte("---\ntitle: A\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	n := &Note{Path: notePath}
	if err := n.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := n.SetBody("changed\n"); err != nil {
		t.Fatalf("setbody: %v", err)
	}
	err := n.Save(nil)
	if err == nil {
		t.Fatal("Save on a rootless note should return an error")
	}
	if !strings.Contains(err.Error(), "no vault root") {
		t.Errorf("error should name the missing root: %v", err)
	}
	if got := readFileString(t, notePath); !strings.Contains(got, "body") {
		t.Errorf("a.md = %q, want unchanged", got)
	}
}
