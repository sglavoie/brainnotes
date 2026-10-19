package journal

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestRecorderCapturesFirstSnapshotAndWritesManifest(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	note := filepath.Join(vaultRoot, "A.md")
	writeFile(t, note, "before\n")

	fixed := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	rec, err := NewRecorder(
		vaultRoot,
		journalRoot,
		"bn set A.md status=done",
		WithClock(func() time.Time { return fixed }),
		WithIDGenerator(func(time.Time) (string, error) { return "entry-1", nil }),
	)
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}

	if err := rec.Capture(note); err != nil {
		t.Fatalf("capture initial: %v", err)
	}
	writeFile(t, note, "after-first\n")
	if err := rec.Capture(note); err != nil {
		t.Fatalf("capture duplicate: %v", err)
	}
	writeFile(t, note, "after-final\n")

	entry, err := rec.Finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}

	if entry.ID != "entry-1" {
		t.Fatalf("entry id = %q, want entry-1", entry.ID)
	}
	if !entry.Timestamp.Equal(fixed) {
		t.Fatalf("timestamp = %s, want %s", entry.Timestamp, fixed)
	}
	if entry.Command != "bn set A.md status=done" {
		t.Fatalf("command = %q", entry.Command)
	}
	if len(entry.Files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(entry.Files))
	}
	recFile := entry.Files[0]
	if recFile.Path != "A.md" {
		t.Fatalf("record path = %q, want A.md", recFile.Path)
	}
	if recFile.Before.Kind != KindFile {
		t.Fatalf("before kind = %q, want file", recFile.Before.Kind)
	}
	if recFile.After.Kind != KindFile {
		t.Fatalf("after kind = %q, want file", recFile.After.Kind)
	}
	if recFile.Before.SHA256 != hashHex("before\n") {
		t.Fatalf("before hash mismatch: %s", recFile.Before.SHA256)
	}
	if recFile.After.SHA256 != hashHex("after-final\n") {
		t.Fatalf("after hash mismatch: %s", recFile.After.SHA256)
	}

	snapPath := filepath.Join(journalRoot, entry.ID, filepath.FromSlash(recFile.Before.Snapshot))
	snapData, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if string(snapData) != "before\n" {
		t.Fatalf("snapshot content = %q, want %q", string(snapData), "before\n")
	}

	loaded, err := LoadEntry(journalRoot, entry.ID)
	if err != nil {
		t.Fatalf("load entry: %v", err)
	}
	if !reflect.DeepEqual(*loaded, *entry) {
		t.Fatalf("loaded entry mismatch\nloaded: %#v\nentry:  %#v", *loaded, *entry)
	}
}

// TestRecorderForgetSinceDropsOnlyLaterCaptures is the batch-item rollback: a
// refused item's captures leave the entry, with their snapshots, and an earlier
// item's stay, so undo neither reverses what lands later at the forgotten paths
// nor loses what the landed item changed.
func TestRecorderForgetSinceDropsOnlyLaterCaptures(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	kept := filepath.Join(vaultRoot, "A.md")
	forgotten := filepath.Join(vaultRoot, "B.md")
	writeFile(t, kept, "a\n")
	writeFile(t, forgotten, "b\n")
	rec, err := NewRecorder(vaultRoot, journalRoot, "bn bulk move",
		WithIDGenerator(func(time.Time) (string, error) { return "entry-forget", nil }))
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	if err := rec.Capture(kept); err != nil {
		t.Fatalf("capture kept: %v", err)
	}
	mark := rec.Mark()
	if err := rec.Capture(forgotten); err != nil {
		t.Fatalf("capture forgotten: %v", err)
	}
	if err := rec.ForgetSince(mark); err != nil {
		t.Fatalf("forget since %d: %v", mark, err)
	}
	snapshot := filepath.Join(journalRoot, "entry-forget", SnapshotsDirName, snapshotFileName("B.md"))
	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatalf("forgotten snapshot stat err = %v, want missing", err)
	}
	entry, err := rec.Finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if len(entry.Files) != 1 || entry.Files[0].Path != "A.md" {
		t.Fatalf("entry files = %+v, want only A.md", entry.Files)
	}
	if err := rec.ForgetSince(0); err == nil {
		t.Fatal("ForgetSince after Finalize succeeded, want refusal")
	}
}
