package vaultgit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Opening -----------------------------------------------------------------

// A vault without Git is not a broken vault. It reports ErrUnmanaged so callers
// carry on with no content history rather than treating it as damage.
func TestOpenUnmanagedVault(t *testing.T) {
	_, err := Open(context.Background(), t.TempDir(), Options{})
	if !IsUnmanaged(err) {
		t.Fatalf("Open on a non-Git vault = %v, want ErrUnmanaged", err)
	}
}

func TestOpenRejectsGitFile(t *testing.T) {
	requireGit(t)
	isolateHome(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatalf("write .git file: %v", err)
	}
	_, err := Open(context.Background(), root, Options{})
	if KindOf(err) != KindRepositoryState {
		t.Fatalf("Open with a .git file = %v, want a repository-state failure", err)
	}
	if IsUnmanaged(err) {
		t.Fatal("a linked worktree must not be reported as an unmanaged vault")
	}
}

// A `.git` directory that is not a repository is the partially-initialized case:
// the user believes they have history and must be told plainly that they do not.
func TestOpenRejectsMalformedRepository(t *testing.T) {
	requireGit(t)
	isolateHome(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "objects"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, err := Open(context.Background(), root, Options{})
	if KindOf(err) != KindRepositoryState {
		t.Fatalf("Open on a malformed repository = %v, want a repository-state failure", err)
	}
}

// A vault that is merely a subdirectory of some larger repository — a home
// directory under version control, say — is not managed. Every path the engine
// computed would otherwise be relative to the wrong root, and it would commit
// the vault into a history that is not the vault's.
func TestOpenIgnoresRepositoryAboveTheVault(t *testing.T) {
	h := newHarness(t)
	nested := filepath.Join(h.root, "sub", "vault")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, err := Open(context.Background(), nested, Options{})
	if !IsUnmanaged(err) {
		t.Fatalf("Open below the repository root = %v, want ErrUnmanaged", err)
	}
}

func TestOpenRejectsDetachedHead(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	rawGit(t, h.root, "checkout", "--detach", "-q", "HEAD")

	_, err := Open(context.Background(), h.root, Options{})
	if KindOf(err) != KindRepositoryState {
		t.Fatalf("Open on a detached HEAD = %v, want a repository-state failure", err)
	}
}

// --- Additions and modifications ---------------------------------------------

// The engine must not be steerable by the environment it happens to inherit. A
// stray GIT_INDEX_FILE or GIT_DIR in the user's shell would otherwise redirect
// a snapshot into another repository.
func TestStrayGitEnvironmentIsIgnored(t *testing.T) {
	h := newHarness(t)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(t.TempDir(), "hijacked-index"))
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "hijacked.git"))
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	t.Setenv("GIT_AUTHOR_NAME", "Impostor")

	h.write("Inbox/note.md", "one\n")
	if !h.commitPending().Committed {
		t.Fatal("the snapshot did not run")
	}
	if !h.trackedSet()["Inbox/note.md"] {
		t.Fatal("the snapshot landed somewhere other than the vault")
	}
	if author := rawGit(t, h.root, "log", "-1", "--format=%an"); author != "BrainNotes" {
		t.Fatalf("commit author = %q, want the engine identity", author)
	}
}

// A NUL byte can never be a legal argument, and rejecting it here means no
// caller can smuggle one into a git invocation.
func TestGitArgumentsRejectNULBytes(t *testing.T) {
	requireGit(t)
	g := &gitExec{exe: requireGit(t), dir: t.TempDir()}
	_, err := g.run(context.Background(), gitCall{args: []string{"status", "bad\x00arg"}})
	if err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Fatalf("run with a NUL argument = %v, want a rejection", err)
	}
}

func TestOpenRequiresAVaultRoot(t *testing.T) {
	if _, err := Open(context.Background(), "  ", Options{}); err == nil {
		t.Fatal("Open with an empty root must fail")
	}
	if _, err := Open(context.Background(), "  ", Options{}); errors.Is(err, ErrUnmanaged) {
		t.Fatal("an empty root is a programming error, not an unmanaged vault")
	}
}
