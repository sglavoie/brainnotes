package vaultgit

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

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
	t.Parallel()
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
	if !slices.Contains(h.tracked(), "note.md") {
		t.Fatalf("the restarted guard skipped the final snapshot: %v", h.tracked())
	}
}

// The same restart without a source. The obligation is a property of the vault
// rather than of live reload, so a watcher that will never re-read a
// configuration still discharges it.
func TestWatcherResumesAnOffTransitionWithoutAPolicySource(t *testing.T) {
	t.Parallel()
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
	if !slices.Contains(h.tracked(), "note.md") {
		t.Fatalf("the restarted guard skipped the final snapshot: %v", h.tracked())
	}
}

// The other half of the same question, and the one that keeps the fix honest: a
// vault that was genuinely already off takes no snapshot at startup, however
// dirty its working tree is. `off` in the record is published only after the
// owed snapshot has landed, so a record that says `off` says nothing is owed.
func TestWatcherRestartingAnAlreadyOffVaultTakesNoSnapshot(t *testing.T) {
	t.Parallel()
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
	if slices.Contains(h.tracked(), "written-while-off.md") {
		t.Fatal("the restart snapshotted a file written while autosave was off")
	}
}

// A vault that has never been watched publishes no policy, and an absent record
// is not evidence of an interrupted transition. Reading it as one would put a
// commit on the first start of every off vault.
func TestWatcherFirstStartUnderOffTakesNoSnapshot(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	if !slices.Contains(h.tracked(), "note.md") {
		t.Fatalf("the retried final snapshot did not record the note: %v", h.tracked())
	}
}

// And a safety failure refuses: a vault that cannot take the snapshot it owes
// does not get to stop taking snapshots, whether the transition was read from
// the file a moment ago or rebuilt from the record at startup.
func TestWatcherResumedOffTransitionStaysOnWhenTheSnapshotIsRefused(t *testing.T) {
	t.Parallel()
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
	if err := os.Mkdir(filepath.Join(h.root, "vanishes.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Second)
	h.mgr = h.open(Options{Autosave: autosavePolicy(t, "off")})
	rec := &watchRecorder{}
	restarted := h.newWatcher(rec)
	restarted.tick(ctx)

	requireKind(t, rec, WatchEventBlocked)
	requireNoKind(t, rec, WatchEventConfigChanged)
	requirePolicy(t, h, "5m")
	if h.head() != baseline {
		t.Fatal("the resumed transition committed on top of an ambiguous type change")
	}
	if !slices.Contains(h.tracked(), "vanishes.md") {
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
	t.Parallel()
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
	if !slices.Contains(h.tracked(), "note.md") {
		t.Fatalf("a corrupt policy token discarded the owed snapshot: %v", h.tracked())
	}
}

// --- While off ----------------------------------------------------------------
