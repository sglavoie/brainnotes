package journal

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// unfinalized builds a recorder that has captured one path and not finalized,
// which is the state a process killed inside a mutation leaves behind.
func unfinalized(t *testing.T) (journalRoot, note string, rec *Recorder) {
	t.Helper()
	vaultRoot := t.TempDir()
	journalRoot = filepath.Join(vaultRoot, ".bnotes", "journal")
	note = filepath.Join(vaultRoot, "A.md")
	writeFile(t, note, "before\n")

	fixed := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	rec, err := NewRecorder(vaultRoot, journalRoot, "bn set A.md status=done",
		WithClock(func() time.Time { return fixed }),
		WithIDGenerator(func(time.Time) (string, error) { return "entry-1", nil }))
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	if err := rec.Capture(note); err != nil {
		t.Fatalf("capture: %v", err)
	}
	return journalRoot, note, rec
}

// One entry identity has three durable states and a caller recovering a dead
// process has to be able to tell them apart from the disk alone, because the
// recorder that knew is gone. Started and Finalized are the two questions that
// separate them, and the test asserts them together at each step: either one
// alone is ambiguous about the state either side of it.
func TestStartedAndFinalizedSeparateTheThreeStatesOfAnEntry(t *testing.T) {
	journalRoot, _, rec := unfinalized(t)

	// Nothing: an identity that was minted and never captured under.
	requireEntryState(t, journalRoot, "entry-unknown", false, false)

	// Started: the snapshots exist and nothing says the operation finished.
	requireEntryState(t, journalRoot, "entry-1", true, false)

	if _, err := rec.Finalize(); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	// Recovery history.
	requireEntryState(t, journalRoot, "entry-1", true, true)
}

func requireEntryState(t *testing.T, journalRoot, id string, wantStarted, wantFinalized bool) {
	t.Helper()
	started, err := Started(journalRoot, id)
	if err != nil {
		t.Fatalf("Started(%s): %v", id, err)
	}
	if started != wantStarted {
		t.Fatalf("Started(%s) = %v, want %v", id, started, wantStarted)
	}
	finalized, err := Finalized(journalRoot, id)
	if err != nil {
		t.Fatalf("Finalized(%s): %v", id, err)
	}
	if finalized != wantFinalized {
		t.Fatalf("Finalized(%s) = %v, want %v", id, finalized, wantFinalized)
	}
}

// A half-built entry is unreadable through LoadEntry and unlistable through
// ListEntries — it has no manifest, which is what both of those read — so
// ReadSnapshot is the only way to get at what the paths held, and the caller
// supplies the path out of its own durable record. That is the whole mechanism a
// rollback over an interrupted mutation rests on.
func TestReadSnapshotReachesIntoAnEntryNothingElseCanRead(t *testing.T) {
	journalRoot, _, _ := unfinalized(t)

	if _, err := LoadEntry(journalRoot, "entry-1"); err == nil {
		t.Fatal("LoadEntry read an entry with no manifest")
	}
	entries, _, err := ListEntries(journalRoot)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("ListEntries reported %d unfinalized entries, want 0", len(entries))
	}

	data, err := ReadSnapshot(journalRoot, "entry-1", "A.md")
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if string(data) != "before\n" {
		t.Fatalf("snapshot holds %q, want the pre-change content", data)
	}

	// A path the entry never captured is fs.ErrNotExist rather than a failure, so
	// a caller restoring a path that used to be absent can tell "nothing was
	// here" from "the disk is damaged".
	if _, err := ReadSnapshot(journalRoot, "entry-1", "Missing.md"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadSnapshot for an uncaptured path = %v, want fs.ErrNotExist", err)
	}
}

// DiscardUnfinalized exists rather than the caller calling os.RemoveAll because
// of the refusal: a finalized entry is what `bn undo` reads and what authorizes
// committing a deletion, and the only thing allowed to remove one is Prune under
// a retention policy.
func TestDiscardUnfinalizedRemovesScratchAndRefusesHistory(t *testing.T) {
	journalRoot, note, rec := unfinalized(t)

	if err := DiscardUnfinalized(journalRoot, "entry-1"); err != nil {
		t.Fatalf("DiscardUnfinalized: %v", err)
	}
	if _, err := os.Stat(filepath.Join(journalRoot, "entry-1")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the entry directory survived: %v", err)
	}
	// Converging on gone, so already gone is success rather than an error: the
	// caller is a recovery pass that may have run before.
	if err := DiscardUnfinalized(journalRoot, "entry-1"); err != nil {
		t.Fatalf("DiscardUnfinalized on an absent entry: %v", err)
	}

	if err := rec.Capture(note); err != nil {
		t.Fatalf("re-capture: %v", err)
	}
	if _, err := rec.Finalize(); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if err := DiscardUnfinalized(journalRoot, "entry-1"); err == nil {
		t.Fatal("DiscardUnfinalized removed recovery history")
	}
	if _, err := LoadEntry(journalRoot, "entry-1"); err != nil {
		t.Fatalf("the refusal did not leave the entry intact: %v", err)
	}
}
