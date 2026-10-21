package vaultgit

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestPushWithoutRemoteIsNotAFailure(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()

	result, err := h.mgr.Push(context.Background())
	if err != nil {
		t.Fatalf("Push with no remote: %v", err)
	}
	if result.Attempted || result.Pushed {
		t.Fatalf("a vault with no remote must not attempt a push: %+v", result)
	}
}

func TestPushFailureRecordsRetryStateAndNeverBlocks(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	local := h.head()

	rawGit(t, h.root, "remote", "add", "origin", filepath.Join(t.TempDir(), "missing.git"))
	_, err := h.mgr.Push(context.Background())
	if !IsPushFailure(err) {
		t.Fatalf("Push to a missing remote = %v, want a push failure", err)
	}
	if _, blocked := h.blockRecord(); blocked {
		t.Fatal("a push failure must never latch the mutation block")
	}
	state, ok := h.mgr.PushStatus()
	if !ok || state.Attempts != 1 || state.LastError == "" {
		t.Fatalf("push state = %+v (ok=%v)", state, ok)
	}
	if h.mgr.PushDue() {
		t.Fatal("a retry must not be due immediately after a failure")
	}

	// Local work continues while the remote is unreachable: remote availability
	// is not an input to local correctness.
	h.advance(time.Minute)
	h.write("Inbox/note.md", "two\n")
	if !h.commitPending().Committed {
		t.Fatal("a snapshot failed while the remote was unreachable")
	}
	if h.head() == local {
		t.Fatal("the branch did not advance while the remote was unreachable")
	}

	// Recovery: the remote comes back and the retry state is forgotten.
	bare := t.TempDir()
	rawGit(t, bare, "-c", "init.defaultBranch=main", "init", "--bare", "-q")
	rawGit(t, h.root, "remote", "set-url", "origin", bare)
	h.advance(10 * time.Minute)
	if !h.mgr.PushDue() {
		t.Fatal("a retry must be due once the backoff has elapsed")
	}
	result, err := h.mgr.Push(context.Background())
	if err != nil {
		t.Fatalf("Push after recovery: %v", err)
	}
	if !result.Pushed {
		t.Fatalf("push result = %+v", result)
	}
	if _, ok := h.mgr.PushStatus(); ok {
		t.Fatal("a successful push must forget the failure streak")
	}
	if remoteHead := rawGit(t, bare, "rev-parse", "refs/heads/main"); remoteHead != h.head() {
		t.Fatalf("remote head = %s, want %s", remoteHead, h.head())
	}
}

// A remote that rejects the push — because it holds commits this clone does not
// — must never be resolved by rewriting history.
func TestPushNeverForces(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()

	bare := t.TempDir()
	rawGit(t, bare, "-c", "init.defaultBranch=main", "init", "--bare", "-q")
	rawGit(t, h.root, "remote", "add", "origin", bare)
	if _, err := h.mgr.Push(context.Background()); err != nil {
		t.Fatalf("Push: %v", err)
	}
	shared := h.head()

	// Another clone publishes a commit this vault has never seen.
	other := t.TempDir()
	rawGit(t, other, "clone", "-q", bare, "clone")
	clone := filepath.Join(other, "clone")
	rawGit(t, clone, "config", "user.name", "Other")
	rawGit(t, clone, "config", "user.email", "other@example.invalid")
	if err := os.WriteFile(filepath.Join(clone, "elsewhere.md"), []byte("from another machine\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	rawGit(t, clone, "add", "elsewhere.md")
	rawGit(t, clone, "commit", "-q", "-m", "from another machine")
	rawGit(t, clone, "push", "-q", "origin", "main")
	remoteHead := rawGit(t, bare, "rev-parse", "refs/heads/main")

	h.advance(time.Minute)
	h.write("Inbox/note.md", "two\n")
	h.commitPending()

	_, err := h.mgr.Push(context.Background())
	if !IsPushFailure(err) {
		t.Fatalf("Push to a diverged remote = %v, want a push failure", err)
	}
	if got := rawGit(t, bare, "rev-parse", "refs/heads/main"); got != remoteHead {
		t.Fatalf("the remote was overwritten: %s -> %s", remoteHead, got)
	}
	if !slices.Contains(h.subjects(), "brainnotes: autosave 2026-08-28T09:01:00Z") {
		t.Fatal("the local snapshot did not survive the rejected push")
	}
	if _, blocked := h.blockRecord(); blocked {
		t.Fatal("a rejected push must never latch the mutation block")
	}
	_ = shared
}

// A remote that accepts the connection and then never answers must not hold
// the push — and with it the watcher's poll loop — for longer than the push
// timeout. The stand-in receive-pack sleeps far past the shortened timeout.
func TestPushToAnUnresponsiveRemoteTimesOut(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()

	bare := t.TempDir()
	rawGit(t, bare, "-c", "init.defaultBranch=main", "init", "--bare", "-q")
	rawGit(t, h.root, "remote", "add", "origin", bare)
	rawGit(t, h.root, "config", "remote.origin.receivepack", "sh -c 'sleep 30' --")
	h.mgr.pushTimeout = 300 * time.Millisecond

	started := time.Now()
	_, err := h.mgr.Push(context.Background())
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("Push to a hung remote returned after %v; the timeout did not bound it", elapsed)
	}
	if !IsPushFailure(err) {
		t.Fatalf("Push to a hung remote = %v, want a push failure", err)
	}
	if state, ok := h.mgr.PushStatus(); !ok || state.Attempts != 1 {
		t.Fatalf("a timed-out push must record retry state: %+v (ok=%v)", state, ok)
	}
}

// --- Environment isolation ---------------------------------------------------
