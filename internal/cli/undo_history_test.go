package cli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
)

func TestHistoryListsNewestFirst(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")
	if err := os.WriteFile(notePath, []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed note: %v", err)
	}

	makeJournalEntry(t, root, notePath, "entry-old", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), "bn set note.md status=old", "old\n")
	makeJournalEntry(t, root, notePath, "entry-new", time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC), "bn set note.md status=new", "new\n")

	out, err := runCLI(t, "", "history")
	if err != nil {
		t.Fatalf("history: %v\n%s", err, out)
	}

	lines := nonEmptyLines(out)
	if len(lines) != 3 {
		t.Fatalf("history line count = %d, want 3\n%s", len(lines), out)
	}
	if lines[0] != "TIMESTAMP\tID\tCOMMAND" {
		t.Fatalf("history header = %q", lines[0])
	}
	if lines[1] != "2026-01-02T03:04:06Z\tentry-new\tbn set note.md status=new" {
		t.Fatalf("history newest row = %q", lines[1])
	}
	if lines[2] != "2026-01-02T03:04:05Z\tentry-old\tbn set note.md status=old" {
		t.Fatalf("history oldest row = %q", lines[2])
	}
}

func TestUndoDefaultsToMostRecentEntry(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")
	if err := os.WriteFile(notePath, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}

	makeJournalEntry(t, root, notePath, "entry-old", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), "bn set note.md stage=mid", "mid\n")
	makeJournalEntry(t, root, notePath, "entry-new", time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC), "bn set note.md stage=after", "after\n")

	out, err := runCLI(t, "", "undo")
	if err != nil {
		t.Fatalf("undo: %v\n%s", err, out)
	}

	data, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	if string(data) != "mid\n" {
		t.Fatalf("undo restored %q, want mid\\n", string(data))
	}
	if !strings.Contains(out, "undo: entry-new (bn set note.md stage=after)") {
		t.Fatalf("undo output missing entry header:\n%s", out)
	}
}

func TestUndoDryRunPrintsDeterministicPreview(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")
	if err := os.WriteFile(notePath, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}

	makeJournalEntry(t, root, notePath, "entry-dry", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), "bn set note.md status=done", "after\n")

	out, err := runCLI(t, "", "undo", "entry-dry", "--dry-run")
	if err != nil {
		t.Fatalf("undo --dry-run: %v\n%s", err, out)
	}

	after, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	if string(after) != "after\n" {
		t.Fatalf("undo --dry-run modified note: %q", string(after))
	}

	want := strings.Join([]string{
		"undo: entry-dry (bn set note.md status=done)",
		"--- note.md",
		"- state: file",
		"+ state: file",
	}, "\n")
	if strings.TrimSpace(out) != want {
		t.Fatalf("undo --dry-run output mismatch\n got:\n%s\nwant:\n%s", out, want)
	}
}

func TestUndoConflictFailsWithoutForce(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")
	if err := os.WriteFile(notePath, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}

	makeJournalEntry(t, root, notePath, "entry-conflict", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), "bn set note.md status=done", "after\n")
	if err := os.WriteFile(notePath, []byte("external edit\n"), 0o644); err != nil {
		t.Fatalf("write external edit: %v", err)
	}

	_, err := runCLI(t, "", "undo", "entry-conflict")
	if err == nil {
		t.Fatal("undo without --force should fail on conflict")
	}
	if !strings.Contains(err.Error(), "re-run with --force to overwrite current files") {
		t.Fatalf("undo conflict error missing --force hint: %v", err)
	}

	after, readErr := os.ReadFile(notePath)
	if readErr != nil {
		t.Fatalf("read note: %v", readErr)
	}
	if string(after) != "external edit\n" {
		t.Fatalf("undo should not modify conflicted note, got %q", string(after))
	}
}

func TestUndoForceAppliesOnConflict(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")
	if err := os.WriteFile(notePath, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}

	makeJournalEntry(t, root, notePath, "entry-force", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), "bn set note.md status=done", "after\n")
	if err := os.WriteFile(notePath, []byte("external edit\n"), 0o644); err != nil {
		t.Fatalf("write external edit: %v", err)
	}

	out, err := runCLI(t, "", "undo", "entry-force", "--force")
	if err != nil {
		t.Fatalf("undo --force: %v\n%s", err, out)
	}

	after, readErr := os.ReadFile(notePath)
	if readErr != nil {
		t.Fatalf("read note: %v", readErr)
	}
	if string(after) != "before\n" {
		t.Fatalf("undo --force restored %q, want before\\n", string(after))
	}
}

func TestUndoRestoresMultiFileRenamePathAndContent(t *testing.T) {
	root := buildRenameFixture(t)
	oldPath := filepath.Join(root, "Dune.md")
	newPath := filepath.Join(root, "Sand Worms.md")
	reviewPath := filepath.Join(root, "Reviews", "Dune Review.md")
	categorizedPath := filepath.Join(root, "categorized.md")
	parentPath := filepath.Join(root, "parent.md")

	beforeOld := readNote(t, oldPath)
	beforeReview := readNote(t, reviewPath)
	beforeCategorized := readNote(t, categorizedPath)
	beforeParent := readNote(t, parentPath)

	out, err := runCLI(t, "", "rename", "Dune", "Sand Worms")
	if err != nil {
		t.Fatalf("rename: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(newPath); statErr != nil {
		t.Fatalf("renamed path missing before undo: %v", statErr)
	}
	if _, statErr := os.Stat(oldPath); !os.IsNotExist(statErr) {
		t.Fatalf("old path should be missing after rename, stat err=%v", statErr)
	}

	out, err = runCLI(t, "", "undo")
	if err != nil {
		t.Fatalf("undo rename entry: %v\n%s", err, out)
	}
	if !strings.Contains(out, "restored ") {
		t.Fatalf("undo output missing restored summary:\n%s", out)
	}

	if got := readNote(t, oldPath); got != beforeOld {
		t.Fatalf("old path content mismatch after undo:\n--- got ---\n%s--- want ---\n%s", got, beforeOld)
	}
	if _, statErr := os.Stat(newPath); !os.IsNotExist(statErr) {
		t.Fatalf("renamed path should be removed by undo, stat err=%v", statErr)
	}
	if got := readNote(t, reviewPath); got != beforeReview {
		t.Fatalf("review content mismatch after undo:\n--- got ---\n%s--- want ---\n%s", got, beforeReview)
	}
	if got := readNote(t, categorizedPath); got != beforeCategorized {
		t.Fatalf("categorized content mismatch after undo:\n--- got ---\n%s--- want ---\n%s", got, beforeCategorized)
	}
	if got := readNote(t, parentPath); got != beforeParent {
		t.Fatalf("parent content mismatch after undo:\n--- got ---\n%s--- want ---\n%s", got, beforeParent)
	}
}

func TestUndoConflictListsAllPathsAndForceHint(t *testing.T) {
	root := buildSetFixture(t)
	pathA := filepath.Join(root, "a.md")
	pathB := filepath.Join(root, "b.md")
	entry := makeJournalEntryForPaths(
		t,
		root,
		"entry-conflict-many",
		time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		"bn set a.md b.md status=done",
		map[string]string{
			pathA: "after-a\n",
			pathB: "after-b\n",
		},
	)

	if err := os.WriteFile(pathA, []byte("external-a\n"), 0o644); err != nil {
		t.Fatalf("write external-a: %v", err)
	}
	if err := os.WriteFile(pathB, []byte("external-b\n"), 0o644); err != nil {
		t.Fatalf("write external-b: %v", err)
	}

	_, err := runCLI(t, "", "undo", entry.ID)
	if err == nil {
		t.Fatal("undo without --force should fail on multi-path conflict")
	}

	msg := err.Error()
	if !strings.Contains(msg, "restore entry-conflict-many aborted due to conflicts:") {
		t.Fatalf("conflict error missing header: %v", err)
	}
	aIdx := strings.Index(msg, "a.md expected=")
	bIdx := strings.Index(msg, "b.md expected=")
	if aIdx < 0 || bIdx < 0 {
		t.Fatalf("conflict error missing both path details: %v", err)
	}
	if aIdx > bIdx {
		t.Fatalf("conflict paths not rendered in deterministic order: %v", err)
	}
	if !strings.Contains(msg, "re-run with --force to overwrite current files") {
		t.Fatalf("conflict error missing --force guidance: %v", err)
	}

	if got := readNote(t, pathA); got != "external-a\n" {
		t.Fatalf("conflicted pathA should remain unchanged, got %q", got)
	}
	if got := readNote(t, pathB); got != "external-b\n" {
		t.Fatalf("conflicted pathB should remain unchanged, got %q", got)
	}
}

func TestUndoUnreadableSnapshotAbortsBeforeAnyWrite(t *testing.T) {
	root := buildSetFixture(t)
	pathA := filepath.Join(root, "a.md")
	pathB := filepath.Join(root, "b.md")
	entry := makeJournalEntryForPaths(
		t,
		root,
		"entry-partial",
		time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		"bn set a.md b.md status=done",
		map[string]string{
			pathA: "after-a\n",
			pathB: "after-b\n",
		},
	)

	recordB := entryRecordForPath(t, entry, "b.md")
	snapshotB := filepath.Join(
		journalRootForVault(root),
		entry.ID,
		filepath.FromSlash(recordB.Before.Snapshot),
	)
	if err := os.Remove(snapshotB); err != nil {
		t.Fatalf("remove snapshot to induce partial failure: %v", err)
	}

	_, err := runCLI(t, "", "undo", entry.ID)
	if err == nil {
		t.Fatal("undo should fail when a required snapshot is missing")
	}
	if !strings.Contains(err.Error(), "read snapshot") {
		t.Fatalf("partial failure error should mention snapshot read failure: %v", err)
	}
	if !strings.Contains(err.Error(), undoPartialFailureGuidance) {
		t.Fatalf("partial failure error missing recovery guidance: %v", err)
	}

	// Snapshots are read while the transaction is built, so an unreadable one
	// aborts before the first file moves: no path is restored, not even the one
	// sorted ahead of the failing path.
	if got := readNote(t, pathA); got != "after-a\n" {
		t.Fatalf("no path should have been restored, got %q for a.md", got)
	}
	if got := readNote(t, pathB); got != "after-b\n" {
		t.Fatalf("failing path should keep post-mutation content, got %q", got)
	}
}

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

func nonEmptyLines(s string) []string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}
