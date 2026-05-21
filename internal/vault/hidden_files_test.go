package vault

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	for _, folder := range []string{"People", "People/", "/People", "/People/", "People\\", "./People", "People/.", "./People/", "People//"} {
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

// The ambiguity error must list candidates in a stable order and with
// forward-slash paths so callers can render and diff it predictably. Without
// sorting, two runs on the same vault can swap the order of candidates.
func TestResolveByTitleAmbiguityCandidatesSorted(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"Zeta/dup.md":   "# z\n",
		"alpha/dup.md":  "# a\n",
		"middle/dup.md": "# m\n",
	})
	_, err := v.ResolveByTitle("dup", "")
	if err == nil {
		t.Fatalf("expected ambiguity error")
	}
	msg := err.Error()
	// Each candidate must use forward slashes regardless of OS conventions.
	for _, want := range []string{"alpha/dup.md", "middle/dup.md", "Zeta/dup.md"} {
		if !strings.Contains(msg, want) {
			t.Errorf("candidate %q missing from error: %s", want, msg)
		}
		if strings.Contains(msg, filepath.FromSlash(want)) && filepath.Separator != '/' {
			t.Errorf("candidate %q rendered with OS separator in: %s", want, msg)
		}
	}
	// Ensure ASCII byte-order sort: uppercase 'Z' sorts before lowercase
	// letters because uppercase bytes are numerically smaller in ASCII.
	aIdx := strings.Index(msg, "alpha/dup.md")
	mIdx := strings.Index(msg, "middle/dup.md")
	zIdx := strings.Index(msg, "Zeta/dup.md")
	if !(zIdx < aIdx && aIdx < mIdx) {
		t.Errorf("candidates not in stable byte-order: %s", msg)
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

// Save previously inherited 0o600 from os.CreateTemp via the temp file's
// rename, silently narrowing the permissions of any 0o644 note in the vault.
// Round-tripping a save should leave the destination's existing mode intact.
func TestSavePreservesFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits not meaningful on Windows")
	}
	v := newTestVault(t, nil, map[string]string{
		"a.md": "---\ntitle: A\n---\nbody\n",
	})
	notePath := filepath.Join(v.Root, "a.md")
	// Force a recognizable mode that differs from os.CreateTemp's 0o600.
	if err := os.Chmod(notePath, 0o664); err != nil {
		t.Fatalf("chmod: %v", err)
	}

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
	info, err := os.Stat(notePath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o664 {
		t.Errorf("mode preservation: got %o, want %o", got, 0o664)
	}
}

// atomicWrite must not widen mode 0o000 to 0o644 on an existing file. The
// zero-mode check previously used `wantMode == 0` to detect "file not found",
// which also matches a file with deliberately restrictive 0o000 permissions.
func TestSavePreservesZeroMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits not meaningful on Windows")
	}
	dir := t.TempDir()
	notePath := filepath.Join(dir, "restricted.md")
	if err := os.WriteFile(notePath, []byte("---\ntitle: R\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	v, err := Open(&config.Config{Vault: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	n, err := v.ResolveByTitle("restricted", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// Load and mutate while the file is still readable, then restrict.
	if err := n.SetBody("changed\n"); err != nil {
		t.Fatalf("setbody: %v", err)
	}
	if err := os.Chmod(notePath, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := n.Save(&v.Cfg.Frontmatter); err != nil {
		t.Fatalf("save: %v", err)
	}

	info, err := os.Stat(notePath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o000 {
		t.Errorf("zero-mode preservation: got %o, want %o", got, 0o000)
	}

	// Clean up: restore permissions so t.TempDir removal succeeds.
	_ = os.Chmod(notePath, 0o644)
}
