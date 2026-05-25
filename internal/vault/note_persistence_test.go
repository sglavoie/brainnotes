package vault

import (
	"os"
	"path/filepath"
	"runtime"
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
	if err := n.Save(&v.Cfg.Frontmatter); err != nil {
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
	if err := n.Save(&v.Cfg.Frontmatter); err == nil {
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
	if err := n.Save(&v.Cfg.Frontmatter); err != nil {
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
	if err := n.Save(&v.Cfg.Frontmatter); err == nil {
		t.Fatal("Save with empty Path should return an error")
	}
}

// TestSaveWritesNewFileMode verifies that a fresh save (no existing file at
// dest) lands with 0o644 rather than leaking the temp file's 0o600 default
// from os.CreateTemp.
func TestSaveWritesNewFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits not meaningful on Windows")
	}
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
	newPath := filepath.Join(v.Root, "brand-new.md")
	n.Path = newPath
	if err := n.Save(&v.Cfg.Frontmatter); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(newPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("new file mode: got %o, want %o", got, 0o644)
	}
}
