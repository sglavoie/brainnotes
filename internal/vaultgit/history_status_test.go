package vaultgit

import (
	"context"
	"os"
	"testing"
	"time"
)

// openHistory opens a read-only view of the harness vault.
func openHistory(t *testing.T, h *testHarness) *History {
	t.Helper()
	view, err := OpenHistory(context.Background(), h.root, HistoryOptions{})
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}
	return view
}

// TestHistoryWatchSeesTheSameBurstTheWriterPublished is the distinction a status
// report is built on: edits that exist on disk but not in the content history.
//
// A reader that answered this by scanning the working tree would be racing the
// autosave it is describing — it could see a dirty tree an instant after the
// commit that cleaned it, or a clean one an instant before the next editor
// write. Reading the record the writer publishes means the observer and the
// scheduler cannot disagree about whether a vault is mid-burst.
func TestHistoryWatchSeesTheSameBurstTheWriterPublished(t *testing.T) {
	h, _ := historyFixture(t)
	view := openHistory(t, h)

	if got := view.Watch(); !got.FirstChangeAt.IsZero() {
		t.Fatalf("a settled vault reports an open burst: %+v", got)
	}

	h.write("Outside.md", "an editor saved this behind BrainNotes' back\n")
	if _, err := h.mgr.NoteExternalChange(); err != nil {
		t.Fatalf("NoteExternalChange: %v", err)
	}

	got := view.Watch()
	if got.FirstChangeAt.IsZero() {
		t.Fatal("an open burst is invisible to the reader")
	}
	if want := h.mgr.WatchStatus(); !got.FirstChangeAt.Equal(want.FirstChangeAt) || !got.LastChangeAt.Equal(want.LastChangeAt) {
		t.Fatalf("the reader and the writer disagree: %+v vs %+v", got, want)
	}

	// The view was opened before the burst and reports the burst closing without
	// being reopened, because it holds no snapshot of anything.
	h.advance(h.mgr.AutosavePolicy().Interval * 4)
	h.commitPending()
	if got := view.Watch(); !got.FirstChangeAt.IsZero() {
		t.Fatalf("the burst stayed open after the snapshot: %+v", got)
	}
}

// TestHistoryWatchReportsNothingRatherThanFailing pins the read's one contract
// for a caller that has no way to handle an error: a missing, corrupt, or
// future-versioned record is not a statement that a burst is open, so it reports
// none. The alternative — a status probe that treats an unreadable schedule as
// unsaved work — would cry wolf on every vault that never had a burst.
func TestHistoryWatchReportsNothingRatherThanFailing(t *testing.T) {
	h, _ := historyFixture(t)
	view := openHistory(t, h)

	if err := os.WriteFile(h.mgr.state.path(watchStateFileName), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("corrupt watch.json: %v", err)
	}
	if got := view.Watch(); !got.FirstChangeAt.IsZero() || got.Version != stateVersion {
		t.Fatalf("a corrupt record must read as an empty one: %+v", got)
	}
}

// TestHistoryBlockedSeesTheMarker is why the door exists at all. Asking whether
// a vault's snapshots are refused must not require opening a Manager, because
// opening one resolves a recovery intent — an observer would be mutating the
// thing it was sent to look at.
func TestHistoryBlockedSeesTheMarker(t *testing.T) {
	h, _ := historyFixture(t)
	view := openHistory(t, h)

	record, blocked, err := view.Blocked()
	if blocked || err != nil {
		t.Fatalf("a healthy vault reads as blocked: %+v blocked=%v err=%v", record, blocked, err)
	}

	want := BlockRecord{
		Kind:       KindUnexplainedDeletion,
		Reason:     "a tracked path vanished",
		Operation:  "commit-pending",
		ObservedAt: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
		Paths:      []string{"Inbox/gone.md"},
	}
	if err := h.mgr.state.SaveBlock(want); err != nil {
		t.Fatalf("SaveBlock: %v", err)
	}

	record, blocked, err = view.Blocked()
	if !blocked || err != nil {
		t.Fatalf("the marker is invisible to the reader: blocked=%v err=%v", blocked, err)
	}
	if record.Kind != want.Kind || record.Reason != want.Reason {
		t.Fatalf("the reader lost the explanation: %+v", record)
	}
}

// TestHistoryBlockedFailsClosed carries the marker's own contract across the
// read-only door unchanged: a marker that exists but cannot be read still means
// blocked, and the error is reported rather than swallowed.
//
// Reporting it is the one place this differs from [Manager.Blocked], which drops
// it because a status command printing a refusal already has the record it
// needs. A companion that keeps running has a further decision to make — it must
// treat an unreadable marker as more reason to stop, not less — and it cannot
// make it from a record alone.
func TestHistoryBlockedFailsClosed(t *testing.T) {
	h, _ := historyFixture(t)
	view := openHistory(t, h)

	if err := os.WriteFile(h.mgr.state.path(blockedFileName), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt marker: %v", err)
	}
	record, blocked, err := view.Blocked()
	if !blocked {
		t.Fatal("a corrupt marker must still read as blocked")
	}
	if err == nil {
		t.Fatal("a corrupt marker must report why it could not be read")
	}
	if record.Kind != KindBlocked || len(record.Recovery) == 0 {
		t.Fatalf("a corrupt marker must still explain itself: %+v", record)
	}
}
