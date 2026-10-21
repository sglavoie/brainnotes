package vaultgit

import (
	"context"
	"slices"
	"testing"
	"time"
)

// Off stops the guard writing history on its own initiative. It stops nothing
// else: the scan, the deletion check it performs, and the pending event all
// continue, and the schedule stays empty because there is nothing to schedule.
func TestWatcherWhileOffScansAndReportsButDoesNotCommit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setAutosave("30s")
	stub := &stubPolicySource{}
	stub.set(t, "off")
	rec := &watchRecorder{}
	w := h.newReloadWatcher(rec, stub.source())
	ctx := context.Background()

	w.tick(ctx)
	requirePolicy(t, h, "off")

	rec.reset()
	h.write("note.md", "written while autosave is off")
	w.tick(ctx)
	requireKind(t, rec, WatchEventPending)

	// Long past every deadline an enabled policy would have.
	for i := 0; i < 5; i++ {
		h.advance(DebounceMaxLatency)
		w.tick(ctx)
	}
	requireNoKind(t, rec, WatchEventCommitted)
	if h.head() != "" {
		t.Fatalf("a snapshot was taken while autosave was off: %v", h.tracked())
	}

	// No burst was opened, so nothing is scheduled and the health consumer reads
	// an absent due time rather than one that will never arrive.
	state := h.mgr.WatchStatus()
	if !state.FirstChangeAt.IsZero() {
		t.Fatalf("a burst was opened while off: %s", state.FirstChangeAt)
	}
	if state.NextCommitDueAt != nil {
		t.Fatalf("off published a due time: %s", state.NextCommitDueAt)
	}
	if h.mgr.ExternalCommitDue() {
		t.Fatal("an off vault reports a snapshot as due")
	}
	fields := h.watchJSON()
	if fields["autosave_min_interval"] != "off" {
		t.Fatalf("watch.json publishes %v, want %q", fields["autosave_min_interval"], "off")
	}
	if _, present := fields["next_commit_due_at"]; present {
		t.Fatalf("watch.json publishes a due time while off: %v", fields["next_commit_due_at"])
	}
}

// Deletions remain pending while autosave is off, without blocking mutations.
func TestWatcherWhileOffReportsExternalDeletion(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setAutosave("30s")
	stub := &stubPolicySource{}
	stub.set(t, "off")
	rec := &watchRecorder{}
	w := h.newReloadWatcher(rec, stub.source())
	ctx := context.Background()

	h.write("kept.md", "kept")
	h.write("vanishes.md", "about to disappear")
	h.commitPending()
	baseline := h.head()

	w.tick(ctx)
	requirePolicy(t, h, "off")

	h.remove("vanishes.md")
	h.advance(time.Minute)
	w.tick(ctx)

	event := requireKind(t, rec, WatchEventPending)
	if !slices.Contains(event.Paths, "vanishes.md") {
		t.Fatalf("the block event does not name the missing path: %+v", event)
	}
	if _, blocked := h.blockRecord(); blocked {
		t.Fatal("deletion latched a block while off")
	}
	if h.head() != baseline {
		t.Fatal("the history moved while autosave was off")
	}
	if !slices.Contains(h.tracked(), "vanishes.md") {
		t.Fatal("the deleted note is no longer in the history")
	}
}

// Pushes are not autosaves. Local history the vault already holds still belongs
// on the remote, so an off vault keeps publishing it.
func TestWatcherWhileOffStillPushes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setAutosave("30s")
	remote := t.TempDir()
	rawGit(t, remote, "init", "-q", "--bare")
	rawGit(t, h.root, "remote", "add", "origin", remote)

	h.write("note.md", "committed before autosave was turned off")
	h.commitPending()

	stub := &stubPolicySource{}
	stub.set(t, "off")
	rec := &watchRecorder{}
	w := h.newReloadWatcher(rec, stub.source())
	ctx := context.Background()

	w.tick(ctx)
	requirePolicy(t, h, "off")
	requireKind(t, rec, WatchEventPushed)

	// And it keeps trying on the ordinary interval rather than only once.
	rec.reset()
	h.advance(DefaultPushInterval + time.Second)
	h.write("later.md", "never committed, because autosave is off")
	w.tick(ctx)
	requireKind(t, rec, WatchEventPushed)
	requireNoKind(t, rec, WatchEventCommitted)
}

// Re-enabling treats a working tree that went dirty while off as a fresh burst.
// The alternative would commit instantly on a readiness deadline that expired
// while the user had asked for nothing to be committed at all.
func TestWatcherReenablingOpensAFreshBurst(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setAutosave("30s")
	stub := &stubPolicySource{}
	stub.set(t, "off")
	rec := &watchRecorder{}
	w := h.newReloadWatcher(rec, stub.source())
	ctx := context.Background()

	w.tick(ctx)
	requirePolicy(t, h, "off")

	h.write("note.md", "written hours before autosave came back")
	w.tick(ctx)
	h.advance(2 * time.Hour)
	w.tick(ctx)
	requireNoKind(t, rec, WatchEventCommitted)

	rec.reset()
	stub.set(t, "30s")
	w.tick(ctx)
	event := requireKind(t, rec, WatchEventConfigChanged)
	if event.Policy != "30s" {
		t.Fatalf("the change event reports policy %q, want %q", event.Policy, "30s")
	}
	requireNoKind(t, rec, WatchEventCommitted)
	if h.head() != "" {
		t.Fatal("re-enabling committed a two-hour-old dirty tree on the spot")
	}

	// The burst opened by that poll is a new one, so the quiet period runs from
	// now rather than from whenever the file was written.
	state := h.mgr.WatchStatus()
	if !state.FirstChangeAt.Equal(h.clock) {
		t.Fatalf("the fresh burst began at %s, want %s", state.FirstChangeAt, h.clock)
	}

	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
	if !slices.Contains(h.tracked(), "note.md") {
		t.Fatalf("the fresh burst never committed: %v", h.tracked())
	}
}

// Re-enabling keeps the previous external snapshot as the floor's anchor, so a
// vault cannot buy itself an extra commit by cycling the policy.
func TestWatcherReenablingKeepsTheFloorAnchor(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setAutosave("10m")
	stub := &stubPolicySource{}
	stub.set(t, "10m")
	rec := &watchRecorder{}
	w := h.newReloadWatcher(rec, stub.source())
	ctx := context.Background()

	h.write("note.md", "anchor")
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
	anchor := h.mgr.WatchStatus().LastCommitAt

	stub.set(t, "off")
	w.tick(ctx)
	h.advance(time.Minute)
	stub.set(t, "10m")
	w.tick(ctx)
	requirePolicy(t, h, "10m")

	if now := h.mgr.WatchStatus().LastCommitAt; !now.Equal(anchor) {
		t.Fatalf("last_commit_at moved to %s across an off/on cycle, want %s", now, anchor)
	}

	rec.reset()
	h.write("second.md", "written just after autosave came back")
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireNoKind(t, rec, WatchEventCommitted)
	if slices.Contains(h.tracked(), "second.md") {
		t.Fatal("cycling the policy bought a commit inside the floor")
	}

	h.clock = anchor.Add(10 * time.Minute)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
}

// --- Invalid configuration ----------------------------------------------------
