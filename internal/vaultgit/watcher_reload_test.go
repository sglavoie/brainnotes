package vaultgit

import (
	"context"
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
