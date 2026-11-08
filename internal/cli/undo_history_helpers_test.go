package cli

import (
	"os"
	"sort"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/journal"
)

func makeJournalEntry(t *testing.T, vaultRoot, notePath, id string, ts time.Time, command, after string) {
	t.Helper()
	journalRoot := journalRootForVault(vaultRoot)
	rec, err := journal.NewRecorder(
		vaultRoot,
		journalRoot,
		command,
		journal.WithClock(func() time.Time { return ts }),
		journal.WithIDGenerator(func(time.Time) (string, error) { return id, nil }),
	)
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	if err := rec.Capture(notePath); err != nil {
		t.Fatalf("capture note: %v", err)
	}
	if err := os.WriteFile(notePath, []byte(after), 0o644); err != nil {
		t.Fatalf("write note after state: %v", err)
	}
	entry, err := rec.Finalize()
	if err != nil {
		t.Fatalf("finalize recorder: %v", err)
	}
	if entry.ID != id {
		t.Fatalf("entry ID = %q, want %q", entry.ID, id)
	}
}

func makeJournalEntryForPaths(
	t *testing.T,
	vaultRoot, id string,
	ts time.Time,
	command string,
	afterByPath map[string]string,
) journal.Entry {
	t.Helper()
	journalRoot := journalRootForVault(vaultRoot)
	rec, err := journal.NewRecorder(
		vaultRoot,
		journalRoot,
		command,
		journal.WithClock(func() time.Time { return ts }),
		journal.WithIDGenerator(func(time.Time) (string, error) { return id, nil }),
	)
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}

	paths := make([]string, 0, len(afterByPath))
	for path := range afterByPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := rec.Capture(path); err != nil {
			t.Fatalf("capture %q: %v", path, err)
		}
	}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte(afterByPath[path]), 0o644); err != nil {
			t.Fatalf("write after state %q: %v", path, err)
		}
	}

	entry, err := rec.Finalize()
	if err != nil {
		t.Fatalf("finalize recorder: %v", err)
	}
	if entry.ID != id {
		t.Fatalf("entry ID = %q, want %q", entry.ID, id)
	}
	return *entry
}
