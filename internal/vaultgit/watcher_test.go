package vaultgit

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// The watcher tests drive tick() directly against the harness clock rather than
// running Run() against the wall clock. A test that slept for the real 30-second
// quiet period would take longer than the whole package's suite and would still
// be racy; stepping the clock tests the deadline the production code actually
// reads, which is the persisted one.

type watchRecorder struct {
	events []WatchEvent
}

func (r *watchRecorder) observe(event WatchEvent) { r.events = append(r.events, event) }

func (r *watchRecorder) kinds() []WatchEventKind {
	kinds := make([]WatchEventKind, 0, len(r.events))
	for _, event := range r.events {
		kinds = append(kinds, event.Kind)
	}
	return kinds
}

func (r *watchRecorder) last(kind WatchEventKind) (WatchEvent, bool) {
	for i := len(r.events) - 1; i >= 0; i-- {
		if r.events[i].Kind == kind {
			return r.events[i], true
		}
	}
	return WatchEvent{}, false
}

func (r *watchRecorder) count(kind WatchEventKind) int {
	n := 0
	for _, event := range r.events {
		if event.Kind == kind {
			n++
		}
	}
	return n
}

func (r *watchRecorder) reset() { r.events = nil }

// newWatcher builds a watcher over the harness manager sharing the harness
// clock, so advancing the harness advances every deadline at once.
func (h *testHarness) newWatcher(rec *watchRecorder) *Watcher {
	h.t.Helper()
	return NewWatcher(h.mgr, WatcherOptions{
		Observe: rec.observe,
		Now:     func() time.Time { return h.clock },
	})
}

func requireKind(t *testing.T, rec *watchRecorder, kind WatchEventKind) WatchEvent {
	t.Helper()
	event, ok := rec.last(kind)
	if !ok {
		t.Fatalf("expected a %q event, saw %v", kind, rec.kinds())
	}
	return event
}

func requireNoKind(t *testing.T, rec *watchRecorder, kind WatchEventKind) {
	t.Helper()
	if _, ok := rec.last(kind); ok {
		t.Fatalf("did not expect a %q event, saw %v", kind, rec.kinds())
	}
}

func TestWatcherCommitsExternalChangeOnlyAfterTheQuietPeriod(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := &watchRecorder{}
	w := h.newWatcher(rec)
	ctx := context.Background()

	h.write("note.md", "first draft")
	w.tick(ctx)
	requireKind(t, rec, WatchEventPending)
	requireNoKind(t, rec, WatchEventCommitted)
	if h.head() != "" {
		t.Fatalf("a snapshot was taken before the quiet period elapsed")
	}

	// Still dirty but unchanged: the burst must not be extended, or the quiet
	// period could never elapse on a vault nobody is touching.
	h.advance(DebounceQuiet - time.Second)
	rec.reset()
	w.tick(ctx)
	requireNoKind(t, rec, WatchEventPending)
	requireNoKind(t, rec, WatchEventCommitted)

	h.advance(2 * time.Second)
	w.tick(ctx)
	event := requireKind(t, rec, WatchEventCommitted)
	if event.Commit == "" {
		t.Fatal("the committed event carries no commit object name")
	}
	if !slices.Contains(h.tracked(), "note.md") {
		t.Fatalf("note.md is not in the history: %v", h.tracked())
	}
}

func TestWatcherRestartsTheQuietPeriodWhenTheFileChangesAgain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := &watchRecorder{}
	w := h.newWatcher(rec)
	ctx := context.Background()

	h.write("note.md", "first draft")
	w.tick(ctx)

	h.advance(DebounceQuiet - time.Second)
	h.write("note.md", "a second draft, longer than the first")
	w.tick(ctx)
	if rec.count(WatchEventPending) != 2 {
		t.Fatalf("the second write did not reopen the quiet period: %v", rec.kinds())
	}

	h.advance(2 * time.Second)
	w.tick(ctx)
	requireNoKind(t, rec, WatchEventCommitted)

	h.advance(DebounceQuiet)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
}

func TestWatcherCommitsAtMaximumLatencyDespiteContinuousChange(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := &watchRecorder{}
	w := h.newWatcher(rec)
	ctx := context.Background()

	// An editor autosaving faster than the quiet period is exactly the case
	// DebounceMaxLatency exists for: quiet is never reached, so the burst has to
	// be closed by its age instead.
	h.write("note.md", "0")
	w.tick(ctx)
	for i := 1; i <= 12; i++ {
		h.advance(10 * time.Second)
		h.write("note.md", strings.Repeat("x", i))
		w.tick(ctx)
		if _, ok := rec.last(WatchEventCommitted); ok {
			break
		}
	}

	event := requireKind(t, rec, WatchEventCommitted)
	if event.Commit == "" {
		t.Fatal("the committed event carries no commit object name")
	}
	elapsed := h.clock.Sub(time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC))
	if elapsed > DebounceMaxLatency+10*time.Second {
		t.Fatalf("the snapshot took %s, past the %s maximum plus one poll", elapsed, DebounceMaxLatency)
	}
}

func TestWatcherRestartResumesTheBurstWithoutExtendingItsMaximum(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()

	first := &watchRecorder{}
	w := h.newWatcher(first)
	h.write("note.md", "written just before the guard died")
	w.tick(ctx)
	requireKind(t, first, WatchEventPending)

	// The guard dies here. Everything it knew in memory is gone; only watch.json
	// survives. A fresh Manager and a fresh Watcher stand in for the restart.
	h.advance(DebounceMaxLatency - 5*time.Second)
	h.mgr = h.open(Options{})
	second := &watchRecorder{}
	restarted := h.newWatcher(second)

	// The restarted guard has never seen this file, so its first poll legitimately
	// records a change and restarts the *quiet* period. The maximum must survive
	// anyway, because it is measured from the persisted FirstChangeAt.
	restarted.tick(ctx)
	requireNoKind(t, second, WatchEventCommitted)

	h.advance(6 * time.Second)
	restarted.tick(ctx)
	requireKind(t, second, WatchEventCommitted)

	elapsed := h.clock.Sub(time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC))
	if elapsed > DebounceMaxLatency+10*time.Second {
		t.Fatalf("the restart pushed the snapshot to %s, past the %s maximum", elapsed, DebounceMaxLatency)
	}
}

func TestWatcherRecordsExternalDeletionAfterRestart(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	h.write("vanishes.md", "about to disappear")
	h.commitPending()
	baseline := h.head()
	rec := &watchRecorder{}
	w := h.newWatcher(rec)
	h.remove("vanishes.md")
	w.tick(ctx)
	requireKind(t, rec, WatchEventPending)
	requireNoKind(t, rec, WatchEventBlocked)
	if h.head() != baseline {
		t.Fatal("deletion bypassed autosave timing")
	}
	w = h.newWatcher(rec)
	h.advance(DebounceMaxLatency + time.Minute)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
	if slices.Contains(h.tracked(), "vanishes.md") {
		t.Fatal("deleted note is still at HEAD")
	}
	if got := rawGit(t, h.root, "show", baseline+":vanishes.md"); got != "about to disappear" {
		t.Fatal(got)
	}
}

func TestWatcherClosesABurstThatResolvesItself(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := &watchRecorder{}
	w := h.newWatcher(rec)
	ctx := context.Background()

	h.write("scratch.md", "typed by accident")
	w.tick(ctx)
	requireKind(t, rec, WatchEventPending)

	h.remove("scratch.md")
	h.advance(5 * time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventSettled)
	if h.head() != "" {
		t.Fatal("an empty burst produced a commit")
	}

	// A vault that is simply idle says nothing at all.
	rec.reset()
	h.advance(time.Minute)
	w.tick(ctx)
	w.tick(ctx)
	if len(rec.events) != 0 {
		t.Fatalf("an idle vault produced %v", rec.kinds())
	}
}

func TestWatcherEventsCarryPathsButNeverNoteBodies(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	rawGit(t, h.root, "remote", "add", "origin", filepath.Join(t.TempDir(), "no-such-remote"))

	const secret = "SENSITIVE-BODY-c0ffee-DO-NOT-LOG"
	rec := &watchRecorder{}
	w := h.newWatcher(rec)

	h.write("private.md", "# heading\n\n"+secret+"\n")
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)

	// And the same for a block, which is the event most likely to be quoting the
	// vault back at a person.
	h.write("doomed.md", secret)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	h.remove("doomed.md")
	h.write("doomed.md/child.md", "replacement directory")
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventBlocked)

	sawPath := false
	for _, event := range rec.events {
		rendered := event.Message + "\x00" + event.Detail + "\x00" + strings.Join(event.Paths, "\x00")
		if strings.Contains(rendered, secret) {
			t.Fatalf("a %q event leaked a note body: %+v", event.Kind, event)
		}
		if slices.Contains(event.Paths, "private.md") || slices.Contains(event.Paths, "doomed.md") {
			sawPath = true
		}
	}
	if !sawPath {
		t.Fatal("no event named a path; the events are content-free but also useless")
	}

	// The commit messages the watcher produces are generated, so they cannot
	// carry a body either.
	for _, subject := range h.subjects() {
		if strings.Contains(subject, secret) {
			t.Fatalf("a commit subject leaked a note body: %q", subject)
		}
	}

	// Neither can the durable marker a person is told to read and paste into an
	// issue.
	marker, err := os.ReadFile(filepath.Join(h.mgr.StateDir(), blockedFileName))
	if err != nil {
		t.Fatalf("read block marker: %v", err)
	}
	if strings.Contains(string(marker), secret) {
		t.Fatal("blocked.json leaked a note body")
	}
}

func TestWatcherRunStopsOnContextCancellation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := &watchRecorder{}
	// Cancel only once the loop has said it started. A fixed sleep before
	// cancelling races a slow machine: Run can observe the cancelled context
	// before it emits Started, and the assertion below then fails on timing
	// rather than on behaviour. The recorder is read only after Run returns.
	started := make(chan struct{})
	var once sync.Once
	w := NewWatcher(h.mgr, WatcherOptions{
		Observe: func(event WatchEvent) {
			rec.observe(event)
			if event.Kind == WatchEventStarted {
				once.Do(func() { close(started) })
			}
		},
		Now:          func() time.Time { return h.clock },
		PollInterval: time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	select {
	case <-started:
	case err := <-done:
		cancel()
		t.Fatalf("Run returned %v before it started", err)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("Run never emitted Started")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
	requireKind(t, rec, WatchEventStarted)
	requireKind(t, rec, WatchEventStopped)
}

// --- The external autosave floor ---------------------------------------------
//
// Everything below drives the real watcher against a real Git repository with an
// injected clock, because the floor is only worth having if it holds where the
// commits are actually made. The state-level tests in state_test.go pin the
// arithmetic; these pin what the vault's history ends up looking like.
