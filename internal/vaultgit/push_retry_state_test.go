package vaultgit

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// A push that lands but cannot remove the old retry record must not leave that
// record steering the schedule: the next failure is the first of a new streak,
// and nothing waits out the old backoff.
func TestPushSuccessWithUnremovableRetryStateStartsTheNextStreakFresh(t *testing.T) {
	h := newHarness(t)
	h.write("Inbox/note.md", "one\n")
	h.commitPending()
	missing := filepath.Join(t.TempDir(), "missing.git")
	rawGit(t, h.root, "remote", "add", "origin", missing)
	for i := 0; i < 3; i++ {
		if _, err := h.mgr.Push(context.Background()); !IsPushFailure(err) {
			t.Fatalf("Push to a missing remote = %v, want a push failure", err)
		}
		h.advance(10 * time.Minute)
	}
	if state, ok := h.mgr.PushStatus(); !ok || state.Attempts != 3 {
		t.Fatalf("push state = %+v (ok=%v), want three attempts", state, ok)
	}

	bare := t.TempDir()
	rawGit(t, bare, "-c", "init.defaultBranch=main", "init", "--bare", "-q")
	rawGit(t, h.root, "remote", "set-url", "origin", bare)
	h.mgr.state.failWrite = func(name string) error {
		if name == pushStateFileName {
			return errors.New("injected: push.json could not be removed")
		}
		return nil
	}
	result, err := h.mgr.Push(context.Background())
	if err != nil || !result.Pushed || result.RetryStateErr == nil {
		t.Fatalf("Push with an unremovable retry record = %+v, %v; want pushed with RetryStateErr", result, err)
	}
	h.mgr.state.failWrite = nil
	if _, ok := h.mgr.PushStatus(); ok {
		t.Fatal("a resolved streak must not be reported after a successful push")
	}
	if !h.mgr.PushDue() {
		t.Fatal("a resolved streak must not hold the next push back")
	}

	rawGit(t, h.root, "remote", "set-url", "origin", missing)
	result, err = h.mgr.Push(context.Background())
	if !IsPushFailure(err) {
		t.Fatalf("Push to a missing remote = %v, want a push failure", err)
	}
	if result.Retry.Attempts != 1 || result.Retry.NextAttemptAfter.Sub(result.Retry.LastAttemptAt) != pushBackoffBase {
		t.Fatalf("retry after a resolved streak = %+v, want a fresh first attempt", result.Retry)
	}
	h.advance(time.Minute)
	if _, err := h.mgr.Push(context.Background()); !IsPushFailure(err) {
		t.Fatalf("second failure = %v", err)
	}
	if state, ok := h.mgr.PushStatus(); !ok || state.Attempts != 2 {
		t.Fatalf("push state = %+v (ok=%v), want the new streak to keep counting", state, ok)
	}
}
