package vaultgit

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Live reload and the off/on transitions.
//
// These drive the real watcher against a real Git repository with an injected
// clock, for the same reason the floor tests do: a policy change is only worth
// having if it changes what the vault's history ends up looking like. The
// configuration itself is injected rather than parsed, because this package
// deliberately does not know what TOML is — what it knows is a fingerprint and a
// resolved policy, and that pair is exactly what a PolicySource hands it.

// newReloadWatcher builds a watcher over the harness manager with a policy
// source attached, sharing the harness clock.
func (h *testHarness) newReloadWatcher(rec *watchRecorder, source PolicySource) *Watcher {
	h.t.Helper()
	return NewWatcher(h.mgr, WatcherOptions{
		Observe: rec.observe,
		Now:     func() time.Time { return h.clock },
		Policy:  source,
	})
}

func requirePolicy(t *testing.T, h *testHarness, want string) {
	t.Helper()
	if got := h.mgr.AutosavePolicy().Canonical; got != want {
		t.Fatalf("the engine is enforcing %q, want %q", got, want)
	}
}

// --- Valid reload -------------------------------------------------------------

// The first poll reads the file the guard already loaded at startup. That is a
// new fingerprint but not a new policy, and a loop that logged every read would
// announce a change on every restart that changed nothing.
func TestWatcherSaysNothingWhenAConfigEditDoesNotChangeThePolicy(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	stub := &stubPolicySource{}
	stub.set(t, "5m")
	rec := &watchRecorder{}
	w := h.newReloadWatcher(rec, stub.source())

	w.tick(context.Background())
	requireNoKind(t, rec, WatchEventConfigChanged)
	requirePolicy(t, h, "5m")

	// A file whose bytes moved but whose meaning did not — a comment, a reordered
	// table, `300s` rewritten as `5m` — is the same non-event.
	stub.snapshot.Hash = "hash-5m-with-a-comment"
	w.tick(context.Background())
	requireNoKind(t, rec, WatchEventConfigChanged)
	requirePolicy(t, h, "5m")
	if stub.calls != 2 {
		t.Fatalf("the source was polled %d times in two ticks", stub.calls)
	}
}

// A shortened floor applies to the burst already waiting on the old one. The
// burst is ready and deferred; the new floor has already elapsed, so the poll
// that reads the change is the poll that commits.
func TestWatcherShorterFloorMakesAReadyBurstDueOnThatPoll(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("10m")
	stub := &stubPolicySource{}
	stub.set(t, "10m")
	rec := &watchRecorder{}
	w := h.newReloadWatcher(rec, stub.source())
	ctx := context.Background()

	// Anchor the floor with a first snapshot, which readiness alone controls.
	h.write("note.md", "anchor")
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
	anchor := h.clock

	// A second burst that goes ready well inside the 10m floor.
	h.write("second.md", "written a minute later")
	h.advance(time.Minute)
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	rec.reset()
	w.tick(ctx)
	requireNoKind(t, rec, WatchEventCommitted)

	// The user shortens the floor to one minute, which the anchor already cleared.
	stub.set(t, "1m")
	w.tick(ctx)
	event := requireKind(t, rec, WatchEventConfigChanged)
	if event.Policy != "1m" {
		t.Fatalf("the change event reports policy %q, want %q", event.Policy, "1m")
	}
	requireKind(t, rec, WatchEventCommitted)
	if !contains(h.tracked(), "second.md") {
		t.Fatalf("the shortened floor did not release the ready burst: %v", h.tracked())
	}
	if h.clock.Sub(anchor) >= 10*time.Minute {
		t.Fatal("the test waited out the old floor rather than measuring the new one")
	}
}

// A lengthened floor postpones the same burst, and the published schedule says
// by how much. Nothing is lost: the burst stays open and commits at the new due
// time.
func TestWatcherLongerFloorPostponesAReadyBurst(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("1m")
	stub := &stubPolicySource{}
	stub.set(t, "1m")
	rec := &watchRecorder{}
	w := h.newReloadWatcher(rec, stub.source())
	ctx := context.Background()

	h.write("note.md", "anchor")
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
	anchor := h.clock

	// Ready at anchor+40s, but the 1m floor has not elapsed.
	h.advance(10 * time.Second)
	h.write("second.md", "written ten seconds later")
	w.tick(ctx)
	h.advance(DebounceQuiet + 5*time.Second)
	rec.reset()

	stub.set(t, "10m")
	w.tick(ctx)
	requireKind(t, rec, WatchEventConfigChanged)
	requireNoKind(t, rec, WatchEventCommitted)

	state := h.mgr.WatchStatus()
	if state.NextCommitDueAt == nil {
		t.Fatal("the postponed burst publishes no due time")
	}
	if want := anchor.Add(10 * time.Minute); !state.NextCommitDueAt.Equal(want) {
		t.Fatalf("next_commit_due_at = %s, want the new floor %s", state.NextCommitDueAt, want)
	}

	// Past the *old* floor and still nothing, which is the whole point.
	h.clock = anchor.Add(2 * time.Minute)
	w.tick(ctx)
	requireNoKind(t, rec, WatchEventCommitted)
	if contains(h.tracked(), "second.md") {
		t.Fatal("the burst committed on the floor the user replaced")
	}

	h.clock = *state.NextCommitDueAt
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
	if !contains(h.tracked(), "second.md") {
		t.Fatalf("the postponed burst never committed: %v", h.tracked())
	}
}

// An ordinary enabled-to-enabled change does not bypass readiness. A burst that
// has not gone quiet yet is still not due, however short the new floor is.
func TestWatcherAShorterFloorStillObeysTheQuietPeriod(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("10m")
	stub := &stubPolicySource{}
	stub.set(t, "10m")
	rec := &watchRecorder{}
	w := h.newReloadWatcher(rec, stub.source())
	ctx := context.Background()

	h.write("note.md", "just typed")
	w.tick(ctx)
	requireKind(t, rec, WatchEventPending)

	h.advance(5 * time.Second)
	stub.set(t, "30s")
	w.tick(ctx)
	requireKind(t, rec, WatchEventConfigChanged)
	requireNoKind(t, rec, WatchEventCommitted)
	if h.head() != "" {
		t.Fatal("a shortened floor committed a burst that had not gone quiet")
	}

	h.advance(DebounceQuiet)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
}

// --- Turning autosave off -----------------------------------------------------

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
	h.advance(time.Second)
	stub.set(t, "off")
	w.tick(ctx)

	requireKind(t, rec, WatchEventBlocked)
	requireNoKind(t, rec, WatchEventConfigChanged)
	requirePolicy(t, h, "5m")
	if h.head() != baseline {
		t.Fatal("the transition committed on top of an unexplained deletion")
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

// A guard that dies mid-transition rebuilds the same intent on its first poll,
// from the one thing that outlives the process that formed it: the policy the
// vault's own watch state says was in force.
//
// The replacement opens its Manager under `off`, because `off` is what its
// config load reads — that is the whole difficulty. The configuration cannot
// distinguish a transition that landed from one that did not, so a restart that
// trusted it alone would answer "already off" and leave the pending edits
// outside the history for good.
func TestWatcherResumesAnOffTransitionAfterARestart(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	stub := &stubPolicySource{}
	stub.set(t, "5m")
	ctx := context.Background()

	first := &watchRecorder{}
	w := h.newReloadWatcher(first, stub.source())
	h.write("note.md", "written just before the guard died")
	w.tick(ctx)
	requireWatchPolicy(t, h, "5m")

	release, err := h.mgr.acquire(safetyLockName, "test")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	stub.set(t, "off")
	w.tick(ctx)
	requireKind(t, first, WatchEventBusy)
	release()

	// The guard dies here, still enforcing 5m with an unlanded transition. Its
	// replacement resolves the working-tree configuration the way cmd/bn-vault-guard
	// does and so opens the Manager under `off`, while the vault's durable record
	// still names the floor the dead process never got to stop enforcing.
	requireWatchPolicy(t, h, "5m")
	h.mgr = h.open(Options{Autosave: autosavePolicy(t, "off")})
	second := &watchRecorder{}
	restarted := h.newReloadWatcher(second, stub.source())

	restarted.tick(ctx)
	requireKind(t, second, WatchEventCommitted)
	requireKind(t, second, WatchEventConfigChanged)
	requirePolicy(t, h, "off")
	requireWatchPolicy(t, h, "off")
	if !contains(h.tracked(), "note.md") {
		t.Fatalf("the restarted guard skipped the final snapshot: %v", h.tracked())
	}
}

// The same restart without a source. The obligation is a property of the vault
// rather than of live reload, so a watcher that will never re-read a
// configuration still discharges it.
func TestWatcherResumesAnOffTransitionWithoutAPolicySource(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	ctx := context.Background()

	h.write("note.md", "written just before the guard died")
	h.newWatcher(&watchRecorder{}).tick(ctx)
	requireWatchPolicy(t, h, "5m")

	h.mgr = h.open(Options{Autosave: autosavePolicy(t, "off")})
	rec := &watchRecorder{}
	restarted := h.newWatcher(rec)
	restarted.tick(ctx)

	requireKind(t, rec, WatchEventCommitted)
	requirePolicy(t, h, "off")
	if !contains(h.tracked(), "note.md") {
		t.Fatalf("the restarted guard skipped the final snapshot: %v", h.tracked())
	}
}

// The other half of the same question, and the one that keeps the fix honest: a
// vault that was genuinely already off takes no snapshot at startup, however
// dirty its working tree is. `off` in the record is published only after the
// owed snapshot has landed, so a record that says `off` says nothing is owed.
func TestWatcherRestartingAnAlreadyOffVaultTakesNoSnapshot(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	stub := &stubPolicySource{}
	stub.set(t, "5m")
	ctx := context.Background()

	// A completed transition, exactly as the live path produces one.
	h.write("note.md", "committed before autosave was turned off")
	w := h.newReloadWatcher(&watchRecorder{}, stub.source())
	w.tick(ctx)
	stub.set(t, "off")
	w.tick(ctx)
	requirePolicy(t, h, "off")
	requireWatchPolicy(t, h, "off")
	landed := h.head()

	// Hours of editing later, the guard restarts under the same off configuration.
	h.advance(3 * time.Hour)
	h.write("written-while-off.md", "an edit the user does not want committed")
	h.mgr = h.open(Options{Autosave: autosavePolicy(t, "off")})
	rec := &watchRecorder{}
	restarted := h.newReloadWatcher(rec, stub.source())

	restarted.tick(ctx)
	restarted.tick(ctx)
	requireNoKind(t, rec, WatchEventCommitted)
	requireNoKind(t, rec, WatchEventConfigChanged)
	requirePolicy(t, h, "off")
	if h.head() != landed {
		t.Fatalf("an already-off vault committed on restart: %v", h.subjects())
	}
	if contains(h.tracked(), "written-while-off.md") {
		t.Fatal("the restart snapshotted a file written while autosave was off")
	}
}

// A vault that has never been watched publishes no policy, and an absent record
// is not evidence of an interrupted transition. Reading it as one would put a
// commit on the first start of every off vault.
func TestWatcherFirstStartUnderOffTakesNoSnapshot(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	h.write("note.md", "written before the guard ever ran")
	h.mgr = h.open(Options{Autosave: autosavePolicy(t, "off")})
	if got := h.mgr.WatchStatus().AutosaveMinInterval; got != "" {
		t.Fatalf("a never-watched vault already publishes policy %q", got)
	}

	rec := &watchRecorder{}
	w := h.newWatcher(rec)
	w.tick(ctx)
	requireNoKind(t, rec, WatchEventCommitted)
	requirePolicy(t, h, "off")
	if h.head() != "" {
		t.Fatalf("the first start of an off vault committed: %v", h.subjects())
	}
}

// The resumed transition is the live one, so it is fail-closed in the same two
// ways. Contention is a retry: the poll that could not take the lock leaves the
// vault enforcing the outgoing floor, and the durable record still says so, so
// even a guard that dies again resumes again.
func TestWatcherResumedOffTransitionRetriesAfterContention(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	ctx := context.Background()

	h.write("note.md", "written just before the guard died")
	h.newWatcher(&watchRecorder{}).tick(ctx)

	h.mgr = h.open(Options{Autosave: autosavePolicy(t, "off")})
	rec := &watchRecorder{}
	restarted := h.newWatcher(rec)

	release, err := h.mgr.acquire(safetyLockName, "test")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	restarted.tick(ctx)
	release()

	requireKind(t, rec, WatchEventBusy)
	requireNoKind(t, rec, WatchEventConfigChanged)
	requirePolicy(t, h, "5m")
	requireWatchPolicy(t, h, "5m")
	if !restarted.pendingOff {
		t.Fatal("contention discarded the resumed switch to off")
	}
	if h.head() != "" {
		t.Fatal("a snapshot landed while the lock was held")
	}

	restarted.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
	requirePolicy(t, h, "off")
	if !contains(h.tracked(), "note.md") {
		t.Fatalf("the retried final snapshot did not record the note: %v", h.tracked())
	}
}

// And a safety failure refuses: a vault that cannot take the snapshot it owes
// does not get to stop taking snapshots, whether the transition was read from
// the file a moment ago or rebuilt from the record at startup.
func TestWatcherResumedOffTransitionStaysOnWhenTheSnapshotIsRefused(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	ctx := context.Background()

	h.write("kept.md", "kept")
	h.write("vanishes.md", "about to disappear")
	h.commitPending()
	baseline := h.head()
	requireWatchPolicy(t, h, "5m")

	// The deletion nothing in the journal explains happens while no guard is
	// running, so the restarted one meets it on the very poll that owes the vault
	// its final snapshot.
	h.remove("vanishes.md")
	h.advance(time.Second)
	h.mgr = h.open(Options{Autosave: autosavePolicy(t, "off")})
	rec := &watchRecorder{}
	restarted := h.newWatcher(rec)
	restarted.tick(ctx)

	requireKind(t, rec, WatchEventBlocked)
	requireNoKind(t, rec, WatchEventConfigChanged)
	requirePolicy(t, h, "5m")
	if h.head() != baseline {
		t.Fatal("the resumed transition committed on top of an unexplained deletion")
	}
	if !contains(h.tracked(), "vanishes.md") {
		t.Fatal("the deleted note is no longer in the history")
	}

	// The record still names the outgoing floor, so the obligation survives this
	// process too rather than being spent by the refusal.
	requireWatchPolicy(t, h, "5m")
	h.mgr = h.open(Options{Autosave: autosavePolicy(t, "off")})
	again := h.newWatcher(&watchRecorder{})
	again.tick(ctx)
	if !again.pendingOff {
		t.Fatal("a second restart no longer owes the vault its final snapshot")
	}
	requirePolicy(t, h, "5m")
}

// A record whose policy token this build cannot read is corruption, and the
// fail-closed reading of it is "the vault was enabled": the owed snapshot is
// taken rather than discarded, and the corrupt token is replaced by one the
// vault's consumers can act on.
func TestWatcherResumesOnACorruptPublishedPolicy(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	ctx := context.Background()

	h.write("note.md", "written just before the guard died")
	h.newWatcher(&watchRecorder{}).tick(ctx)
	h.corruptWatchPolicy("5 minutes please")

	h.mgr = h.open(Options{Autosave: autosavePolicy(t, "off")})
	rec := &watchRecorder{}
	restarted := h.newWatcher(rec)
	restarted.tick(ctx)

	requireKind(t, rec, WatchEventCommitted)
	requirePolicy(t, h, "off")
	requireWatchPolicy(t, h, "off")
	if !contains(h.tracked(), "note.md") {
		t.Fatalf("a corrupt policy token discarded the owed snapshot: %v", h.tracked())
	}
}

// --- While off ----------------------------------------------------------------

// Off stops the guard writing history on its own initiative. It stops nothing
// else: the scan, the deletion check it performs, and the pending event all
// continue, and the schedule stays empty because there is nothing to schedule.
func TestWatcherWhileOffScansAndReportsButDoesNotCommit(t *testing.T) {
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

// The protection that matters most is the one that is not a commit. A note that
// vanishes while autosave is off still latches the block marker, because the
// scan that finds it is the scan that never stopped running.
func TestWatcherWhileOffStillBlocksAnUnexplainedDeletion(t *testing.T) {
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

	event := requireKind(t, rec, WatchEventBlocked)
	if !contains(event.Paths, "vanishes.md") {
		t.Fatalf("the block event does not name the missing path: %+v", event)
	}
	if _, blocked := h.blockRecord(); !blocked {
		t.Fatal("no block marker was latched while off")
	}
	if h.head() != baseline {
		t.Fatal("the history moved while autosave was off")
	}
	if !contains(h.tracked(), "vanishes.md") {
		t.Fatal("the deleted note is no longer in the history")
	}
}

// Pushes are not autosaves. Local history the vault already holds still belongs
// on the remote, so an off vault keeps publishing it.
func TestWatcherWhileOffStillPushes(t *testing.T) {
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
	if !contains(h.tracked(), "note.md") {
		t.Fatalf("the fresh burst never committed: %v", h.tracked())
	}
}

// Re-enabling keeps the previous external snapshot as the floor's anchor, so a
// vault cannot buy itself an extra commit by cycling the policy.
func TestWatcherReenablingKeepsTheFloorAnchor(t *testing.T) {
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
	if contains(h.tracked(), "second.md") {
		t.Fatal("cycling the policy bought a commit inside the floor")
	}

	h.clock = anchor.Add(10 * time.Minute)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
}

// --- Invalid configuration ----------------------------------------------------

// A broken file is reported once per broken version, not once per poll, and the
// last valid policy keeps running the vault the whole time.
func TestWatcherReportsOneDiagnosticPerInvalidConfigVersion(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("30s")
	stub := &stubPolicySource{}
	stub.set(t, "30s")
	rec := &watchRecorder{}
	w := h.newReloadWatcher(rec, stub.source())
	ctx := context.Background()

	w.tick(ctx)
	rec.reset()

	stub.setInvalid("hash-broken-1", "config: autosave.min_interval \"5 minutes\" must be a Go duration")
	for i := 0; i < 5; i++ {
		h.advance(DefaultPollInterval)
		w.tick(ctx)
	}
	if got := rec.count(WatchEventConfigInvalid); got != 1 {
		t.Fatalf("five polls of one broken version produced %d diagnostics", got)
	}
	event := requireKind(t, rec, WatchEventConfigInvalid)
	if event.Policy != "30s" {
		t.Fatalf("the diagnostic reports policy %q, want the retained %q", event.Policy, "30s")
	}
	if event.Detail == "" {
		t.Fatal("the diagnostic carries no detail, so nobody can fix the file")
	}
	requirePolicy(t, h, "30s")

	// Editing a broken file into a differently broken file is a new version and is
	// reported again: the user is iterating and needs to see each answer.
	stub.setInvalid("hash-broken-2", "config: unknown keys: autosave.min_intervl")
	w.tick(ctx)
	if got := rec.count(WatchEventConfigInvalid); got != 2 {
		t.Fatalf("a second broken version produced %d diagnostics in total", got)
	}

	// And a source that cannot read its bytes at all has no fingerprint, so the
	// diagnostic itself stands in for one and is likewise reported once.
	stub.setUnreadable("read brainnotes.toml: permission denied")
	w.tick(ctx)
	w.tick(ctx)
	if got := rec.count(WatchEventConfigInvalid); got != 3 {
		t.Fatalf("an unreadable source produced %d diagnostics in total", got)
	}

	// Fixing the file recovers without a restart, and the recovery is announced.
	stub.set(t, "1m")
	w.tick(ctx)
	recovered := requireKind(t, rec, WatchEventConfigRecovered)
	if recovered.Policy != "1m" {
		t.Fatalf("the recovery event reports policy %q, want %q", recovered.Policy, "1m")
	}
	requireKind(t, rec, WatchEventConfigChanged)
	requirePolicy(t, h, "1m")

	// A valid file that stays valid says nothing further.
	before := rec.count(WatchEventConfigRecovered)
	w.tick(ctx)
	w.tick(ctx)
	if rec.count(WatchEventConfigRecovered) != before {
		t.Fatal("recovery was re-announced on a file that was never broken again")
	}
}

// Reverting a broken edit to the exact bytes that were valid before is a
// recovery even though the fingerprint is one the watcher has already acted on.
func TestWatcherRecoversWhenABrokenEditIsRevertedToTheSameBytes(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("1m")
	stub := &stubPolicySource{}
	stub.set(t, "1m")
	rec := &watchRecorder{}
	w := h.newReloadWatcher(rec, stub.source())
	ctx := context.Background()

	w.tick(ctx)
	valid := stub.snapshot

	stub.setInvalid("hash-broken", "config: autosave.min_interval \"\" requires a value")
	w.tick(ctx)
	requireKind(t, rec, WatchEventConfigInvalid)

	rec.reset()
	stub.snapshot, stub.err = valid, nil
	w.tick(ctx)
	requireKind(t, rec, WatchEventConfigRecovered)
	requireNoKind(t, rec, WatchEventConfigChanged)
	requirePolicy(t, h, "1m")
}

// The invalid file is itself a note-shaped thing in the vault: `brainnotes.toml`
// lives in the working tree, so a hand edit that breaks it is a pending change
// like any other. It must still be fingerprinted, reported, and snapshotted
// under the last valid policy — otherwise the one edit a user is most likely to
// get wrong is the one edit the vault would not preserve.
func TestWatcherSnapshotsAManuallyBrokenConfigUnderTheLastValidPolicy(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("30s")
	rec := &watchRecorder{}
	h.writePolicyFile("30s\n")
	w := h.newReloadWatcher(rec, h.policySource())
	ctx := context.Background()

	// The valid file is committed like anything else in the vault.
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
	if !contains(h.tracked(), policyFile) {
		t.Fatalf("the configuration file is not in the history: %v", h.tracked())
	}

	rec.reset()
	h.advance(time.Second)
	h.writePolicyFile("5 minutes please\n")
	w.tick(ctx)

	requireKind(t, rec, WatchEventConfigInvalid)
	requireKind(t, rec, WatchEventPending)
	requirePolicy(t, h, "30s")

	// And the broken bytes reach the history on the retained policy's schedule,
	// so the user can recover what they typed.
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	event := requireKind(t, rec, WatchEventCommitted)
	if !contains(event.Paths, policyFile) {
		t.Fatalf("the snapshot does not name the broken configuration: %+v", event)
	}
	if got := rawGit(t, h.root, "show", "HEAD:"+policyFile); !strings.Contains(got, "5 minutes please") {
		t.Fatalf("the committed configuration is %q, not the bytes the user typed", got)
	}

	// Fixing it applies without a restart.
	rec.reset()
	h.advance(time.Second)
	h.writePolicyFile("off\n")
	w.tick(ctx)
	requireKind(t, rec, WatchEventConfigRecovered)
	requireKind(t, rec, WatchEventConfigChanged)
	requirePolicy(t, h, "off")
}

// --- Privacy ------------------------------------------------------------------

// The reload events are subject to the same rule as every other WatchEvent: a
// path, a count, an object name, a closed-vocabulary policy token, or a
// sanitized diagnostic, and never a note's bytes.
func TestWatcherReloadEventsCarryNoNoteContents(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("30s")
	rec := &watchRecorder{}
	ctx := context.Background()

	const secret = "SENSITIVE-BODY-c0ffee-DO-NOT-LOG"
	h.write("private.md", "# heading\n\n"+secret+"\n")
	h.writePolicyFile("30s\n")
	w := h.newReloadWatcher(rec, h.policySource())
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)

	// A broken configuration is the event most likely to quote something back at
	// a person, and its diagnostic must still be one bounded line.
	rec.reset()
	h.advance(time.Second)
	h.writePolicyFile("not a duration\nand a second line\n\x00\x07\n")
	w.tick(ctx)
	invalid := requireKind(t, rec, WatchEventConfigInvalid)
	if strings.ContainsAny(invalid.Detail, "\n\r\x00") {
		t.Fatalf("the diagnostic is not a single sanitized line: %q", invalid.Detail)
	}

	// Then off, then on, so every reload event kind has been produced.
	h.advance(time.Second)
	h.writePolicyFile("off\n")
	w.tick(ctx)
	h.advance(time.Second)
	h.writePolicyFile("1m\n")
	w.tick(ctx)
	requireKind(t, rec, WatchEventConfigRecovered)
	requireKind(t, rec, WatchEventConfigChanged)

	for _, event := range rec.events {
		rendered := event.Message + "\x00" + event.Detail + "\x00" + event.Policy + "\x00" + strings.Join(event.Paths, "\x00")
		if strings.Contains(rendered, secret) {
			t.Fatalf("a %q event leaked a note body: %+v", event.Kind, event)
		}
	}
	for _, event := range rec.events {
		switch event.Kind {
		case WatchEventConfigChanged, WatchEventConfigInvalid, WatchEventConfigRecovered:
			switch event.Policy {
			case "off", "30s", "1m":
			default:
				t.Fatalf("a %q event reports policy %q, which is outside the closed vocabulary", event.Kind, event.Policy)
			}
		}
	}
}

// --- Without a source ---------------------------------------------------------

// A watcher with no PolicySource is the pre-reload watcher, exactly. Every other
// test in this package builds one, so this states the guarantee they rely on.
func TestWatcherWithoutAPolicySourceNeverChangesTheEnforcedPolicy(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	rec := &watchRecorder{}
	w := h.newWatcher(rec)
	ctx := context.Background()

	h.write("note.md", "content")
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
	requireNoKind(t, rec, WatchEventConfigChanged)
	requireNoKind(t, rec, WatchEventConfigInvalid)
	requirePolicy(t, h, "5m")
}
