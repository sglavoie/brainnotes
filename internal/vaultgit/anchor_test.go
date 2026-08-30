package vaultgit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The floor's anchor used to live in exactly one field, written exactly once,
// after the commit it describes had already landed. Everything in this file is
// about the window that left open: a successful external snapshot whose
// LastCommitAt never reached the disk, because the write failed or the process
// died between the two. The vault would then schedule from the *previous*
// snapshot and could take a second one inside its own configured interval.
//
// The repair is a second durable field written before the attempt rather than
// after it, so no ordering exists in which a commit is in the history and
// nothing on disk anchors it.
//
// The marker is updated through the recovery intent once the ref is observed to
// land. The end-to-end tests below therefore run under a clock that advances on
// every read, so the write-ahead marker, commit object, and observed ref landing
// are distinct instants exactly as they are on a real disk.

// An attempt that has been recorded but not yet completed anchors the floor
// exactly as a completed one does. This is the whole of the fail-closed rule,
// stated at the level where the schedule is computed.
func TestAnInFlightExternalSnapshotAnchorsTheFloor(t *testing.T) {
	state := newState(t)
	policy := testPolicy(t, "5m")

	if _, err := state.BeginExternalCommit(policy, watchStart); err != nil {
		t.Fatalf("BeginExternalCommit: %v", err)
	}
	if _, err := state.NoteChange(policy, watchStart.Add(10*time.Second)); err != nil {
		t.Fatalf("NoteChange: %v", err)
	}

	// Readiness has elapsed and the record names no completed snapshot at all, so
	// the marker is the only thing that can be holding this burst back.
	ready := watchStart.Add(10*time.Second + DebounceQuiet)
	if state.LoadWatchState().DueAt(policy, ready) {
		t.Fatal("a ready burst committed inside the floor of an unfinished snapshot")
	}
	due, ok := state.LoadWatchState().DueTime(policy)
	if !ok || !due.Equal(watchStart.Add(5*time.Minute)) {
		t.Fatalf("due = %s (ok=%v), want the in-flight anchor plus the interval %s", due, ok, watchStart.Add(5*time.Minute))
	}
	if !state.LoadWatchState().DueAt(policy, watchStart.Add(5*time.Minute)) {
		t.Fatal("the burst never became due; a crash must defer the floor, not stop it")
	}
}

// The marker is a statement about one unfinished attempt, so completing that
// attempt replaces it rather than adding to it: from then on the anchor names
// when the snapshot finished.
func TestCompletingAnExternalSnapshotRetiresTheInFlightAnchor(t *testing.T) {
	state := newState(t)
	policy := testPolicy(t, "5m")

	if _, err := state.BeginExternalCommit(policy, watchStart); err != nil {
		t.Fatalf("BeginExternalCommit: %v", err)
	}
	done := watchStart.Add(2 * time.Second)
	if _, err := state.NoteCommit(policy, done); err != nil {
		t.Fatalf("NoteCommit: %v", err)
	}

	record := state.LoadWatchState()
	if record.ExternalCommitStartedAt != nil {
		t.Fatalf("a finished snapshot left its in-flight anchor behind: %s", record.ExternalCommitStartedAt)
	}
	if !record.LastCommitAt.Equal(done) {
		t.Fatalf("last_commit_at = %s, want the instant the snapshot finished", record.LastCommitAt)
	}
	if got := record.externalAnchor(); !got.Equal(done) {
		t.Fatalf("anchor = %s, want %s", got, done)
	}
}

// Only the attempt that wrote a marker may retire it. Matching the instant is
// what stops a pass that recorded nothing from lifting a floor some other
// attempt is still holding.
func TestAbandoningAnAttemptRetiresOnlyItsOwnAnchor(t *testing.T) {
	state := newState(t)
	policy := testPolicy(t, "5m")

	if _, err := state.BeginExternalCommit(policy, watchStart); err != nil {
		t.Fatalf("BeginExternalCommit: %v", err)
	}
	if _, err := state.AbandonExternalCommit(policy, watchStart.Add(time.Second)); err != nil {
		t.Fatalf("AbandonExternalCommit: %v", err)
	}
	if state.LoadWatchState().ExternalCommitStartedAt == nil {
		t.Fatal("a pass retired an in-flight anchor it had not written")
	}

	if _, err := state.AbandonExternalCommit(policy, watchStart); err != nil {
		t.Fatalf("AbandonExternalCommit: %v", err)
	}
	if got := state.LoadWatchState().ExternalCommitStartedAt; got != nil {
		t.Fatalf("the attempt that wrote the anchor could not retire it: %s", got)
	}
}

// The on-disk shape a crash between a landed commit and its completion write
// leaves behind is "an in-flight marker and a clean working tree", so settling
// the burst is the first thing that runs afterwards. It must leave the marker
// alone: retiring it there would drop the only durable evidence of that
// snapshot and hand the next burst a stale floor.
func TestSettlingABurstKeepsTheInFlightAnchor(t *testing.T) {
	state := newState(t)
	policy := testPolicy(t, "5m")

	if _, err := state.NoteChange(policy, watchStart); err != nil {
		t.Fatalf("NoteChange: %v", err)
	}
	if _, err := state.BeginExternalCommit(policy, watchStart.Add(time.Minute)); err != nil {
		t.Fatalf("BeginExternalCommit: %v", err)
	}
	if closed, err := state.NoteSettled(policy); err != nil || !closed {
		t.Fatalf("NoteSettled: closed=%v err=%v", closed, err)
	}

	settled := state.LoadWatchState()
	if settled.ExternalCommitStartedAt == nil {
		t.Fatal("settling the burst dropped the anchor of an unfinished snapshot")
	}
	if _, err := state.NoteChange(policy, watchStart.Add(2*time.Minute)); err != nil {
		t.Fatalf("NoteChange: %v", err)
	}
	if state.LoadWatchState().DueAt(policy, watchStart.Add(3*time.Minute)) {
		t.Fatal("the burst after a settled crash committed inside the floor")
	}
}

// The end-to-end version, against a real repository: a snapshot that takes real
// time lands, the write that would record its completion fails, and the vault
// must still refuse a second external snapshot for the whole configured interval
// after the first ref was observed to land.
func TestLostCompletionWriteHoldsFloorAfterObservedRefLanding(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	rec := &watchRecorder{}
	w := h.newWatcher(rec)
	ctx := context.Background()

	h.write("note.md", "first")
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	if !h.mgr.ExternalCommitDue() {
		t.Fatal("the first burst never became due")
	}

	// The snapshot is taken directly rather than through the poll, so the two
	// watch-state writes it makes are the only ones the injection has to count. It
	// runs under a clock that moves on every read, so the write-ahead marker,
	// authored commit timestamp, and observed ref landing are separated.
	restore := h.slowClock(45 * time.Second)
	failWatchWriteFrom(h.mgr, 2)
	result, err := h.mgr.CommitPending(ctx)
	h.mgr.state.failWrite = nil
	restore()
	if err != nil {
		t.Fatalf("a failed scheduling write turned a successful snapshot into an error: %v", err)
	}
	if !result.Committed {
		t.Fatalf("the fixture never took a snapshot: %+v", result)
	}
	landedAt := requireObservedLandingBound(t, h, result)

	lost := h.mgr.WatchStatus()
	if !lost.LastCommitAt.IsZero() {
		t.Fatalf("the injected failure never landed: last_commit_at = %s", lost.LastCommitAt)
	}

	// Four minutes of editing inside the five-minute floor. The burst the failed
	// write left open is already past its own latency bound, so anything short of
	// the floor would commit on the very next poll.
	requireNoSnapshotBefore(t, ctx, h, w, landedAt.Add(5*time.Minute))
}

// The same window, survived by a different process. Everything held in memory is
// gone, which is the case the finding named: a restarted guard reads only
// watch.json and must schedule from what a crash left there — including how long
// the snapshot it is recovering from took.
func TestARestartAfterALostCompletionWriteStillHoldsTheFloor(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	ctx := context.Background()

	h.write("note.md", "first")
	if _, err := h.mgr.NoteExternalChange(); err != nil {
		t.Fatalf("NoteExternalChange: %v", err)
	}
	h.advance(DebounceQuiet + time.Second)

	restore := h.slowClock(45 * time.Second)
	failWatchWriteFrom(h.mgr, 2)
	result, err := h.mgr.CommitPending(ctx)
	h.mgr.state.failWrite = nil
	restore()
	if err != nil || !result.Committed {
		t.Fatalf("the fixture never took a snapshot: %+v (%v)", result, err)
	}
	landedAt := requireObservedLandingBound(t, h, result)

	// The guard dies here and a new one starts on the same vault. Open proves the
	// intent's commit reached the ref, publishes its observed landing bound as the
	// completion anchor, and only then retires the intent.
	h.mgr = h.open(Options{})
	recovered := h.mgr.WatchStatus()
	if !recovered.LastCommitAt.Equal(landedAt) {
		t.Fatalf("the restarted process anchored at %s, want observed landing %s", recovered.LastCommitAt, landedAt)
	}
	if recovered.ExternalCommitStartedAt != nil {
		t.Fatalf("the restarted process left the recovered intent marker behind: %s", recovered.ExternalCommitStartedAt)
	}
	requireNoSnapshotBefore(t, ctx, h, h.newWatcher(&watchRecorder{}), landedAt.Add(5*time.Minute))
}

// requireObservedLandingBound asserts that the recovery intent persisted the
// first clock reading after the ref was observed to land, and returns that bound.
//
// The authored Git timestamp is deliberately not the boundary: it is chosen
// before update-ref and cannot prove when the ref actually became visible.
func requireObservedLandingBound(t *testing.T, h *testHarness, result CommitResult) time.Time {
	t.Helper()
	if result.landedAt.IsZero() {
		t.Fatal("the successful snapshot reported no observed ref landing")
	}
	times := h.commitTimes()
	if len(times) == 0 {
		t.Fatal("the fixture left no commit for the landing bound to cover")
	}
	committedAt := times[0]
	if !result.landedAt.After(committedAt) {
		t.Fatalf("observed landing = %s, want an instant after authored commit time %s", result.landedAt, committedAt)
	}
	intent, present := h.mgr.Interrupted()
	if !present || intent.LandedAt == nil {
		t.Fatal("the failed completion write left no durable observed landing in the recovery intent")
	}
	if !intent.LandedAt.Equal(result.landedAt) {
		t.Fatalf("intent landed_at = %s, want observed landing %s", intent.LandedAt, result.landedAt)
	}
	return result.landedAt
}

// requireNoSnapshotBefore edits and polls w up to floorEnd, asserting the vault
// takes no further external snapshot until then and exactly one once the floor
// has elapsed.
func requireNoSnapshotBefore(t *testing.T, ctx context.Context, h *testHarness, w *Watcher, floorEnd time.Time) {
	t.Helper()
	before := len(h.subjects())

	for i := 0; h.clock.Add(20 * time.Second).Before(floorEnd); i++ {
		h.advance(20 * time.Second)
		h.write("note.md", strings.Repeat("x", i+1))
		w.tick(ctx)
	}
	if got := len(h.subjects()) - before; got != 0 {
		t.Fatalf("%d external snapshots landed inside the floor after a lost completion write", got)
	}

	h.advance(time.Minute)
	w.tick(ctx)
	if got := len(h.subjects()) - before; got != 1 {
		t.Fatalf("the deferred burst produced %d commits once the floor elapsed, want one", got)
	}
	times := h.commitTimes()
	if gap := times[0].Sub(times[1]); gap < 5*time.Minute {
		t.Fatalf("two external snapshots are %s apart, inside the 5m floor", gap)
	}
	// And the vault is back on the durable anchor rather than the crash marker.
	resumed := h.mgr.WatchStatus()
	if resumed.LastCommitAt.IsZero() {
		t.Fatal("the recovering snapshot did not record its own completion")
	}
	if resumed.ExternalCommitStartedAt != nil {
		t.Fatalf("the crash marker outlived the snapshot that replaced it: %s", resumed.ExternalCommitStartedAt)
	}
}

// An external commit keeps its deliberately preselected authored date, but the
// floor is anchored at the later instant its ref was observed to land.
func TestASlowSnapshotThatCompletesStillAnchorsAtItsCompletion(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	ctx := context.Background()

	h.write("note.md", "first")
	if _, err := h.mgr.NoteExternalChange(); err != nil {
		t.Fatalf("NoteExternalChange: %v", err)
	}
	h.advance(DebounceQuiet + time.Second)

	restore := h.slowClock(45 * time.Second)
	result, err := h.mgr.CommitPending(ctx)
	restore()
	if err != nil || !result.Committed {
		t.Fatalf("the fixture never took a snapshot: %+v (%v)", result, err)
	}

	record := h.mgr.WatchStatus()
	if record.ExternalCommitStartedAt != nil {
		t.Fatalf("a finished snapshot left its in-flight anchor behind: %s", record.ExternalCommitStartedAt)
	}
	if !record.LastCommitAt.Equal(result.landedAt) {
		t.Fatalf("last_commit_at = %s, want observed ref landing %s", record.LastCommitAt, result.landedAt)
	}
	if got := record.externalAnchor(); !got.Equal(record.LastCommitAt.UTC()) {
		t.Fatalf("anchor = %s, want the completion instant %s", got, record.LastCommitAt.UTC())
	}
}

// Explicit mutations have no write-ahead floor marker to match, so their
// user-visible timestamp is chosen at commit creation, after intent creation and
// staging, exactly as it was before external floor anchoring was introduced.
func TestExplicitCommitTimestampIsChosenAtCommitCreation(t *testing.T) {
	h := newHarness(t)
	h.write(".bnotes/recall.json", `{"generation":1}`)

	const step = 45 * time.Second
	startedAt := h.clock
	restore := h.slowClock(step)
	result, err := h.mgr.CommitPaths(context.Background(), "recall-state", []string{".bnotes/recall.json"})
	restore()
	if err != nil || !result.Committed {
		t.Fatalf("the explicit fixture never committed: %+v (%v)", result, err)
	}

	want := startedAt.Add(2 * step) // beginIntent, then commit creation
	if got := h.commitTimes()[0]; !got.Equal(want) {
		t.Fatalf("explicit commit timestamp = %s, want commit-creation reading %s", got, want)
	}
	state := h.mgr.WatchStatus()
	if !state.LastCommitAt.IsZero() || state.ExternalCommitStartedAt != nil {
		t.Fatalf("explicit commit advanced external floor state: %+v", state)
	}
}

// The write-ahead anchor is written by the external scan and by nothing else, so
// the floor is still the watcher's own budget. A person renaming a note, a
// sidecar rewriting its state, and a restore replacing the tree each commit
// immediately and leave both anchors exactly where they were.
func TestExplicitCommitsNeverAnchorTheExternalFloor(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	ctx := context.Background()

	h.write("Inbox/tracked.md", "one\n")
	if _, err := h.mgr.NoteExternalChange(); err != nil {
		t.Fatalf("NoteExternalChange: %v", err)
	}
	h.advance(DebounceQuiet + time.Second)
	if !h.commitPending().Committed {
		t.Fatal("the fixture never took an external snapshot")
	}
	anchored := h.mgr.WatchStatus()
	if anchored.LastCommitAt.IsZero() {
		t.Fatal("a completed external snapshot did not anchor the floor")
	}
	if anchored.ExternalCommitStartedAt != nil {
		t.Fatalf("a completed snapshot left its in-flight anchor behind: %s", anchored.ExternalCommitStartedAt)
	}

	// A journalled note mutation, the CLI and web write path.
	h.advance(time.Minute)
	entry := h.record("bn set", []string{"Inbox/tracked.md"}, func() {
		h.write("Inbox/tracked.md", "two\n")
	})
	if _, err := h.mgr.CommitJournalEntry(ctx, entry.ID, "set"); err != nil {
		t.Fatalf("CommitJournalEntry: %v", err)
	}

	// A durable sidecar write, which has no journal entry behind it.
	h.advance(time.Minute)
	h.write(".bnotes/recall.json", `{"generation":1}`)
	if _, err := h.mgr.CommitPaths(ctx, "recall-state", []string{".bnotes/recall.json"}); err != nil {
		t.Fatalf("CommitPaths: %v", err)
	}

	// And a wholesale restore.
	h.advance(time.Minute)
	h.write("Inbox/restored.md", "from an archive\n")
	if _, err := h.mgr.CommitRestore(ctx, "restore"); err != nil {
		t.Fatalf("CommitRestore: %v", err)
	}

	if got := len(h.subjects()); got != 4 {
		t.Fatalf("the three explicit mutations produced %d commits in total, want four with the snapshot", got)
	}
	after := h.mgr.WatchStatus()
	if !after.LastCommitAt.Equal(anchored.LastCommitAt) {
		t.Fatalf("last_commit_at moved to %s; explicit commits must not anchor the external floor", after.LastCommitAt)
	}
	if after.ExternalCommitStartedAt != nil {
		t.Fatalf("an explicit commit wrote an in-flight external anchor: %s", after.ExternalCommitStartedAt)
	}
}

// An external scan that reaches the repository and records nothing owes the
// floor nothing either: the interval bounds how often history is written, and no
// history was written. The marker it wrote on the way in is retired.
func TestAScanThatRecordsNothingDoesNotSpendTheFloor(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	ctx := context.Background()

	// One note the engine cannot read, and nothing else pending. The scan finds a
	// change, the staging skips it, and the pass commits nothing.
	h.write("Inbox/locked.md", "unreadable\n")
	locked := filepath.Join(h.root, "Inbox", "locked.md")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })

	if _, err := h.mgr.NoteExternalChange(); err != nil {
		t.Fatalf("NoteExternalChange: %v", err)
	}
	h.advance(DebounceQuiet + time.Second)
	result, err := h.mgr.CommitPending(ctx)
	if err != nil {
		t.Fatalf("CommitPending: %v", err)
	}
	if result.Committed {
		t.Fatalf("an unreadable-only scan committed something: %+v", result)
	}
	if got := h.mgr.WatchStatus().ExternalCommitStartedAt; got != nil {
		t.Fatalf("a pass that recorded nothing kept the floor to itself: %s", got)
	}

	// So a readable note written straight afterwards is still governed by
	// readiness alone rather than by an interval nothing earned.
	if err := os.Chmod(locked, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	h.advance(DebounceQuiet + time.Second)
	if !h.mgr.ExternalCommitDue() {
		t.Fatal("a scan that recorded nothing deferred the next real snapshot by a whole interval")
	}
}
