package journal

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// recordTwoFileEntry journals a rewrite of two notes and returns the entry.
func recordTwoFileEntry(t *testing.T, vaultRoot, journalRoot string) *Entry {
	t.Helper()

	first := filepath.Join(vaultRoot, "First.md")
	second := filepath.Join(vaultRoot, "Second.md")
	writeFile(t, first, "first-before\n")
	writeFile(t, second, "second-before\n")

	rec, err := NewRecorder(
		vaultRoot,
		journalRoot,
		"bn web source save",
		WithClock(func() time.Time { return time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC) }),
		WithIDGenerator(func(time.Time) (string, error) { return "entry-preview", nil }),
	)
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	for _, p := range []string{first, second} {
		if err := rec.Capture(p); err != nil {
			t.Fatalf("capture %q: %v", p, err)
		}
	}
	writeFile(t, first, "first-after\n")
	writeFile(t, second, "second-after\n")

	entry, err := rec.Finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	return entry
}

func previewPaths(conflicts []RestoreConflict) []string {
	paths := make([]string, 0, len(conflicts))
	for _, conflict := range conflicts {
		paths = append(paths, conflict.Path)
	}
	sort.Strings(paths)
	return paths
}

// The point of PreviewRestore: the plan and the conflicts come back together.
// Restore can only ever answer one of the two — without Force a conflict is the
// error and the plan is discarded, with Force the plan comes back and the
// conflicts are dropped — so a surface that has to show somebody what an
// overriding restore would replace has no way to ask Restore for it.
func TestPreviewRestoreReturnsPlanAndConflictsTogether(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	entry := recordTwoFileEntry(t, vaultRoot, journalRoot)

	// One of the two paths moves on; the other is untouched.
	writeFile(t, filepath.Join(vaultRoot, "Second.md"), "second-newer\n")

	plan, conflicts, err := PreviewRestore(vaultRoot, journalRoot, entry.ID)
	if err != nil {
		t.Fatalf("preview restore: %v", err)
	}
	if plan == nil || plan.EntryID != entry.ID || plan.Command != "bn web source save" {
		t.Fatalf("plan = %#v, want the recorded entry", plan)
	}
	if len(plan.Changes) != 2 {
		t.Fatalf("len(changes) = %d, want 2", len(plan.Changes))
	}
	if got := previewPaths(conflicts); len(got) != 1 || got[0] != "Second.md" {
		t.Fatalf("conflicts = %v, want [Second.md]", got)
	}

	// Restore without Force refuses and hands back no plan at all, which is the
	// gap this function exists to close.
	if _, err := Restore(vaultRoot, journalRoot, entry.ID, RestoreOptions{}); err == nil {
		t.Fatal("restore without force: got nil error, want a conflict")
	} else {
		var conflictErr *RestoreConflictError
		if !errors.As(err, &conflictErr) {
			t.Fatalf("restore error = %v, want *RestoreConflictError", err)
		}
	}
}

// Previewing writes nothing: not the vault, not the journal. It is the one
// operation somebody reaches for when they are unsure, so it has to be safe to
// run on a vault they have not decided about yet.
func TestPreviewRestoreWritesNothing(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	entry := recordTwoFileEntry(t, vaultRoot, journalRoot)

	before := readFileString(t, filepath.Join(vaultRoot, "First.md"))
	journalBefore := listDirNames(t, journalRoot)

	if _, _, err := PreviewRestore(vaultRoot, journalRoot, entry.ID); err != nil {
		t.Fatalf("preview restore: %v", err)
	}

	if after := readFileString(t, filepath.Join(vaultRoot, "First.md")); after != before {
		t.Fatalf("preview changed the note: got %q, want %q", after, before)
	}
	if after := listDirNames(t, journalRoot); len(after) != len(journalBefore) {
		t.Fatalf("preview changed the journal: got %v, want %v", after, journalBefore)
	}
}

// A clean vault previews with no conflicts, so a caller can tell "this would
// replace nothing" apart from "this would replace something" before deciding.
func TestPreviewRestoreReportsNoConflictsWhenNothingMoved(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	entry := recordTwoFileEntry(t, vaultRoot, journalRoot)

	plan, conflicts, err := PreviewRestore(vaultRoot, journalRoot, entry.ID)
	if err != nil {
		t.Fatalf("preview restore: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %#v, want none", conflicts)
	}
	if plan == nil || len(plan.Changes) != 2 {
		t.Fatalf("plan = %#v, want two changes", plan)
	}
}

// A pruned or mistyped entry reports as missing rather than as an empty plan,
// so a surface can tell "there is nothing to recover here" from "the thing you
// asked for is gone".
func TestPreviewRestoreMissingEntry(t *testing.T) {
	vaultRoot := t.TempDir()
	journalRoot := filepath.Join(vaultRoot, ".bnotes", "journal")
	recordTwoFileEntry(t, vaultRoot, journalRoot)

	_, _, err := PreviewRestore(vaultRoot, journalRoot, "entry-does-not-exist")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preview missing entry: got %v, want os.ErrNotExist", err)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- test-only path
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return string(data)
}

func listDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %q: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}
