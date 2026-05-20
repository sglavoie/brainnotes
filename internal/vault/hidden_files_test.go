package vault

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// ResolveByTitle previously compared the user-supplied folder to the note's
// folder verbatim, so leading or trailing slashes caused a no-match. It now
// normalizes both like the Folder query predicate does.
func TestResolveByTitleNormalizesFolder(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "People")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "Alice.md"), []byte("# a\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	v, err := Open(&config.Config{Vault: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, folder := range []string{"People", "People/", "/People", "/People/", "People\\"} {
		n, err := v.ResolveByTitle("Alice", folder)
		if err != nil {
			t.Errorf("ResolveByTitle(Alice, %q): %v", folder, err)
			continue
		}
		if filepath.Base(n.Path) != "Alice.md" {
			t.Errorf("ResolveByTitle(Alice, %q) = %q", folder, n.Path)
		}
	}
}

// Walk previously emitted hidden files (e.g. `.DS_Store.md`, editor temp files)
// even though hidden directories were pruned. Both are now skipped consistently.
func TestWalkSkipsHiddenFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "visible.md"), []byte("# v\n"), 0o644); err != nil {
		t.Fatalf("write visible: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".hidden.md"), []byte("# h\n"), 0o644); err != nil {
		t.Fatalf("write hidden: %v", err)
	}
	v, err := Open(&config.Config{Vault: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	var paths []string
	err = v.Walk(WalkFilter{}, func(n *Note) error {
		paths = append(paths, filepath.Base(n.Path))
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	for _, p := range paths {
		if p == ".hidden.md" {
			t.Errorf("hidden file %q should be skipped, got %v", p, paths)
		}
	}
	found := false
	for _, p := range paths {
		if p == "visible.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("visible.md not found in %v", paths)
	}
}
