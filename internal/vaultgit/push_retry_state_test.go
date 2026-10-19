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
		requirePushFailure(t, h)
		h.advance(10 * time.Minute)
	}
	requirePushAttempts(t, h, 3)

	pushWithUnremovableRetryState(t, h)
	if _, ok := h.mgr.PushStatus(); ok || !h.mgr.PushDue() {
		t.Fatal("a resolved streak must be neither reported nor hold the next push back")
	}

	rawGit(t, h.root, "remote", "set-url", "origin", missing)
	retry := requirePushFailure(t, h)
	if retry.Attempts != 1 || retry.NextAttemptAfter.Sub(retry.LastAttemptAt) != pushBackoffBase {
		t.Fatalf("retry after a resolved streak = %+v, want a fresh first attempt", retry)
	}
	h.advance(time.Minute)
	requirePushFailure(t, h)
	requirePushAttempts(t, h, 2)
}

func requirePushFailure(t *testing.T, h *testHarness) PushState {
	t.Helper()
	result, err := h.mgr.Push(context.Background())
	if !IsPushFailure(err) {
		t.Fatalf("Push to a missing remote = %v, want a push failure", err)
	}
	return result.Retry
}

func requirePushAttempts(t *testing.T, h *testHarness, want int) {
	t.Helper()
	if state, ok := h.mgr.PushStatus(); !ok || state.Attempts != want {
		t.Fatalf("push state = %+v (ok=%v), want %d attempts", state, ok, want)
	}
}

// pushWithUnremovableRetryState points origin at a reachable remote and pushes
// while removing push.json fails.
func pushWithUnremovableRetryState(t *testing.T, h *testHarness) {
	t.Helper()
	bare := t.TempDir()
	rawGit(t, bare, "-c", "init.defaultBranch=main", "init", "--bare", "-q")
	rawGit(t, h.root, "remote", "set-url", "origin", bare)
	h.mgr.state.failWrite = func(name string) error {
		if name == pushStateFileName {
			return errors.New("injected: push.json could not be removed")
		}
		return nil
	}
	defer func() { h.mgr.state.failWrite = nil }()
	result, err := h.mgr.Push(context.Background())
	if err != nil || !result.Pushed || result.RetryStateErr == nil {
		t.Fatalf("Push with an unremovable retry record = %+v, %v; want pushed with RetryStateErr", result, err)
	}
}
