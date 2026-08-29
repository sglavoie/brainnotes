package vaultgit

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	if !contains(h.tracked(), "note.md") {
		t.Fatalf("note.md is not in the history: %v", h.tracked())
	}
}

func TestWatcherRestartsTheQuietPeriodWhenTheFileChangesAgain(t *testing.T) {
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

func TestWatcherBlocksOnUnexplainedDeletionWithoutCommittingIt(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	h.write("kept.md", "kept")
	h.write("vanishes.md", "about to disappear")
	h.commitPending()
	baseline := h.head()

	rec := &watchRecorder{}
	w := h.newWatcher(rec)

	// A deletion nothing in the journal explains: a sync client, a stray `rm`, or
	// the File Provider bug this whole plan exists because of.
	h.remove("vanishes.md")
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)

	event := requireKind(t, rec, WatchEventBlocked)
	if !contains(event.Paths, "vanishes.md") {
		t.Fatalf("the block event does not name the missing path: %+v", event)
	}
	requireNoKind(t, rec, WatchEventCommitted)
	if h.head() != baseline {
		t.Fatal("the watcher committed on top of an unexplained deletion")
	}
	if _, blocked := h.blockRecord(); !blocked {
		t.Fatal("no block marker was latched")
	}

	// The block is announced once, not once per poll: a person who is away from
	// the machine for an hour should come back to one report, not seven hundred.
	before := rec.count(WatchEventBlocked)
	h.advance(time.Minute)
	w.tick(ctx)
	w.tick(ctx)
	if rec.count(WatchEventBlocked) != before {
		t.Fatalf("the block was re-announced on every poll: %d events", rec.count(WatchEventBlocked))
	}
	if !contains(h.tracked(), "vanishes.md") {
		t.Fatal("the deleted note is no longer in the history")
	}
}

func TestWatcherClosesABurstThatResolvesItself(t *testing.T) {
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

func TestWatcherPushesAtStartupAndAtTheInterval(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	remote := t.TempDir()
	rawGit(t, remote, "init", "-q", "--bare")
	rawGit(t, h.root, "remote", "add", "origin", remote)

	h.write("note.md", "content")
	h.commitPending()

	rec := &watchRecorder{}
	w := h.newWatcher(rec)
	w.tick(ctx)
	requireKind(t, rec, WatchEventPushed)

	// Well inside the interval: no second attempt.
	rec.reset()
	h.advance(DefaultPushInterval - time.Second)
	w.tick(ctx)
	requireNoKind(t, rec, WatchEventPushed)

	h.advance(2 * time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventPushed)
}

func TestWatcherKeepsCommittingLocallyWhileTheRemoteIsUnreachable(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	rawGit(t, h.root, "remote", "add", "origin", filepath.Join(t.TempDir(), "no-such-remote"))

	rec := &watchRecorder{}
	w := h.newWatcher(rec)

	h.write("note.md", "written while offline")
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)

	requireKind(t, rec, WatchEventCommitted)
	requireKind(t, rec, WatchEventPushFailed)
	if !contains(h.tracked(), "note.md") {
		t.Fatal("the local snapshot did not survive the failed push")
	}
	if _, blocked := h.blockRecord(); blocked {
		t.Fatal("a push failure latched the block marker; the remote is not a correctness input")
	}

	// Three consecutive failures are announced once, and the announcement is not
	// repeated for the same streak.
	for i := 0; i < 6; i++ {
		h.advance(DefaultPushInterval + pushBackoffMax)
		w.tick(ctx)
	}
	if got := rec.count(WatchEventPushFailing); got != 1 {
		t.Fatalf("expected exactly one streak announcement, got %d in %v", got, rec.kinds())
	}
	event := requireKind(t, rec, WatchEventPushFailing)
	if event.Attempts < pushFailureNotifyAfter {
		t.Fatalf("the streak was announced after %d attempts", event.Attempts)
	}
}

func TestWatcherPushesAutomaticallyOnceTheRemoteComesBack(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	remote := filepath.Join(t.TempDir(), "origin.git")
	rawGit(t, h.root, "remote", "add", "origin", remote)

	rec := &watchRecorder{}
	w := h.newWatcher(rec)
	h.write("note.md", "written during the outage")
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
	requireKind(t, rec, WatchEventPushFailed)

	// The remote comes back. Nothing tells the guard; the next scheduled attempt
	// is what finds out.
	rawGit(t, filepath.Dir(remote), "init", "-q", "--bare", "origin.git")
	rec.reset()
	for i := 0; i < 4 && rec.count(WatchEventPushed) == 0; i++ {
		h.advance(DefaultPushInterval + pushBackoffMax)
		w.tick(ctx)
	}
	requireKind(t, rec, WatchEventPushed)
	if _, ok := h.mgr.PushStatus(); ok {
		t.Fatal("the failure streak survived a successful push")
	}
}

func TestWatcherEventsCarryPathsButNeverNoteBodies(t *testing.T) {
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
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventBlocked)

	sawPath := false
	for _, event := range rec.events {
		rendered := event.Message + "\x00" + event.Detail + "\x00" + strings.Join(event.Paths, "\x00")
		if strings.Contains(rendered, secret) {
			t.Fatalf("a %q event leaked a note body: %+v", event.Kind, event)
		}
		if contains(event.Paths, "private.md") || contains(event.Paths, "doomed.md") {
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
	h := newHarness(t)
	rec := &watchRecorder{}
	w := NewWatcher(h.mgr, WatcherOptions{
		Observe:      rec.observe,
		Now:          func() time.Time { return h.clock },
		PollInterval: time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	time.Sleep(20 * time.Millisecond)
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

// The regression these sessions exist for, end to end. A vault under continuous
// editing used to collect an external snapshot roughly every two minutes — the
// observed history was commits 2, 3, 1, and 4 minutes apart — because readiness
// was the only deadline the loop had.
func TestWatcherDoesNotSnapshotFasterThanTheFloor(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	rec := &watchRecorder{}
	w := h.newWatcher(rec)
	ctx := context.Background()

	// The first snapshot is readiness-only: nothing has anchored a floor yet, so a
	// person who has just started editing is not made to wait five minutes.
	h.write("note.md", "0")
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)

	// Half an hour of an editor autosaving every twenty seconds.
	for elapsed := time.Duration(0); elapsed < 30*time.Minute; elapsed += 20 * time.Second {
		h.advance(20 * time.Second)
		h.write("note.md", strings.Repeat("x", int(elapsed/time.Second)+1))
		w.tick(ctx)
	}

	subjects := h.subjects()
	// One initial commit plus six intervals in thirty minutes. The readiness-only
	// schedule would have produced about fifteen.
	if len(subjects) > 9 {
		t.Fatalf("thirty minutes of continuous editing produced %d commits under a 5m floor", len(subjects))
	}
	if len(subjects) < 5 {
		t.Fatalf("thirty minutes of continuous editing produced only %d commits; the floor is deferring past its own interval", len(subjects))
	}

	// And every gap the watcher chose respects the floor. The commit dates come
	// from the injected clock, so these are the instants the schedule picked.
	times := h.commitTimes()
	for i := 1; i < len(times); i++ {
		if gap := times[i-1].Sub(times[i]); gap < 5*time.Minute {
			t.Fatalf("two external snapshots are %s apart, inside the 5m floor", gap)
		}
	}
}

// Readiness still decides eligibility: inside the floor a ready burst waits, and
// the moment the floor elapses the same burst commits without a further edit.
func TestWatcherCommitsAsSoonAsTheFloorElapses(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	rec := &watchRecorder{}
	w := h.newWatcher(rec)
	ctx := context.Background()

	h.write("note.md", "first")
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
	anchor := h.clock

	// A second, separate edit that goes quiet well inside the floor.
	h.write("second.md", "written a minute later")
	h.advance(time.Minute)
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	rec.reset()
	w.tick(ctx)
	requireNoKind(t, rec, WatchEventCommitted)
	if contains(h.tracked(), "second.md") {
		t.Fatal("a ready burst was snapshotted inside the floor")
	}

	// The published schedule says exactly when it will happen, and nothing has to
	// change on disk for it to.
	state := h.mgr.WatchStatus()
	if state.NextCommitDueAt == nil {
		t.Fatal("a deferred burst publishes no due time")
	}
	if want := anchor.Add(5 * time.Minute); !state.NextCommitDueAt.Equal(want) {
		t.Fatalf("next_commit_due_at = %s, want the floor %s", state.NextCommitDueAt, want)
	}

	h.clock = *state.NextCommitDueAt
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
	if !contains(h.tracked(), "second.md") {
		t.Fatalf("the burst did not commit when its floor elapsed: %v", h.tracked())
	}
}

// The maximum-latency bound and the quiet period are still the two readiness
// deadlines, and the floor composes with whichever of them binds. Under a floor
// shorter than DebounceMaxLatency, a burst that never goes quiet still commits at
// its latency bound.
func TestWatcherStillHonoursMaximumLatencyUnderAFloor(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("1m")
	rec := &watchRecorder{}
	w := h.newWatcher(rec)
	ctx := context.Background()

	// Anchor a floor first, so the readiness-only first-commit case is not what is
	// being measured.
	h.write("seed.md", "seed")
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
	anchor := h.clock

	// Now a burst that never goes quiet. Its readiness is the latency bound at
	// anchor+2m, which is later than its floor at anchor+1m, so the latency bound
	// is what the schedule reports.
	rec.reset()
	var burstStart time.Time
	for i := 1; i <= 18; i++ {
		h.advance(10 * time.Second)
		h.write("busy.md", strings.Repeat("y", i))
		w.tick(ctx)
		if i == 1 {
			burstStart = h.clock
		}
		if _, ok := rec.last(WatchEventCommitted); ok {
			break
		}
	}
	requireKind(t, rec, WatchEventCommitted)

	// Measured from the burst's own first change, which is what readiness is
	// measured from — not from the anchor, which is only what the floor is
	// measured from.
	elapsed := h.clock.Sub(burstStart)
	if elapsed < DebounceMaxLatency {
		t.Fatalf("a continuous burst committed %s after its first change, before its %s latency bound", elapsed, DebounceMaxLatency)
	}
	if elapsed > DebounceMaxLatency+10*time.Second {
		t.Fatalf("a continuous burst committed %s after its first change, past its %s latency bound", elapsed, DebounceMaxLatency)
	}
	// And the floor it cleared along the way was the shorter of the two deadlines,
	// so this test really did measure readiness rather than the floor.
	if h.clock.Sub(anchor) <= time.Minute {
		t.Fatalf("the burst committed %s after the anchor, inside its own 1m floor", h.clock.Sub(anchor))
	}
}

// Rapid bursts separated by idle periods must not accumulate a commit each: the
// floor is measured from the last external snapshot, not from the last quiet
// period, so five bursts inside one interval produce one commit.
func TestWatcherCollapsesRepeatedRapidBurstsIntoOneSnapshot(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	rec := &watchRecorder{}
	w := h.newWatcher(rec)
	ctx := context.Background()

	h.write("note.md", "anchor")
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
	before := len(h.subjects())

	rec.reset()
	// Five separate write-then-go-quiet bursts, each of which the old schedule
	// would have committed on its own.
	for i := 0; i < 5; i++ {
		h.write("note.md", strings.Repeat("z", i+1))
		w.tick(ctx)
		h.advance(DebounceQuiet + time.Second)
		w.tick(ctx)
	}
	if got := rec.count(WatchEventCommitted); got != 0 {
		t.Fatalf("five bursts inside one 5m floor produced %d snapshots", got)
	}

	// Past the floor, the accumulated content lands in a single commit.
	h.advance(5 * time.Minute)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
	if got := len(h.subjects()) - before; got != 1 {
		t.Fatalf("the deferred bursts produced %d commits, want one", got)
	}
}

// An explicit BrainNotes mutation commits immediately and does not touch the
// external floor: it neither waits for it nor resets it. A person renaming a note
// has not asked to be rate limited, and the watcher's own budget is not theirs to
// spend.
func TestExplicitCommitsDoNotMoveTheExternalFloor(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	rec := &watchRecorder{}
	w := h.newWatcher(rec)
	ctx := context.Background()

	h.write("Inbox/tracked.md", "one\n")
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
	anchor := h.mgr.WatchStatus().LastCommitAt
	if anchor.IsZero() {
		t.Fatal("an external snapshot did not anchor the floor")
	}

	// Three explicit mutations, each landing its own commit right away.
	h.advance(time.Minute)
	before := len(h.subjects())
	for i := 0; i < 3; i++ {
		h.advance(10 * time.Second)
		entry := h.record("bn set", []string{"Inbox/tracked.md"}, func() {
			h.write("Inbox/tracked.md", strconv.Itoa(i)+"\n")
		})
		result, err := h.mgr.CommitJournalEntry(ctx, entry.ID, "set")
		if err != nil {
			t.Fatalf("CommitJournalEntry: %v", err)
		}
		if !result.Committed {
			t.Fatalf("an explicit mutation waited for the external floor: %+v", result)
		}
	}
	if got := len(h.subjects()) - before; got != 3 {
		t.Fatalf("three explicit mutations produced %d commits", got)
	}

	// The anchor is untouched, so the next external snapshot is still measured
	// from the watcher's own last one.
	if now := h.mgr.WatchStatus().LastCommitAt; !now.Equal(anchor) {
		t.Fatalf("last_commit_at moved to %s; explicit commits must not anchor the external floor", now)
	}
}

// A poll that loses the safety lock is contention, not a lost schedule. The due
// state is persisted, so the next poll that wins the lock still commits.
func TestWatcherRetriesAfterABusyPoll(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("30s")
	rec := &watchRecorder{}
	w := h.newWatcher(rec)
	ctx := context.Background()

	h.write("note.md", "written while another process is mid-snapshot")
	w.tick(ctx)
	requireKind(t, rec, WatchEventPending)
	h.advance(DebounceQuiet + time.Second)

	// Another BrainNotes process is inside a safety operation for this poll.
	release, err := h.mgr.acquire(safetyLockName, "test")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	rec.reset()
	w.tick(ctx)
	release()

	requireKind(t, rec, WatchEventBusy)
	requireNoKind(t, rec, WatchEventCommitted)
	if _, blocked := h.blockRecord(); blocked {
		t.Fatal("contention latched a block marker")
	}
	if h.head() != "" {
		t.Fatal("a busy poll committed anyway")
	}
	// The burst is still open and still past its deadline: the schedule survived
	// the poll that could not act on it.
	if !h.mgr.ExternalCommitDue() {
		t.Fatal("a busy poll dropped the pending due state")
	}

	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
	if !contains(h.tracked(), "note.md") {
		t.Fatal("the retried snapshot did not record the note")
	}
}

// A guard that dies inside a floor and comes back must resume the same schedule
// rather than starting a fresh one — and must not commit early because its own
// memory of the last snapshot is gone.
func TestWatcherRestartResumesTheFloor(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	ctx := context.Background()

	first := &watchRecorder{}
	w := h.newWatcher(first)
	h.write("note.md", "anchor")
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, first, WatchEventCommitted)
	anchor := h.clock

	// A second edit that goes ready well inside the floor, then the guard dies.
	h.write("second.md", "written inside the floor")
	h.advance(time.Minute)
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)

	h.mgr = h.open(Options{})
	second := &watchRecorder{}
	restarted := h.newWatcher(second)

	restarted.tick(ctx)
	requireNoKind(t, second, WatchEventCommitted)
	if contains(h.tracked(), "second.md") {
		t.Fatal("a restarted guard committed inside the floor it could not remember")
	}

	// The floor is still measured from the pre-restart snapshot.
	h.clock = anchor.Add(5 * time.Minute)
	restarted.tick(ctx)
	requireKind(t, second, WatchEventCommitted)
	if !contains(h.tracked(), "second.md") {
		t.Fatalf("the resumed burst never committed: %v", h.tracked())
	}
}

// The two additive fields are a contract with the health consumer, which reads
// watch.json as flat top-level JSON. Absence is how "no snapshot is scheduled" is
// expressed, so it has to survive a real poll cycle and not just a unit test.
func TestWatcherPublishesTheScheduleForTheHealthConsumer(t *testing.T) {
	h := newHarness(t)
	h.setAutosave("5m")
	rec := &watchRecorder{}
	w := h.newWatcher(rec)
	ctx := context.Background()

	// An idle vault: the policy is published, the schedule is absent.
	w.tick(ctx)
	idle := h.watchJSON()
	if idle["autosave_min_interval"] != "5m" {
		t.Fatalf("autosave_min_interval = %v, want %q", idle["autosave_min_interval"], "5m")
	}
	if _, present := idle["next_commit_due_at"]; present {
		t.Fatalf("an idle vault published a due time: %v", idle["next_commit_due_at"])
	}

	// A pending burst publishes the exact instant the watcher will act on.
	h.write("note.md", "pending")
	w.tick(ctx)
	pending := h.watchJSON()
	due, ok := pending["next_commit_due_at"].(string)
	if !ok {
		t.Fatalf("next_commit_due_at = %v, want an RFC3339 string", pending["next_commit_due_at"])
	}
	parsed, err := time.Parse(time.RFC3339, due)
	if err != nil {
		t.Fatalf("next_commit_due_at %q is not RFC3339: %v", due, err)
	}
	if !parsed.Equal(h.clock.Add(DebounceQuiet)) {
		t.Fatalf("next_commit_due_at = %s, want %s", parsed, h.clock.Add(DebounceQuiet))
	}

	// And a snapshot clears it again.
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
	after := h.watchJSON()
	if _, present := after["next_commit_due_at"]; present {
		t.Fatalf("a committed burst still publishes a due time: %v", after["next_commit_due_at"])
	}
}
