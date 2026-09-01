package vaultgit

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"
)

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
