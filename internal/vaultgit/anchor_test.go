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
// Written before the attempt, that marker is only a bound on the commit if the
// commit cannot be stamped after it — otherwise a snapshot that took real time
// would subtract its own execution time from the vault's configured interval.
// The end-to-end tests below therefore run under a clock that advances on every
// read, so the anchor, the commit object, and the completion are three distinct
// instants exactly as they are on a real disk.

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
// after the commit the history actually holds.
func TestALostCompletionWriteStillHoldsTheExternalFloor(t *testing.T) {
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
	// writes it makes are the only ones the injection has to count. It runs under
	// a clock that moves on every read, so anchoring the floor, stamping the
	// commit, and recording the completion are three separated instants.
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
	committedAt := requireAnchorCoversTheCommit(t, h)

	lost := h.mgr.WatchStatus()
	if !lost.LastCommitAt.IsZero() {
		t.Fatalf("the injected failure never landed: last_commit_at = %s", lost.LastCommitAt)
	}

	// Four minutes of editing inside the five-minute floor. The burst the failed
	// write left open is already past its own latency bound, so anything short of
	// the floor would commit on the very next poll.
	requireNoSnapshotBefore(t, ctx, h, w, committedAt.Add(5*time.Minute))
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
	committedAt := requireAnchorCoversTheCommit(t, h)

	// The guard dies here and a new one starts on the same vault.
	h.mgr = h.open(Options{})
	if h.mgr.WatchStatus().ExternalCommitStartedAt == nil {
		t.Fatal("the restarted process found no anchor for the snapshot in its history")
	}
	requireNoSnapshotBefore(t, ctx, h, h.newWatcher(&watchRecorder{}), committedAt.Add(5*time.Minute))
}

// requireAnchorCoversTheCommit asserts that the only anchor a lost completion
// write leaves behind is no earlier than the commit it is supposed to bound, and
// reports that commit's own timestamp.
//
// This is the finding stated at the level it was actually wrong at. The floor is
// applied to the anchor, but the interval a person configured is about the
// spacing of *commits*, so an anchor earlier than its own commit hands the next
// snapshot the difference for free — and the difference is exactly how long the
// snapshot took, which is unbounded.
func requireAnchorCoversTheCommit(t *testing.T, h *testHarness) time.Time {
	t.Helper()
	times := h.commitTimes()
	if len(times) == 0 {
		t.Fatal("the fixture left no commit for the anchor to cover")
	}
	committedAt := times[0]
	anchor := h.mgr.WatchStatus().ExternalCommitStartedAt
	if anchor == nil {
		t.Fatal("a completed external snapshot is anchored by nothing on disk")
	}
	if anchor.Before(committedAt) {
		t.Fatalf("the surviving anchor is %s but its commit is dated %s, so the floor is short by %s",
			anchor.UTC(), committedAt, committedAt.Sub(anchor.UTC()))
	}
	return committedAt
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

// Pinning the commit's date to the anchored instant bounds the commit; it does
// not replace the completion anchor. A slow snapshot that finishes normally still
// records when it *finished*, which is later than the commit it wrote, so the
// durable anchor of a healthy vault is unchanged by any of this.
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
	committedAt := h.commitTimes()[0]
	if !record.LastCommitAt.After(committedAt) {
		t.Fatalf("last_commit_at = %s, want the completion instant, which is after the commit it recorded (%s)",
			record.LastCommitAt, committedAt)
	}
	if got := record.externalAnchor(); !got.Equal(record.LastCommitAt.UTC()) {
		t.Fatalf("anchor = %s, want the completion instant %s", got, record.LastCommitAt.UTC())
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
