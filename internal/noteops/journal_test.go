package noteops

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
)

func TestJournalRootUsesDefaultJournalDir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	got := JournalRoot(root)
	want := filepath.Join(root, ".bnotes", "journal")
	if got != want {
		t.Fatalf("JournalRoot() = %q, want %q", got, want)
	}
}

func TestJournalIsLazyUntilCapture(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	j, err := BeginOperation(root, "noop", "bn web noop", nil)
	if err != nil {
		t.Fatalf("BeginOperation: %v", err)
	}

	entry, err := j.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if entry != nil {
		t.Fatalf("Finalize entry = %#v, want nil", entry)
	}
	if _, err := os.Stat(JournalRoot(root)); !os.IsNotExist(err) {
		t.Fatalf("journal root stat err = %v, want not exist", err)
	}
}

func TestJournalCaptureAndFinalize(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	note := filepath.Join(root, "A.md")
	if err := os.WriteFile(note, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	fixed := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	j, err := BeginOperation(
		root,
		"trash",
		"bn web trash",
		[]string{note},
		journal.WithClock(func() time.Time { return fixed }),
		journal.WithIDGenerator(func(time.Time) (string, error) { return "entry-1", nil }),
	)
	if err != nil {
		t.Fatalf("BeginOperation: %v", err)
	}

	if err := j.Capture(note); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if err := os.WriteFile(note, []byte("after\n"), 0o644); err != nil {
		t.Fatalf("rewrite note: %v", err)
	}
	entry, err := j.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if entry == nil {
		t.Fatal("Finalize entry = nil, want entry")
	}
	if entry.ID != "entry-1" {
		t.Fatalf("entry ID = %q, want entry-1", entry.ID)
	}
	if entry.Command != "bn web trash" {
		t.Fatalf("entry command = %q", entry.Command)
	}
	if len(entry.Files) != 1 || entry.Files[0].Path != "A.md" {
		t.Fatalf("entry files = %#v, want one A.md record", entry.Files)
	}
}
