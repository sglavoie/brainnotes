package vaultgit

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

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
	assertClosedWatchSchedule(t, state.LoadWatchState(), time.Time{})

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
	assertClosedWatchSchedule(t, state.LoadWatchState(), watchStart.Add(2*time.Minute))
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
	assertWatchSchedule(t, refreshed, "5m", want)
	// The burst it already held is untouched.
	if !refreshed.FirstChangeAt.Equal(watchStart) || !refreshed.LastChangeAt.Equal(watchStart.Add(10*time.Second)) {
		t.Fatalf("the upgrade rewrote the burst: %+v", refreshed)
	}

	// And the republish is durable rather than in-memory only.
	reloaded := state.LoadWatchState()
	assertWatchSchedule(t, reloaded, "5m", want)
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
	assertIdleWatchJSON(t, state)

	if _, err := state.NoteChange(policy, watchStart); err != nil {
		t.Fatalf("NoteChange: %v", err)
	}
	pending := decodeWatchJSON(t, state)
	assertWatchJSONTime(t, pending, "next_commit_due_at", watchStart.Add(DebounceQuiet))

	// The in-flight anchor is present only while a snapshot is unfinished, and it
	// is written in the same shape as the due time — an absent key rather than a
	// zero instant, so a consumer reading flat top-level keys can tell the two
	// apart without knowing what a Go zero time looks like.
	if _, err := state.BeginExternalCommit(policy, watchStart.Add(time.Minute)); err != nil {
		t.Fatalf("BeginExternalCommit: %v", err)
	}
	inFlight := decodeWatchJSON(t, state)
	assertWatchJSONTime(t, inFlight, "external_commit_started_at", watchStart.Add(time.Minute))
	// And the due time it implies is the floor it anchors, not the readiness the
	// burst would otherwise have had.
	if got := inFlight["next_commit_due_at"]; got != watchStart.Add(6*time.Minute).Format(time.RFC3339) {
		t.Fatalf("next_commit_due_at = %v, want the in-flight anchor plus the 5m floor", got)
	}

	if _, err := state.NoteCommit(policy, watchStart.Add(2*time.Minute)); err != nil {
		t.Fatalf("NoteCommit: %v", err)
	}
	if _, present := decodeWatchJSON(t, state)["external_commit_started_at"]; present {
		t.Fatal("a finished snapshot still publishes an in-flight anchor")
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

func assertClosedWatchSchedule(t *testing.T, state WatchState, lastCommit time.Time) {
	t.Helper()
	if state.NextCommitDueAt != nil {
		t.Fatalf("closed schedule still has a due time: %v", state.NextCommitDueAt)
	}
	if !state.LastCommitAt.Equal(lastCommit) {
		t.Fatalf("last commit = %s, want %s", state.LastCommitAt, lastCommit)
	}
}

func assertWatchSchedule(t *testing.T, state WatchState, interval string, due time.Time) {
	t.Helper()
	if state.AutosaveMinInterval != interval || state.NextCommitDueAt == nil || !state.NextCommitDueAt.Equal(due) {
		t.Fatalf("schedule = %+v, want interval %s due %s", state, interval, due)
	}
}

func assertWatchJSONTime(t *testing.T, fields map[string]any, key string, want time.Time) {
	t.Helper()
	value, ok := fields[key].(string)
	if !ok {
		t.Fatalf("%s = %v, want RFC3339 string", key, fields[key])
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("%s is not RFC3339: %v", key, err)
	}
	if parsed.Location() != time.UTC || !parsed.Equal(want) {
		t.Fatalf("%s = %s, want %s in UTC", key, parsed, want)
	}
}

func assertIdleWatchJSON(t *testing.T, state *safetyState) {
	t.Helper()
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
	if _, present := idle["external_commit_started_at"]; present {
		t.Fatalf("a vault with no snapshot in flight published one: %v", idle["external_commit_started_at"])
	}

}
