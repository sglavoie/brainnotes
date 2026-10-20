package vaultgit

import (
	"testing"
	"time"
)

// A retry scheduled further out than the backoff ceiling can only have been
// written before the wall clock stepped backwards. It is due rather than a
// pause for however long the clock jumped.
func TestPushRetryIsDueAfterTheClockStepsBack(t *testing.T) {
	state := newState(t)
	failure, err := state.RecordPushFailure(watchStart, "connection refused")
	if err != nil {
		t.Fatalf("RecordPushFailure: %v", err)
	}
	if failure.DueAt(watchStart) {
		t.Fatal("a retry must not be due immediately after a failure")
	}
	if failure.DueAt(watchStart.Add(-pushBackoffBase)) {
		t.Fatal("a retry inside the backoff ceiling must still be honoured")
	}
	if !failure.DueAt(watchStart.Add(-24 * time.Hour)) {
		t.Fatal("a retry a day past the backoff ceiling must be due after the clock steps back")
	}
}

// A wall clock that steps backwards leaves the burst and the floor anchored in
// the future. They are pulled back to now and the correction persisted, so the
// snapshot waits the ordinary schedule from now and not for however long the
// clock jumped.
func TestFutureAnchorsAreClampedAfterTheClockStepsBack(t *testing.T) {
	state := newState(t)
	policy := testPolicy(t, "5m")
	if _, err := state.NoteCommit(policy, watchStart); err != nil {
		t.Fatalf("NoteCommit: %v", err)
	}
	if _, err := state.NoteChange(policy, watchStart.Add(time.Second)); err != nil {
		t.Fatalf("NoteChange: %v", err)
	}

	stepped := watchStart.Add(-24 * time.Hour)
	refreshed, err := state.RefreshWatchState(policy, stepped)
	if err != nil {
		t.Fatalf("RefreshWatchState: %v", err)
	}
	if !refreshed.FirstChangeAt.Equal(stepped) || !refreshed.LastChangeAt.Equal(stepped) || !refreshed.LastCommitAt.Equal(stepped) {
		t.Fatalf("future anchors were not clamped to now: %+v", refreshed)
	}

	// The clamp is durable, so a later poll sees the same deadline rather than
	// one sliding forward with the clock: the floor binds five minutes after the
	// step and not a moment later.
	loaded := state.LoadWatchState()
	if !loaded.LastCommitAt.Equal(stepped) {
		t.Fatalf("the clamp was not persisted: %+v", loaded)
	}
	if loaded.DueAt(policy, stepped.Add(4*time.Minute)) {
		t.Fatal("the clamped floor must still be honoured")
	}
	if !loaded.DueAt(policy, stepped.Add(5*time.Minute)) {
		t.Fatal("a burst must be due once the floor measured from the clamp elapses")
	}
}
