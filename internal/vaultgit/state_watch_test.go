package vaultgit

import (
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/checkpoint"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

func TestWatchStateMutationWaitsForCheckpoint(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	held, err := checkpoint.Hold(h.root, checkpoint.Options{Reason: "test watch state", Drain: time.Second})
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, writeErr := h.mgr.NoteExternalChange()
		done <- writeErr
	}()
	select {
	case err := <-done:
		t.Fatalf("watch state write completed during checkpoint: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := held.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("watch state write after checkpoint: %v", err)
	}
}

// testPolicy resolves a floor the way `brainnotes.toml` would.
func testPolicy(t *testing.T, spelling string) config.AutosavePolicy {
	t.Helper()
	policy, err := config.ParseAutosaveMinInterval(spelling)
	if err != nil {
		t.Fatalf("ParseAutosaveMinInterval(%q): %v", spelling, err)
	}
	return policy
}

var watchStart = time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)

func TestWatchDebounce(t *testing.T) {
	t.Parallel()
	state := newState(t)
	// The smallest legal floor, so this test is about readiness and only readiness.
	policy := testPolicy(t, "30s")
	start := watchStart

	if state.LoadWatchState().DueAt(policy, start) {
		t.Fatal("a vault with no open burst is never due")
	}
	if _, err := state.NoteChange(policy, start); err != nil {
		t.Fatalf("NoteChange: %v", err)
	}
	if state.LoadWatchState().DueAt(policy, start.Add(10*time.Second)) {
		t.Fatal("a burst must not commit before the quiet period")
	}
	if !state.LoadWatchState().DueAt(policy, start.Add(DebounceQuiet)) {
		t.Fatal("a burst must commit once the working tree is quiet")
	}

	// An editor that autosaves faster than the quiet period would defer forever
	// without the maximum-latency bound.
	for offset := time.Duration(0); offset < DebounceMaxLatency; offset += 20 * time.Second {
		if _, err := state.NoteChange(policy, start.Add(offset)); err != nil {
			t.Fatalf("NoteChange: %v", err)
		}
	}
	if !state.LoadWatchState().DueAt(policy, start.Add(DebounceMaxLatency)) {
		t.Fatal("a continuously changing burst must still commit at the latency bound")
	}

	if _, err := state.NoteCommit(policy, start.Add(DebounceMaxLatency)); err != nil {
		t.Fatalf("NoteCommit: %v", err)
	}
	if state.LoadWatchState().DueAt(policy, start.Add(time.Hour)) {
		t.Fatal("a committed burst must be closed")
	}
}

// The regression this whole plan exists for. A vault under continuous editing
// used to acquire an external snapshot every two minutes forever, because
// readiness was the only deadline: the observed history was commits 2, 3, 1, and
// 4 minutes apart. Under a five-minute floor the same edit pattern produces one
// commit per five minutes and no more.
func TestExternalAutosavesCannotOutpaceTheFloor(t *testing.T) {
	t.Parallel()
	state := newState(t)
	policy := testPolicy(t, "5m")
	now := watchStart

	// The first snapshot is governed by readiness alone: nothing has anchored a
	// floor yet, so a fresh vault does not wait five minutes for its first commit.
	if _, err := state.NoteChange(policy, now); err != nil {
		t.Fatalf("NoteChange: %v", err)
	}
	if !state.LoadWatchState().DueAt(policy, now.Add(DebounceQuiet)) {
		t.Fatal("the first external snapshot must not wait for a floor nothing anchors")
	}
	firstCommit := now.Add(DebounceQuiet)
	if _, err := state.NoteCommit(policy, firstCommit); err != nil {
		t.Fatalf("NoteCommit: %v", err)
	}

	// Now edit continuously for an hour, exactly as an editor with autosave does,
	// and count how often the schedule says a snapshot may be taken.
	commits := []time.Time{firstCommit}
	for offset := time.Duration(0); offset < time.Hour; offset += 20 * time.Second {
		at := firstCommit.Add(offset)
		if _, err := state.NoteChange(policy, at); err != nil {
			t.Fatalf("NoteChange: %v", err)
		}
		if state.LoadWatchState().DueAt(policy, at) {
			if _, err := state.NoteCommit(policy, at); err != nil {
				t.Fatalf("NoteCommit: %v", err)
			}
			commits = append(commits, at)
		}
	}

	for i := 1; i < len(commits); i++ {
		if gap := commits[i].Sub(commits[i-1]); gap < policy.Interval {
			t.Fatalf("snapshots %d and %d are %s apart, inside the %s floor", i-1, i, gap, policy.Canonical)
		}
	}
	// One hour of continuous editing under a five-minute floor is twelve intervals.
	// The old readiness-only schedule produced one every two minutes, which is what
	// the 2/3/1/4-minute history recorded.
	if len(commits) > 14 {
		t.Fatalf("an hour of continuous editing produced %d snapshots under a %s floor", len(commits), policy.Canonical)
	}
	if len(commits) < 10 {
		t.Fatalf("an hour of continuous editing produced only %d snapshots; the floor is deferring past its own interval", len(commits))
	}
}

// The floor pushes a due time later and never earlier, so an interval shorter
// than the readiness window changes nothing about when a burst commits.
func TestAFloorShorterThanReadinessNeverMovesTheDueTime(t *testing.T) {
	t.Parallel()
	state := newState(t)
	policy := testPolicy(t, "30s")
	now := watchStart

	if _, err := state.NoteCommit(policy, now); err != nil {
		t.Fatalf("NoteCommit: %v", err)
	}
	if _, err := state.NoteChange(policy, now.Add(time.Second)); err != nil {
		t.Fatalf("NoteChange: %v", err)
	}
	due, ok := state.LoadWatchState().DueTime(policy)
	if !ok {
		t.Fatal("an open burst under an enabled policy must have a due time")
	}
	// Readiness is last_change + 30s = t+31s; the floor is last_commit + 30s = t+30s.
	if want := now.Add(time.Second + DebounceQuiet); !due.Equal(want) {
		t.Fatalf("due = %s, want the readiness time %s", due, want)
	}
}

// And an interval longer than the readiness window is what actually binds.
func TestAFloorLongerThanReadinessDefersTheDueTime(t *testing.T) {
	t.Parallel()
	state := newState(t)
	policy := testPolicy(t, "10m")
	now := watchStart

	if _, err := state.NoteCommit(policy, now); err != nil {
		t.Fatalf("NoteCommit: %v", err)
	}
	if _, err := state.NoteChange(policy, now.Add(time.Second)); err != nil {
		t.Fatalf("NoteChange: %v", err)
	}
	loaded := state.LoadWatchState()
	due, ok := loaded.DueTime(policy)
	if !ok {
		t.Fatal("an open burst under an enabled policy must have a due time")
	}
	want := now.Add(10 * time.Minute)
	if !due.Equal(want) {
		t.Fatalf("due = %s, want the floor %s", due, want)
	}
	// The boundary is inclusive on both sides of the comparison the watcher makes.
	if loaded.DueAt(policy, want.Add(-time.Nanosecond)) {
		t.Fatal("a burst is not due one nanosecond before its floor elapses")
	}
	if !loaded.DueAt(policy, want) {
		t.Fatal("a burst is due at the exact instant its floor elapses")
	}
}

// A disabled policy has no schedule at all: nothing is ever due and nothing is
// published for a health consumer to read as a pending snapshot.
func TestADisabledPolicyIsNeverDue(t *testing.T) {
	t.Parallel()
	state := newState(t)
	off := testPolicy(t, config.AutosaveOff)

	if _, err := state.NoteChange(off, watchStart); err != nil {
		t.Fatalf("NoteChange: %v", err)
	}
	loaded := state.LoadWatchState()
	if loaded.DueAt(off, watchStart.Add(24*time.Hour)) {
		t.Fatal("an off policy must never report a due burst")
	}
	if _, ok := loaded.DueTime(off); ok {
		t.Fatal("an off policy must publish no due time")
	}
	if loaded.NextCommitDueAt != nil {
		t.Fatalf("next_commit_due_at = %v under an off policy", loaded.NextCommitDueAt)
	}
	if loaded.AutosaveMinInterval != config.AutosaveOff {
		t.Fatalf("autosave_min_interval = %q, want %q", loaded.AutosaveMinInterval, config.AutosaveOff)
	}
	// The burst timestamps are still recorded, so re-enabling later treats the
	// dirty tree as the burst it already is rather than as a fresh one.
	if loaded.FirstChangeAt.IsZero() {
		t.Fatal("an off policy discarded the burst it was asked to record")
	}
}
