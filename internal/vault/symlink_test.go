package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
)

// Walk previously followed symlinks pointing outside the vault. WalkDir
// reports a symlink-to-file with d.IsDir() == false, so a link named
// `linked.md` would be emitted as a regular note even though its target
// lives outside the vault root. The CLI assumes every note Walk emits is
// contained in the vault, so that leak breaks rename/move/set guarantees.
func TestWalkSkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("# secret\n"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "regular.md"), []byte("# r\n"), 0o644); err != nil {
		t.Fatalf("write regular: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(dir, "linked.md")); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}
	// A symlink pointing at a directory should also be skipped entirely.
	if err := os.Symlink(outside, filepath.Join(dir, "linkdir")); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	v, err := Open(&config.Config{Vault: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	var paths []string
	if err := v.Walk(WalkFilter{}, func(n *Note) error {
		rel, _ := filepath.Rel(v.Root, n.Path)
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	for _, p := range paths {
		if p == "linked.md" || p == "linkdir/secret.md" {
			t.Errorf("symlinked entry %q should be skipped, got %v", p, paths)
		}
	}
	found := false
	for _, p := range paths {
		if p == "regular.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("regular.md not found in %v", paths)
	}
}

// Open must resolve a symlink root to the real directory path. Without
// resolution, filepath.WalkDir sees a symlink (not a directory) via Lstat on
// the root entry and does not descend, silently returning zero notes.
func TestOpenResolvesSymlinkRoot(t *testing.T) {
	real := t.TempDir()
	if err := os.WriteFile(filepath.Join(real, "note.md"), []byte("# n\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	link := filepath.Join(t.TempDir(), "vault_link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	v, err := Open(&config.Config{Vault: link})
	if err != nil {
		t.Fatalf("open via symlink: %v", err)
	}
	// Root should be the resolved real directory, not the symlink.
	if v.Root == link {
		t.Errorf("Root should be resolved, got symlink %q", v.Root)
	}

	var paths []string
	if err := v.Walk(WalkFilter{}, func(n *Note) error {
		paths = append(paths, filepath.Base(n.Path))
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(paths) != 1 || paths[0] != "note.md" {
		t.Errorf("expected [note.md], got %v", paths)
	}
}

// Open must resolve chained symlinks (link2 -> link1 -> realdir) so that
// WalkDir can descend into the real directory. Before the fix,
// resolveRootSymlink only resolved one hop, leaving v.Root pointing at an
// intermediate symlink that WalkDir treated as a non-directory.
func TestOpenResolvesChainedSymlinkRoot(t *testing.T) {
	real := t.TempDir()
	if err := os.WriteFile(filepath.Join(real, "note.md"), []byte("# n\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	parent := t.TempDir()
	link1 := filepath.Join(parent, "link1")
	link2 := filepath.Join(parent, "link2")
	if err := os.Symlink(real, link1); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	if err := os.Symlink(link1, link2); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	v, err := Open(&config.Config{Vault: link2})
	if err != nil {
		t.Fatalf("open via chained symlink: %v", err)
	}
	// Root must be the final real directory, not any intermediate symlink.
	fi, err := os.Lstat(v.Root)
	if err != nil {
		t.Fatalf("lstat root: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("Root %q is still a symlink after resolution", v.Root)
	}

	var paths []string
	if err := v.Walk(WalkFilter{}, func(n *Note) error {
		paths = append(paths, filepath.Base(n.Path))
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(paths) != 1 || paths[0] != "note.md" {
		t.Errorf("expected [note.md], got %v", paths)
	}
}

// Open must reject a vault root that is a cyclic symlink chain rather than
// looping forever. The POSIX convention is to return ELOOP; we return a
// descriptive error after a bounded number of hops.
func TestOpenRejectsCyclicSymlinkRoot(t *testing.T) {
	parent := t.TempDir()
	a := filepath.Join(parent, "a")
	b := filepath.Join(parent, "b")
	if err := os.Symlink(b, a); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	if err := os.Symlink(a, b); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	_, err := Open(&config.Config{Vault: a})
	if err == nil {
		t.Fatal("expected error for cyclic symlink root")
	}
	if !strings.Contains(err.Error(), "symbolic links") {
		t.Errorf("error should mention symbolic links: %v", err)
	}
}
