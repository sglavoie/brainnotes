package vaultgit

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
	first, err := state.RecordPushFailure(now, "connection refused", false)
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

	second, err := state.RecordPushFailure(now.Add(time.Minute), "connection refused", false)
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
