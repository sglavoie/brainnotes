package vaultgit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

func newState(t *testing.T) *safetyState {
	t.Helper()
	state := newSafetyState(t.TempDir())
	if err := state.ensureDir(); err != nil {
		t.Fatalf("ensureDir: %v", err)
	}
	return state
}

func TestBlockRoundTrip(t *testing.T) {
	state := newState(t)
	if _, blocked, err := state.LoadBlock(); blocked || err != nil {
		t.Fatalf("a fresh vault must not be blocked: blocked=%v err=%v", blocked, err)
	}
	record := BlockRecord{
		Kind:       KindUnexplainedDeletion,
		Reason:     "a tracked path vanished",
		Operation:  "commit-pending",
		ObservedAt: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
		Paths:      []string{"Inbox/gone.md"},
		Recovery:   deletionRecovery([]string{"Inbox/gone.md"}),
	}
	if err := state.SaveBlock(record); err != nil {
		t.Fatalf("SaveBlock: %v", err)
	}
	loaded, blocked, err := state.LoadBlock()
	if err != nil || !blocked {
		t.Fatalf("LoadBlock after save: blocked=%v err=%v", blocked, err)
	}
	if loaded.Kind != record.Kind || loaded.Reason != record.Reason || len(loaded.Paths) != 1 {
		t.Fatalf("round trip lost data: %+v", loaded)
	}
	if loaded.Version != stateVersion {
		t.Fatalf("version = %d, want %d", loaded.Version, stateVersion)
	}
	if err := state.ClearBlock(); err != nil {
		t.Fatalf("ClearBlock: %v", err)
	}
	if _, blocked, _ := state.LoadBlock(); blocked {
		t.Fatal("ClearBlock must remove the marker")
	}
}

// Truncating or corrupting the marker must not be a way to re-enable mutations.
// A crash mid-write, a full disk, and a careless shell redirect all produce this
// state, and every one of them means "something went wrong", not "all clear".
func TestCorruptBlockMarkerStaysBlocked(t *testing.T) {
	state := newState(t)
	if err := os.WriteFile(state.path(blockedFileName), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt marker: %v", err)
	}
	record, blocked, err := state.LoadBlock()
	if !blocked {
		t.Fatal("a corrupt marker must still block")
	}
	if err == nil {
		t.Fatal("a corrupt marker must report why it could not be read")
	}
	if record.Kind != KindBlocked || record.Reason == "" || len(record.Recovery) == 0 {
		t.Fatalf("a corrupt marker must still explain itself: %+v", record)
	}
}

func TestEmptyBlockMarkerStaysBlocked(t *testing.T) {
	state := newState(t)
	if err := os.WriteFile(state.path(blockedFileName), nil, 0o644); err != nil {
		t.Fatalf("write empty marker: %v", err)
	}
	if _, blocked, _ := state.LoadBlock(); !blocked {
		t.Fatal("an empty marker must still block")
	}
}

func TestUnknownVersionBlockMarkerStaysBlocked(t *testing.T) {
	state := newState(t)
	if err := os.WriteFile(state.path(blockedFileName), []byte(`{"version":99,"reason":"future"}`), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	record, blocked, err := state.LoadBlock()
	if !blocked || err != nil {
		t.Fatalf("a newer marker must block cleanly: blocked=%v err=%v", blocked, err)
	}
	if record.Kind != KindBlocked {
		t.Fatalf("kind = %q, want %q", record.Kind, KindBlocked)
	}
}

// The state directory lives inside .git, which is what keeps the marker from
// becoming a pending change the engine would then try to commit.
func TestStateDirIsInsideGitDir(t *testing.T) {
	gitDir := t.TempDir()
	state := newSafetyState(gitDir)
	if filepath.Dir(state.Dir()) != gitDir {
		t.Fatalf("state dir %q is not directly inside %q", state.Dir(), gitDir)
	}
}

func TestPushFailureBackoff(t *testing.T) {
	state := newState(t)
	now := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	first, err := state.RecordPushFailure(now, "connection refused")
	if err != nil {
		t.Fatalf("RecordPushFailure: %v", err)
	}
	if first.Attempts != 1 || !first.NextAttemptAfter.Equal(now.Add(30*time.Second)) {
		t.Fatalf("first failure = %+v", first)
	}
	if first.DueAt(now) {
		t.Fatal("a retry must not be due immediately after a failure")
	}
	if !first.DueAt(now.Add(31 * time.Second)) {
		t.Fatal("a retry must be due after the backoff elapses")
	}

	second, err := state.RecordPushFailure(now.Add(time.Minute), "connection refused")
	if err != nil {
		t.Fatalf("RecordPushFailure: %v", err)
	}
	if second.Attempts != 2 || !second.FirstFailureAt.Equal(now) {
		t.Fatalf("second failure = %+v", second)
	}

	if err := state.ClearPushState(); err != nil {
		t.Fatalf("ClearPushState: %v", err)
	}
	if _, ok := state.LoadPushState(); ok {
		t.Fatal("a cleared push state must be gone")
	}
}

// The plan requires an attempt at least every five minutes, so the backoff has
// to stop doubling there rather than growing without bound.
func TestPushBackoffCaps(t *testing.T) {
	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{0, 30 * time.Second},
		{1, 30 * time.Second},
		{2, time.Minute},
		{3, 2 * time.Minute},
		{4, 4 * time.Minute},
		{5, 5 * time.Minute},
		{50, 5 * time.Minute},
	}
	for _, tc := range cases {
		if got := pushBackoff(tc.attempts); got != tc.want {
			t.Fatalf("pushBackoff(%d) = %v, want %v", tc.attempts, got, tc.want)
		}
	}
}

// An unreadable push state is treated as absent, the deliberate inverse of the
// block marker: forgetting a backoff schedule costs one early retry, while
// forgetting a block would cost a note.
func TestCorruptPushStateIsForgotten(t *testing.T) {
	state := newState(t)
	if err := os.WriteFile(state.path(pushStateFileName), []byte("{broken"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, ok := state.LoadPushState(); ok {
		t.Fatal("a corrupt push state must be treated as absent")
	}
}

// testPolicy resolves a floor the way `brainnotes.toml` would.
func testPolicy(t *testing.T, spelling string) config.AutosavePolicy {
	t.Helper()
	policy, err := config.ParseAutosaveMinInterval(spelling)
	if err != nil {
		t.Fatalf("ParseAutosaveMinInterval(%q): %v", spelling, err)
	}
	return policy
}

var watchStart = time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)

func TestWatchDebounce(t *testing.T) {
	state := newState(t)
	// The smallest legal floor, so this test is about readiness and only readiness.
	policy := testPolicy(t, "30s")
	start := watchStart

	if state.LoadWatchState().DueAt(policy, start) {
		t.Fatal("a vault with no open burst is never due")
	}
	if _, err := state.NoteChange(policy, start); err != nil {
		t.Fatalf("NoteChange: %v", err)
	}
	if state.LoadWatchState().DueAt(policy, start.Add(10*time.Second)) {
		t.Fatal("a burst must not commit before the quiet period")
	}
	if !state.LoadWatchState().DueAt(policy, start.Add(DebounceQuiet)) {
		t.Fatal("a burst must commit once the working tree is quiet")
	}

	// An editor that autosaves faster than the quiet period would defer forever
	// without the maximum-latency bound.
	for offset := time.Duration(0); offset < DebounceMaxLatency; offset += 20 * time.Second {
		if _, err := state.NoteChange(policy, start.Add(offset)); err != nil {
			t.Fatalf("NoteChange: %v", err)
		}
	}
	if !state.LoadWatchState().DueAt(policy, start.Add(DebounceMaxLatency)) {
		t.Fatal("a continuously changing burst must still commit at the latency bound")
	}

	if _, err := state.NoteCommit(policy, start.Add(DebounceMaxLatency)); err != nil {
		t.Fatalf("NoteCommit: %v", err)
	}
	if state.LoadWatchState().DueAt(policy, start.Add(time.Hour)) {
		t.Fatal("a committed burst must be closed")
	}
}

// The regression this whole plan exists for. A vault under continuous editing
// used to acquire an external snapshot every two minutes forever, because
// readiness was the only deadline: the observed history was commits 2, 3, 1, and
// 4 minutes apart. Under a five-minute floor the same edit pattern produces one
// commit per five minutes and no more.
func TestExternalAutosavesCannotOutpaceTheFloor(t *testing.T) {
	state := newState(t)
	policy := testPolicy(t, "5m")
	now := watchStart

	// The first snapshot is governed by readiness alone: nothing has anchored a
	// floor yet, so a fresh vault does not wait five minutes for its first commit.
	if _, err := state.NoteChange(policy, now); err != nil {
		t.Fatalf("NoteChange: %v", err)
	}
	if !state.LoadWatchState().DueAt(policy, now.Add(DebounceQuiet)) {
		t.Fatal("the first external snapshot must not wait for a floor nothing anchors")
	}
	firstCommit := now.Add(DebounceQuiet)
	if _, err := state.NoteCommit(policy, firstCommit); err != nil {
		t.Fatalf("NoteCommit: %v", err)
	}

	// Now edit continuously for an hour, exactly as an editor with autosave does,
	// and count how often the schedule says a snapshot may be taken.
	commits := []time.Time{firstCommit}
	for offset := time.Duration(0); offset < time.Hour; offset += 20 * time.Second {
		at := firstCommit.Add(offset)
		if _, err := state.NoteChange(policy, at); err != nil {
			t.Fatalf("NoteChange: %v", err)
		}
		if state.LoadWatchState().DueAt(policy, at) {
			if _, err := state.NoteCommit(policy, at); err != nil {
				t.Fatalf("NoteCommit: %v", err)
			}
			commits = append(commits, at)
		}
	}

	for i := 1; i < len(commits); i++ {
		if gap := commits[i].Sub(commits[i-1]); gap < policy.Interval {
			t.Fatalf("snapshots %d and %d are %s apart, inside the %s floor", i-1, i, gap, policy.Canonical)
		}
	}
	// One hour of continuous editing under a five-minute floor is twelve intervals.
	// The old readiness-only schedule produced one every two minutes, which is what
	// the 2/3/1/4-minute history recorded.
	if len(commits) > 14 {
		t.Fatalf("an hour of continuous editing produced %d snapshots under a %s floor", len(commits), policy.Canonical)
	}
	if len(commits) < 10 {
		t.Fatalf("an hour of continuous editing produced only %d snapshots; the floor is deferring past its own interval", len(commits))
	}
}

// The floor pushes a due time later and never earlier, so an interval shorter
// than the readiness window changes nothing about when a burst commits.
func TestAFloorShorterThanReadinessNeverMovesTheDueTime(t *testing.T) {
	state := newState(t)
	policy := testPolicy(t, "30s")
	now := watchStart

	if _, err := state.NoteCommit(policy, now); err != nil {
		t.Fatalf("NoteCommit: %v", err)
	}
	if _, err := state.NoteChange(policy, now.Add(time.Second)); err != nil {
		t.Fatalf("NoteChange: %v", err)
	}
	due, ok := state.LoadWatchState().DueTime(policy)
	if !ok {
		t.Fatal("an open burst under an enabled policy must have a due time")
	}
	// Readiness is last_change + 30s = t+31s; the floor is last_commit + 30s = t+30s.
	if want := now.Add(time.Second + DebounceQuiet); !due.Equal(want) {
		t.Fatalf("due = %s, want the readiness time %s", due, want)
	}
}

// And an interval longer than the readiness window is what actually binds.
func TestAFloorLongerThanReadinessDefersTheDueTime(t *testing.T) {
	state := newState(t)
	policy := testPolicy(t, "10m")
	now := watchStart

	if _, err := state.NoteCommit(policy, now); err != nil {
		t.Fatalf("NoteCommit: %v", err)
	}
	if _, err := state.NoteChange(policy, now.Add(time.Second)); err != nil {
		t.Fatalf("NoteChange: %v", err)
	}
	loaded := state.LoadWatchState()
	due, ok := loaded.DueTime(policy)
	if !ok {
		t.Fatal("an open burst under an enabled policy must have a due time")
	}
	want := now.Add(10 * time.Minute)
	if !due.Equal(want) {
		t.Fatalf("due = %s, want the floor %s", due, want)
	}
	// The boundary is inclusive on both sides of the comparison the watcher makes.
	if loaded.DueAt(policy, want.Add(-time.Nanosecond)) {
		t.Fatal("a burst is not due one nanosecond before its floor elapses")
	}
	if !loaded.DueAt(policy, want) {
		t.Fatal("a burst is due at the exact instant its floor elapses")
	}
}

// A disabled policy has no schedule at all: nothing is ever due and nothing is
// published for a health consumer to read as a pending snapshot.
func TestADisabledPolicyIsNeverDue(t *testing.T) {
	state := newState(t)
	off := testPolicy(t, config.AutosaveOff)

	if _, err := state.NoteChange(off, watchStart); err != nil {
		t.Fatalf("NoteChange: %v", err)
	}
	loaded := state.LoadWatchState()
	if loaded.DueAt(off, watchStart.Add(24*time.Hour)) {
		t.Fatal("an off policy must never report a due burst")
	}
	if _, ok := loaded.DueTime(off); ok {
		t.Fatal("an off policy must publish no due time")
	}
	if loaded.NextCommitDueAt != nil {
		t.Fatalf("next_commit_due_at = %v under an off policy", loaded.NextCommitDueAt)
	}
	if loaded.AutosaveMinInterval != config.AutosaveOff {
		t.Fatalf("autosave_min_interval = %q, want %q", loaded.AutosaveMinInterval, config.AutosaveOff)
	}
	// The burst timestamps are still recorded, so re-enabling later treats the
	// dirty tree as the burst it already is rather than as a fresh one.
	if loaded.FirstChangeAt.IsZero() {
		t.Fatal("an off policy discarded the burst it was asked to record")
	}
}

// Settling and committing both close the burst, and only one of them moves the
// floor's anchor.
func TestSettlingClosesTheScheduleWithoutMovingTheFloor(t *testing.T) {
	state := newState(t)
	policy := testPolicy(t, "5m")

	if _, err := state.NoteChange(policy, watchStart); err != nil {
		t.Fatalf("NoteChange: %v", err)
	}
	if state.LoadWatchState().NextCommitDueAt == nil {
		t.Fatal("an open burst must publish a due time")
	}
	closed, err := state.NoteSettled(policy)
	if err != nil || !closed {
		t.Fatalf("NoteSettled: closed=%v err=%v", closed, err)
	}
	settled := state.LoadWatchState()
	if settled.NextCommitDueAt != nil {
		t.Fatalf("a settled burst still publishes a due time: %v", settled.NextCommitDueAt)
	}
	if !settled.LastCommitAt.IsZero() {
		t.Fatal("settling a burst claimed a snapshot that was never taken")
	}

	// A second settle on an already-idle vault reports nothing to close, which is
	// what keeps the watcher silent on an idle vault.
	if closed, err := state.NoteSettled(policy); err != nil || closed {
		t.Fatalf("second NoteSettled: closed=%v err=%v", closed, err)
	}

	if _, err := state.NoteChange(policy, watchStart.Add(time.Minute)); err != nil {
		t.Fatalf("NoteChange: %v", err)
	}
	if _, err := state.NoteCommit(policy, watchStart.Add(2*time.Minute)); err != nil {
		t.Fatalf("NoteCommit: %v", err)
	}
	committed := state.LoadWatchState()
	if committed.NextCommitDueAt != nil {
		t.Fatalf("a committed burst still publishes a due time: %v", committed.NextCommitDueAt)
	}
	if !committed.LastCommitAt.Equal(watchStart.Add(2 * time.Minute)) {
		t.Fatalf("last_commit_at = %s, want the commit time", committed.LastCommitAt)
	}
}

// The persisted record is the schedule, so a restart resumes it rather than
// recomputing a fresh one. This is the same guarantee the burst timestamps
// already had, now extended to the floor.
func TestTheFloorSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	policy := testPolicy(t, "5m")

	before := newSafetyState(dir)
	if err := before.ensureDir(); err != nil {
		t.Fatalf("ensureDir: %v", err)
	}
	if _, err := before.NoteCommit(policy, watchStart); err != nil {
		t.Fatalf("NoteCommit: %v", err)
	}
	if _, err := before.NoteChange(policy, watchStart.Add(time.Minute)); err != nil {
		t.Fatalf("NoteChange: %v", err)
	}

	// Everything held in memory is gone; only watch.json survives.
	after := newSafetyState(dir)
	resumed := after.LoadWatchState()
	if resumed.DueAt(policy, watchStart.Add(2*time.Minute)) {
		t.Fatal("a restart forgot the floor and committed inside it")
	}
	due, ok := resumed.DueTime(policy)
	if !ok || !due.Equal(watchStart.Add(5*time.Minute)) {
		t.Fatalf("resumed due = %s (ok=%v), want %s", due, ok, watchStart.Add(5*time.Minute))
	}
	if !resumed.DueAt(policy, watchStart.Add(5*time.Minute)) {
		t.Fatal("the resumed burst never became due")
	}
}

// A version-1 record written before the schedule fields existed must be read,
// not discarded: discarding it would restart a burst that was already past its
// deadline. Reading it republishes the derived fields from the timestamps it
// already carries, and invents nothing else.
func TestAPreSchedulingRecordIsUpgradedInPlace(t *testing.T) {
	state := newState(t)
	legacy := `{
  "version": 1,
  "first_change_at": "2026-08-28T09:00:00Z",
  "last_change_at": "2026-08-28T09:00:10Z",
  "last_commit_at": "2026-08-28T08:58:00Z"
}
`
	if err := os.WriteFile(state.path(watchStateFileName), []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy record: %v", err)
	}

	loaded := state.LoadWatchState()
	if loaded.AutosaveMinInterval != "" || loaded.NextCommitDueAt != nil {
		t.Fatalf("a legacy record decoded with invented fields: %+v", loaded)
	}
	if !loaded.FirstChangeAt.Equal(watchStart) {
		t.Fatalf("a legacy record lost its burst: %+v", loaded)
	}

	policy := testPolicy(t, "5m")
	refreshed, err := state.RefreshWatchState(policy)
	if err != nil {
		t.Fatalf("RefreshWatchState: %v", err)
	}
	if refreshed.AutosaveMinInterval != "5m" {
		t.Fatalf("autosave_min_interval = %q, want %q", refreshed.AutosaveMinInterval, "5m")
	}
	// last_commit_at + 5m beats last_change_at + 30s, so the floor binds.
	want := watchStart.Add(-2 * time.Minute).Add(5 * time.Minute)
	if refreshed.NextCommitDueAt == nil || !refreshed.NextCommitDueAt.Equal(want) {
		t.Fatalf("next_commit_due_at = %v, want %s", refreshed.NextCommitDueAt, want)
	}
	// The burst it already held is untouched.
	if !refreshed.FirstChangeAt.Equal(watchStart) || !refreshed.LastChangeAt.Equal(watchStart.Add(10*time.Second)) {
		t.Fatalf("the upgrade rewrote the burst: %+v", refreshed)
	}

	// And the republish is durable rather than in-memory only.
	reloaded := state.LoadWatchState()
	if reloaded.AutosaveMinInterval != "5m" || reloaded.NextCommitDueAt == nil || !reloaded.NextCommitDueAt.Equal(want) {
		t.Fatalf("the upgraded record was not written to disk: %+v", reloaded)
	}
}

// A change of interval republishes the schedule from the same burst, so the
// health check and the watcher never disagree about when the next snapshot is.
func TestChangingTheIntervalRepublishesTheSchedule(t *testing.T) {
	state := newState(t)
	short := testPolicy(t, "1m")
	long := testPolicy(t, "20m")

	if _, err := state.NoteCommit(short, watchStart); err != nil {
		t.Fatalf("NoteCommit: %v", err)
	}
	if _, err := state.NoteChange(short, watchStart.Add(time.Second)); err != nil {
		t.Fatalf("NoteChange: %v", err)
	}
	first := state.LoadWatchState()
	if first.AutosaveMinInterval != "1m" {
		t.Fatalf("autosave_min_interval = %q, want %q", first.AutosaveMinInterval, "1m")
	}

	if _, err := state.RefreshWatchState(long); err != nil {
		t.Fatalf("RefreshWatchState: %v", err)
	}
	second := state.LoadWatchState()
	if second.AutosaveMinInterval != "20m" {
		t.Fatalf("autosave_min_interval = %q, want %q", second.AutosaveMinInterval, "20m")
	}
	if second.NextCommitDueAt == nil || !second.NextCommitDueAt.Equal(watchStart.Add(20*time.Minute)) {
		t.Fatalf("next_commit_due_at = %v, want %s", second.NextCommitDueAt, watchStart.Add(20*time.Minute))
	}
	if second.DueAt(long, watchStart.Add(10*time.Minute)) {
		t.Fatal("the lengthened floor was not applied")
	}
}

// The serialized shape is a contract with the health consumer, which reads flat
// top-level keys out of watch.json and treats an absent one as "no schedule".
func TestWatchStateSerializationIsStable(t *testing.T) {
	state := newState(t)
	policy := testPolicy(t, "5m")

	if _, err := state.RefreshWatchState(policy); err != nil {
		t.Fatalf("RefreshWatchState: %v", err)
	}
	idle := decodeWatchJSON(t, state)
	if got := idle["version"]; got != float64(stateVersion) {
		t.Fatalf("version = %v, want %d", got, stateVersion)
	}
	if got := idle["autosave_min_interval"]; got != "5m" {
		t.Fatalf("autosave_min_interval = %v, want %q", got, "5m")
	}
	if _, present := idle["next_commit_due_at"]; present {
		t.Fatalf("an idle vault published a due time: %v", idle["next_commit_due_at"])
	}

	if _, err := state.NoteChange(policy, watchStart); err != nil {
		t.Fatalf("NoteChange: %v", err)
	}
	pending := decodeWatchJSON(t, state)
	due, ok := pending["next_commit_due_at"].(string)
	if !ok {
		t.Fatalf("next_commit_due_at = %v, want an RFC3339 string", pending["next_commit_due_at"])
	}
	parsed, err := time.Parse(time.RFC3339, due)
	if err != nil {
		t.Fatalf("next_commit_due_at %q is not RFC3339: %v", due, err)
	}
	if parsed.Location() != time.UTC {
		t.Fatalf("next_commit_due_at %q is not UTC", due)
	}
	if !parsed.Equal(watchStart.Add(DebounceQuiet)) {
		t.Fatalf("next_commit_due_at = %s, want %s", parsed, watchStart.Add(DebounceQuiet))
	}
}

func decodeWatchJSON(t *testing.T, state *safetyState) map[string]any {
	t.Helper()
	data, err := os.ReadFile(state.path(watchStateFileName))
	if err != nil {
		t.Fatalf("read watch.json: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("decode watch.json: %v", err)
	}
	return fields
}

// A caller that names no policy must get the shipped default rather than no
// floor, because "I did not say" is not a request to turn the protection off.
func TestAnUnsetPolicyResolvesToTheVaultDefault(t *testing.T) {
	resolved := resolveAutosavePolicy(config.AutosavePolicy{})
	want := testPolicy(t, config.DefaultAutosaveMinInterval)
	if resolved != want {
		t.Fatalf("the zero policy resolved to %+v, want %+v", resolved, want)
	}
	if resolved.Interval == 0 {
		t.Fatal("the zero policy resolved to no floor at all")
	}

	// A policy carrying only a duration still gets its canonical spelling, so the
	// persisted token is never empty.
	canonical := resolveAutosavePolicy(config.AutosavePolicy{Interval: 300 * time.Second})
	if canonical.Canonical != "5m" {
		t.Fatalf("canonical spelling = %q, want %q", canonical.Canonical, "5m")
	}
}
