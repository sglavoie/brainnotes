package noteops

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
)

func TestTrashNoteMovesToTrashAndJournalsMove(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "Folder", "A.md")
	writeTrashTestFile(t, source, "body\n")

	result, err := TrashNote(
		root,
		"Folder/A.md",
		WithTrashClock(func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }),
		WithTrashJournalOptions(journal.WithIDGenerator(func(time.Time) (string, error) { return "entry-trash", nil })),
	)
	if err != nil {
		t.Fatalf("TrashNote: %v", err)
	}
	if result.Destination.RelPath != "Trash/Folder/A.md" {
		t.Fatalf("destination rel = %q, want Trash/Folder/A.md", result.Destination.RelPath)
	}
	assertTrashTestMissing(t, source)
	assertTrashTestFile(t, filepath.Join(root, "Trash", "Folder", "A.md"), "body\n")

	if result.Journal == nil {
		t.Fatal("journal entry = nil, want entry")
	}
	if result.Journal.ID != "entry-trash" {
		t.Fatalf("journal ID = %q, want entry-trash", result.Journal.ID)
	}
	sourceRec := trashRecordForPath(t, *result.Journal, "Folder/A.md")
	destRec := trashRecordForPath(t, *result.Journal, "Trash/Folder/A.md")
	if sourceRec.Before.Kind != journal.KindFile || sourceRec.After.Kind != journal.KindMissing {
		t.Fatalf("source record = %#v, want file -> missing", sourceRec)
	}
	if destRec.Before.Kind != journal.KindMissing || destRec.After.Kind != journal.KindFile {
		t.Fatalf("destination record = %#v, want missing -> file", destRec)
	}
}

func TestTrashNoteCollisionAppendsDeletedTimestampAndShortID(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTrashTestFile(t, filepath.Join(root, "Folder", "A.md"), "new\n")
	writeTrashTestFile(t, filepath.Join(root, "Trash", "Folder", "A.md"), "existing\n")

	result, err := TrashNote(
		root,
		"Folder/A.md",
		WithTrashClock(func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }),
		WithTrashShortIDGenerator(func() (string, error) { return "abc123ef", nil }),
		WithTrashJournalOptions(journal.WithIDGenerator(func(time.Time) (string, error) { return "entry-collision", nil })),
	)
	if err != nil {
		t.Fatalf("TrashNote: %v", err)
	}
	wantRel := "Trash/Folder/A.deleted-20260102T030405Z-abc123ef.md"
	if result.Destination.RelPath != wantRel {
		t.Fatalf("destination rel = %q, want %q", result.Destination.RelPath, wantRel)
	}
	assertTrashTestFile(t, filepath.Join(root, "Trash", "Folder", "A.md"), "existing\n")
	assertTrashTestFile(t, filepath.Join(root, "Trash", "Folder", "A.deleted-20260102T030405Z-abc123ef.md"), "new\n")
}

func TestTrashNoteUndoRestoresSourceAndRemovesTrashDestination(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "Folder", "A.md")
	dest := filepath.Join(root, "Trash", "Folder", "A.md")
	writeTrashTestFile(t, source, "restore me\n")

	result, err := TrashNote(
		root,
		"Folder/A.md",
		WithTrashClock(func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }),
		WithTrashJournalOptions(journal.WithIDGenerator(func(time.Time) (string, error) { return "entry-undo-trash", nil })),
	)
	if err != nil {
		t.Fatalf("TrashNote: %v", err)
	}

	if _, err := journal.Restore(root, JournalRoot(root), result.Journal.ID, journal.RestoreOptions{}); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	assertTrashTestFile(t, source, "restore me\n")
	assertTrashTestMissing(t, dest)
}

func TestTrashNoteRejectsEscapingSourcePath(t *testing.T) {
	t.Parallel()

	_, err := TrashNote(t.TempDir(), "../Outside.md")
	if CodeOf(err) != CodeForbidden {
		t.Fatalf("CodeOf(err) = %q, want %q (%v)", CodeOf(err), CodeForbidden, err)
	}
}

func TestTrashNoteRejectsSymlinkOutsideVault(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	outsideNote := filepath.Join(outside, "Outside.md")
	writeTrashTestFile(t, outsideNote, "outside\n")
	if err := os.Symlink(outsideNote, filepath.Join(root, "Linked.md")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	_, err := TrashNote(root, "Linked.md")
	if !errors.Is(err, ErrOutsideVault) {
		t.Fatalf("TrashNote err = %v, want ErrOutsideVault", err)
	}
	if CodeOf(err) != CodeForbidden {
		t.Fatalf("CodeOf(err) = %q, want %q", CodeOf(err), CodeForbidden)
	}
}

func TestTrashNoteRejectsSymlinkTrashParentOutsideVault(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	writeTrashTestFile(t, filepath.Join(root, "A.md"), "body\n")
	if err := os.Symlink(outside, filepath.Join(root, "Trash")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	_, err := TrashNote(root, "A.md")
	if !errors.Is(err, ErrOutsideVault) {
		t.Fatalf("TrashNote err = %v, want ErrOutsideVault", err)
	}
	if CodeOf(err) != CodeForbidden {
		t.Fatalf("CodeOf(err) = %q, want %q", CodeOf(err), CodeForbidden)
	}
}

func writeTrashTestFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %q: %v", p, err)
	}
}

func assertTrashTestFile(t *testing.T, p, want string) {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %q: %v", p, err)
	}
	if string(data) != want {
		t.Fatalf("%q = %q, want %q", p, string(data), want)
	}
}

func assertTrashTestMissing(t *testing.T, p string) {
	t.Helper()
	_, err := os.Stat(p)
	if !os.IsNotExist(err) {
		t.Fatalf("%q should be missing, stat err=%v", p, err)
	}
}

func trashRecordForPath(t *testing.T, entry journal.Entry, rel string) journal.FileRecord {
	t.Helper()
	for _, rec := range entry.Files {
		if rec.Path == rel {
			return rec
		}
	}
	t.Fatalf("entry missing record for %q: %#v", rel, entry.Files)
	return journal.FileRecord{}
}
