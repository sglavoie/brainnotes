package vaultgit

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcherPushesAtStartupAndAtTheInterval(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	remote := t.TempDir()
	rawGit(t, remote, "init", "-q", "--bare")
	rawGit(t, h.root, "remote", "add", "origin", remote)

	h.write("note.md", "content")
	h.commitPending()

	rec := &watchRecorder{}
	w := h.newWatcher(rec)
	w.tick(ctx)
	requireKind(t, rec, WatchEventPushed)

	// Well inside the interval: no second attempt.
	rec.reset()
	h.advance(DefaultPushInterval - time.Second)
	w.tick(ctx)
	requireNoKind(t, rec, WatchEventPushed)

	h.advance(2 * time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventPushed)
}

func TestWatcherKeepsCommittingLocallyWhileTheRemoteIsUnreachable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	rawGit(t, h.root, "remote", "add", "origin", filepath.Join(t.TempDir(), "no-such-remote"))

	rec := &watchRecorder{}
	w := h.newWatcher(rec)

	h.write("note.md", "written while offline")
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)

	requireKind(t, rec, WatchEventCommitted)
	requireKind(t, rec, WatchEventPushFailed)
	if !contains(h.tracked(), "note.md") {
		t.Fatal("the local snapshot did not survive the failed push")
	}
	if _, blocked := h.blockRecord(); blocked {
		t.Fatal("a push failure latched the block marker; the remote is not a correctness input")
	}

	// Three consecutive failures are announced once, and the announcement is not
	// repeated for the same streak.
	for i := 0; i < 6; i++ {
		h.advance(DefaultPushInterval + pushBackoffMax)
		w.tick(ctx)
	}
	if got := rec.count(WatchEventPushFailing); got != 1 {
		t.Fatalf("expected exactly one streak announcement, got %d in %v", got, rec.kinds())
	}
	event := requireKind(t, rec, WatchEventPushFailing)
	if event.Attempts < pushFailureNotifyAfter {
		t.Fatalf("the streak was announced after %d attempts", event.Attempts)
	}
}

func TestWatcherPushesAutomaticallyOnceTheRemoteComesBack(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	remote := filepath.Join(t.TempDir(), "origin.git")
	rawGit(t, h.root, "remote", "add", "origin", remote)

	rec := &watchRecorder{}
	w := h.newWatcher(rec)
	h.write("note.md", "written during the outage")
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
	requireKind(t, rec, WatchEventPushFailed)

	// The remote comes back. Nothing tells the guard; the next scheduled attempt
	// is what finds out.
	rawGit(t, filepath.Dir(remote), "init", "-q", "--bare", "origin.git")
	rec.reset()
	for i := 0; i < 4 && rec.count(WatchEventPushed) == 0; i++ {
		h.advance(DefaultPushInterval + pushBackoffMax)
		w.tick(ctx)
	}
	requireKind(t, rec, WatchEventPushed)
	if _, ok := h.mgr.PushStatus(); ok {
		t.Fatal("the failure streak survived a successful push")
	}
}
