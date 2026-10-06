package vaultgit

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The manual case: someone edits the config by hand while the working tree is
// dirty. `off` becomes effective only after one immediate full snapshot, which
// bypasses both the quiet period and the floor.
func TestWatcherTurningOffSnapshotsTheDirtyTreeFirst(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("10m")
	stub := &stubPolicySource{}
	stub.set(t, "10m")
	rec := &watchRecorder{}
	w := h.newReloadWatcher(rec, stub.source())
	ctx := context.Background()

	// Freshly written, nowhere near quiet, and — after the anchor below — well
	// inside the floor too.
	h.write("anchor.md", "anchor")
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)

	rec.reset()
	h.advance(2 * time.Second)
	h.write("unsaved.md", "typed a moment before autosave was turned off")
	stub.set(t, "off")
	w.tick(ctx)

	event := requireKind(t, rec, WatchEventCommitted)
	if !contains(event.Paths, "unsaved.md") {
		t.Fatalf("the final snapshot does not name the pending file: %+v", event)
	}
	if !contains(h.tracked(), "unsaved.md") {
		t.Fatalf("the final snapshot did not reach the history: %v", h.tracked())
	}
	changed := requireKind(t, rec, WatchEventConfigChanged)
	if changed.Policy != "off" {
		t.Fatalf("the change event reports policy %q, want %q", changed.Policy, "off")
	}
	requirePolicy(t, h, "off")

	// And the burst and the schedule are cleared with it.
	state := h.mgr.WatchStatus()
	if !state.FirstChangeAt.IsZero() {
		t.Fatalf("a burst survived the switch to off: %s", state.FirstChangeAt)
	}
	if state.NextCommitDueAt != nil {
		t.Fatalf("off still publishes a due time: %s", state.NextCommitDueAt)
	}
	if h.watchJSON()["autosave_min_interval"] != "off" {
		t.Fatalf("watch.json publishes %v, want %q", h.watchJSON()["autosave_min_interval"], "off")
	}
}

// The CLI case: `bn config set autosave.min_interval off` commits its own edit,
// so the watcher meets a clean working tree. There is nothing to snapshot, so
// the switch is immediate and adds no commit of its own.
func TestWatcherTurningOffOnACleanTreeSwitchesWithoutCommitting(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	stub := &stubPolicySource{}
	stub.set(t, "5m")
	rec := &watchRecorder{}
	w := h.newReloadWatcher(rec, stub.source())
	ctx := context.Background()

	h.write("note.md", "content")
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
	before := len(h.subjects())

	rec.reset()
	stub.set(t, "off")
	w.tick(ctx)
	requireNoKind(t, rec, WatchEventCommitted)
	requireKind(t, rec, WatchEventConfigChanged)
	requirePolicy(t, h, "off")
	if got := len(h.subjects()); got != before {
		t.Fatalf("switching off a clean vault added %d commits", got-before)
	}
}

// Contention is a retry, not a lost transition. The intent lives in the watcher
// and the poll that could not take the lock does not consume it.
func TestWatcherRetriesTheOffTransitionAfterContention(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	stub := &stubPolicySource{}
	stub.set(t, "5m")
	rec := &watchRecorder{}
	w := h.newReloadWatcher(rec, stub.source())
	ctx := context.Background()

	h.write("note.md", "written while another process is mid-snapshot")
	w.tick(ctx)

	release, err := h.mgr.acquire(safetyLockName, "test")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	rec.reset()
	stub.set(t, "off")
	w.tick(ctx)
	release()

	requireKind(t, rec, WatchEventBusy)
	requireNoKind(t, rec, WatchEventConfigChanged)
	requirePolicy(t, h, "5m")
	if !w.pendingOff {
		t.Fatal("contention discarded the pending switch to off")
	}

	// The next poll wins the lock. The source has not been read again in any
	// meaningful sense — its fingerprint is unchanged — so the retry is driven by
	// the surviving intent rather than by re-reading the file.
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
	requireKind(t, rec, WatchEventConfigChanged)
	requirePolicy(t, h, "off")
	if !contains(h.tracked(), "note.md") {
		t.Fatalf("the retried final snapshot did not record the note: %v", h.tracked())
	}
}

// A safety failure is fail-closed: a vault that cannot take its final snapshot
// does not get to stop taking snapshots.
func TestWatcherKeepsAutosaveOnWhenTheFinalSnapshotIsRefused(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	stub := &stubPolicySource{}
	stub.set(t, "5m")
	rec := &watchRecorder{}
	w := h.newReloadWatcher(rec, stub.source())
	ctx := context.Background()

	h.write("kept.md", "kept")
	h.write("vanishes.md", "about to disappear")
	h.commitPending()
	baseline := h.head()

	// A deletion nothing in the journal explains, and then the user turns autosave
	// off. The final snapshot would have to commit that deletion, and it will not.
	h.remove("vanishes.md")
	if err := os.Mkdir(filepath.Join(h.root, "vanishes.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Second)
	stub.set(t, "off")
	w.tick(ctx)

	requireKind(t, rec, WatchEventBlocked)
	requireNoKind(t, rec, WatchEventConfigChanged)
	requirePolicy(t, h, "5m")
	if h.head() != baseline {
		t.Fatal("the transition committed on top of an ambiguous type change")
	}
	if !contains(h.tracked(), "vanishes.md") {
		t.Fatal("the deleted note is no longer in the history")
	}

	// It stays refused for as long as the condition lasts, and it is announced
	// once rather than once per poll.
	before := rec.count(WatchEventBlocked)
	h.advance(time.Minute)
	w.tick(ctx)
	w.tick(ctx)
	if rec.count(WatchEventBlocked) != before {
		t.Fatalf("the refusal was re-announced on every poll: %d events", rec.count(WatchEventBlocked))
	}
	requirePolicy(t, h, "5m")
}

// A transition withdrawn before it lands leaves no trace: `off` was never
// published, so there is nothing to undo.
func TestWatcherWithdrawsAnOffTransitionThatNeverLanded(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	stub := &stubPolicySource{}
	stub.set(t, "5m")
	rec := &watchRecorder{}
	w := h.newReloadWatcher(rec, stub.source())
	ctx := context.Background()

	h.write("note.md", "pending")
	w.tick(ctx)

	// Off is read, but the lock is held so the final snapshot cannot land.
	release, err := h.mgr.acquire(safetyLockName, "test")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	stub.set(t, "off")
	w.tick(ctx)
	release()
	if !w.pendingOff {
		t.Fatal("the transition was not recorded as pending")
	}

	// The user changes their mind before the retry.
	rec.reset()
	stub.set(t, "1m")
	w.tick(ctx)
	if w.pendingOff {
		t.Fatal("the withdrawn transition is still pending")
	}
	requirePolicy(t, h, "1m")
	event := requireKind(t, rec, WatchEventConfigChanged)
	if event.Policy != "1m" {
		t.Fatalf("the change event reports policy %q, want %q", event.Policy, "1m")
	}

	// The burst it would have snapshotted is still the live one, so the vault
	// commits on the ordinary schedule rather than immediately.
	requireNoKind(t, rec, WatchEventCommitted)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
}

// Withdrawing back to the policy that was already in force is still worth one
// line. Nothing about the engine changed, but the pending transition the log
// announced as owed is no longer owed, and a log that never says so leaves a
// reader believing the vault is about to stop autosaving.
func TestWatcherReportsAWithdrawalBackToTheSamePolicy(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	stub := &stubPolicySource{}
	stub.set(t, "5m")
	rec := &watchRecorder{}
	w := h.newReloadWatcher(rec, stub.source())
	ctx := context.Background()

	h.write("note.md", "pending")
	w.tick(ctx)

	release, err := h.mgr.acquire(safetyLockName, "test")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	stub.set(t, "off")
	w.tick(ctx)
	release()

	// The same 5m the vault has been enforcing all along, under fresh bytes.
	rec.reset()
	stub.set(t, "5m")
	stub.snapshot.Hash = "hash-5m-restored"
	w.tick(ctx)

	if w.pendingOff {
		t.Fatal("the withdrawn transition is still pending")
	}
	event := requireKind(t, rec, WatchEventConfigChanged)
	if event.Policy != "5m" {
		t.Fatalf("the withdrawal reports policy %q, want %q", event.Policy, "5m")
	}
	requireNoKind(t, rec, WatchEventCommitted)
	requirePolicy(t, h, "5m")

	// And the burst it would have closed is still the live one, so the vault
	// commits on its own schedule rather than immediately or never.
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
}

// Re-reading `off` under a new fingerprint is not a second transition. An
// already-off vault has nothing to publish, and a loop that announced one on
// every unrelated edit to the file would be noise.
func TestWatcherDoesNotRepeatATransitionThatAlreadyLanded(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	stub := &stubPolicySource{}
	stub.set(t, "5m")
	rec := &watchRecorder{}
	w := h.newReloadWatcher(rec, stub.source())
	ctx := context.Background()

	w.tick(ctx)
	stub.set(t, "off")
	w.tick(ctx)
	requireKind(t, rec, WatchEventConfigChanged)
	requirePolicy(t, h, "off")

	rec.reset()
	stub.set(t, "off")
	stub.snapshot.Hash = "hash-off-with-an-unrelated-edit"
	h.write("note.md", "an edit that arrives while off")
	w.tick(ctx)
	requireNoKind(t, rec, WatchEventConfigChanged)
	requireNoKind(t, rec, WatchEventCommitted)
	if w.pendingOff {
		t.Fatal("an already-off vault recorded a transition to off")
	}
	requirePolicy(t, h, "off")
}
