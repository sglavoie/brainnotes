package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
)

func TestSet_WritesJournalEntryWithPreMutationSnapshot(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")
	before := readNote(t, notePath)

	if _, err := runCLI(t, "", "set", notePath, "status=done"); err != nil {
		t.Fatalf("execute: %v", err)
	}

	entries := loadJournalEntries(t, root)
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	rec := entryRecordForPath(t, entries[0], "note.md")
	if rec.Before.Kind != journal.KindFile {
		t.Fatalf("before kind = %q, want file", rec.Before.Kind)
	}
	if rec.After.Kind != journal.KindFile {
		t.Fatalf("after kind = %q, want file", rec.After.Kind)
	}
	if got := string(loadSnapshot(t, root, entries[0], rec)); got != before {
		t.Fatalf("snapshot mismatch:\n--- got ---\n%s--- want ---\n%s", got, before)
	}
}

func TestSet_DryRunDoesNotWriteJournalEntry(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	if _, err := runCLI(t, "", "set", notePath, "status=done", "--dry-run"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := len(loadJournalEntries(t, root)); got != 0 {
		t.Fatalf("len(entries) = %d, want 0", got)
	}
}

func TestSet_NoOpDoesNotWriteJournalEntry(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")

	if _, err := runCLI(t, "", "set", notePath, "title=Existing"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := len(loadJournalEntries(t, root)); got != 0 {
		t.Fatalf("len(entries) = %d, want 0", got)
	}
}

func TestUnset_WritesJournalEntryWithPreMutationSnapshot(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")
	before := readNote(t, notePath)

	if _, err := runCLI(t, "", "unset", notePath, "tags"); err != nil {
		t.Fatalf("execute: %v", err)
	}

	entries := loadJournalEntries(t, root)
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	rec := entryRecordForPath(t, entries[0], "tags.md")
	if got := string(loadSnapshot(t, root, entries[0], rec)); got != before {
		t.Fatalf("snapshot mismatch:\n--- got ---\n%s--- want ---\n%s", got, before)
	}
}

func TestUnset_DryRunDoesNotWriteJournalEntry(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")

	if _, err := runCLI(t, "", "unset", notePath, "tags", "--dry-run"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := len(loadJournalEntries(t, root)); got != 0 {
		t.Fatalf("len(entries) = %d, want 0", got)
	}
}

func TestReplace_WritesJournalEntryWithPreMutationSnapshot(t *testing.T) {
	root := buildReplaceFixture(t)
	notePath := filepath.Join(root, "Areas", "Alpha.md")
	before := readNote(t, notePath)

	if _, err := runCLI(t, "", "replace", "status", "wip", "shipped", notePath); err != nil {
		t.Fatalf("execute: %v", err)
	}

	entries := loadJournalEntries(t, root)
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	rec := entryRecordForPath(t, entries[0], "Areas/Alpha.md")
	if got := string(loadSnapshot(t, root, entries[0], rec)); got != before {
		t.Fatalf("snapshot mismatch:\n--- got ---\n%s--- want ---\n%s", got, before)
	}
}

func TestReplace_DryRunDoesNotWriteJournalEntry(t *testing.T) {
	root := buildReplaceFixture(t)
	notePath := filepath.Join(root, "Areas", "Alpha.md")

	if _, err := runCLI(t, "", "replace", "status", "wip", "shipped", notePath, "--dry-run"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := len(loadJournalEntries(t, root)); got != 0 {
		t.Fatalf("len(entries) = %d, want 0", got)
	}
}

func TestReplace_NoOpDoesNotWriteJournalEntry(t *testing.T) {
	root := buildReplaceFixture(t)

	if _, err := runCLI(t, "", "replace", "status", "nope", "still-nope"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := len(loadJournalEntries(t, root)); got != 0 {
		t.Fatalf("len(entries) = %d, want 0", got)
	}
}

func TestNew_WritesJournalEntryForCreatedNote(t *testing.T) {
	root := buildNewFixture(t)

	if _, err := runNew(t, "new", "book", "Dune", "--var", "author=Herbert"); err != nil {
		t.Fatalf("execute: %v", err)
	}

	entries := loadJournalEntries(t, root)
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	rec := entryRecordForPath(t, entries[0], "References/Books/Dune.md")
	if rec.Before.Kind != journal.KindMissing {
		t.Fatalf("before kind = %q, want missing", rec.Before.Kind)
	}
	if rec.After.Kind != journal.KindFile {
		t.Fatalf("after kind = %q, want file", rec.After.Kind)
	}
	if rec.Before.Snapshot != "" {
		t.Fatalf("before snapshot = %q, want empty", rec.Before.Snapshot)
	}
}

func TestNewForce_WritesSnapshotBeforeOverwrite(t *testing.T) {
	root := buildNewFixture(t)
	target := filepath.Join(root, "References", "Books", "Dune.md")
	before := "existing\n"
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(target, []byte(before), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := runNew(t, "new", "book", "Dune", "--var", "author=Herbert", "--force"); err != nil {
		t.Fatalf("execute: %v", err)
	}

	entries := loadJournalEntries(t, root)
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	rec := entryRecordForPath(t, entries[0], "References/Books/Dune.md")
	if rec.Before.Kind != journal.KindFile {
		t.Fatalf("before kind = %q, want file", rec.Before.Kind)
	}
	if got := string(loadSnapshot(t, root, entries[0], rec)); got != before {
		t.Fatalf("snapshot mismatch:\n--- got ---\n%s--- want ---\n%s", got, before)
	}
}

func TestRename_WritesJournalEntryForMoveAndRewrites(t *testing.T) {
	root := buildRenameFixture(t)
	before := readNote(t, filepath.Join(root, "Dune.md"))

	if _, err := runCLI(t, "", "rename", "Dune", "Sand Worms"); err != nil {
		t.Fatalf("execute: %v", err)
	}

	entries := loadJournalEntries(t, root)
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	oldRec := entryRecordForPath(t, entries[0], "Dune.md")
	if oldRec.Before.Kind != journal.KindFile || oldRec.After.Kind != journal.KindMissing {
		t.Fatalf("old path states = (%q,%q), want (file,missing)", oldRec.Before.Kind, oldRec.After.Kind)
	}
	newRec := entryRecordForPath(t, entries[0], "Sand Worms.md")
	if newRec.Before.Kind != journal.KindMissing || newRec.After.Kind != journal.KindFile {
		t.Fatalf("new path states = (%q,%q), want (missing,file)", newRec.Before.Kind, newRec.After.Kind)
	}
	if got := string(loadSnapshot(t, root, entries[0], oldRec)); got != before {
		t.Fatalf("snapshot mismatch:\n--- got ---\n%s--- want ---\n%s", got, before)
	}
}

func TestRename_DryRunDoesNotWriteJournalEntry(t *testing.T) {
	root := buildRenameFixture(t)

	if _, err := runCLI(t, "", "rename", "Dune", "Sand Worms", "--dry-run"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := len(loadJournalEntries(t, root)); got != 0 {
		t.Fatalf("len(entries) = %d, want 0", got)
	}
}

func TestMove_WritesJournalEntryForMoveAndRewrites(t *testing.T) {
	root := buildMoveFixture(t)
	oldPath := filepath.Join(root, "Books", "Dune.md")
	before := readNote(t, oldPath)

	if _, err := runCLI(t, "", "move", oldPath, "References/Books"); err != nil {
		t.Fatalf("execute: %v", err)
	}

	entries := loadJournalEntries(t, root)
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	oldRec := entryRecordForPath(t, entries[0], "Books/Dune.md")
	if oldRec.Before.Kind != journal.KindFile || oldRec.After.Kind != journal.KindMissing {
		t.Fatalf("old path states = (%q,%q), want (file,missing)", oldRec.Before.Kind, oldRec.After.Kind)
	}
	newRec := entryRecordForPath(t, entries[0], "References/Books/Dune.md")
	if newRec.Before.Kind != journal.KindMissing || newRec.After.Kind != journal.KindFile {
		t.Fatalf("new path states = (%q,%q), want (missing,file)", newRec.Before.Kind, newRec.After.Kind)
	}
	if got := string(loadSnapshot(t, root, entries[0], oldRec)); got != before {
		t.Fatalf("snapshot mismatch:\n--- got ---\n%s--- want ---\n%s", got, before)
	}
}

func TestMove_DryRunDoesNotWriteJournalEntry(t *testing.T) {
	root := buildMoveFixture(t)
	oldPath := filepath.Join(root, "Books", "Dune.md")

	if _, err := runCLI(t, "", "move", oldPath, "References/Books", "--dry-run"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := len(loadJournalEntries(t, root)); got != 0 {
		t.Fatalf("len(entries) = %d, want 0", got)
	}
}

func TestRepair_WritesJournalEntryWithPreMutationSnapshot(t *testing.T) {
	root := buildRepairFixture(t)
	notePath := filepath.Join(root, "bad_case.md")
	before := readNote(t, notePath)

	if _, err := runCLI(t, "", "repair", "--property", "status", notePath); err != nil {
		t.Fatalf("execute: %v", err)
	}

	entries := loadJournalEntries(t, root)
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	rec := entryRecordForPath(t, entries[0], "bad_case.md")
	if got := string(loadSnapshot(t, root, entries[0], rec)); got != before {
		t.Fatalf("snapshot mismatch:\n--- got ---\n%s--- want ---\n%s", got, before)
	}
}

func TestRepair_NoOpDoesNotWriteJournalEntry(t *testing.T) {
	root := buildRepairFixture(t)
	notePath := filepath.Join(root, "clean.md")

	if _, err := runCLI(t, "", "repair", "--property", "status", notePath); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := len(loadJournalEntries(t, root)); got != 0 {
		t.Fatalf("len(entries) = %d, want 0", got)
	}
}

func TestRepair_DryRunDoesNotWriteJournalEntry(t *testing.T) {
	root := buildRepairFixture(t)
	notePath := filepath.Join(root, "bad_case.md")

	if _, err := runCLI(t, "", "repair", "--property", "status", notePath, "--dry-run"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := len(loadJournalEntries(t, root)); got != 0 {
		t.Fatalf("len(entries) = %d, want 0", got)
	}
}

func TestRelink_WritesJournalEntryWithPreMutationSnapshot(t *testing.T) {
	root := buildRelinkFixture(t)
	notePath := filepath.Join(root, "Notes", "Ref.md")
	before := readNote(t, notePath)

	if _, err := runCLI(t, "", "relink", notePath); err != nil {
		t.Fatalf("execute: %v", err)
	}

	entries := loadJournalEntries(t, root)
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	rec := entryRecordForPath(t, entries[0], "Notes/Ref.md")
	if got := string(loadSnapshot(t, root, entries[0], rec)); got != before {
		t.Fatalf("snapshot mismatch:\n--- got ---\n%s--- want ---\n%s", got, before)
	}
}

func TestRelink_NoOpDoesNotWriteJournalEntry(t *testing.T) {
	root := buildRelinkFixture(t)
	notePath := filepath.Join(root, "Projects", "Roadmap.md")

	if _, err := runCLI(t, "", "relink", notePath); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := len(loadJournalEntries(t, root)); got != 0 {
		t.Fatalf("len(entries) = %d, want 0", got)
	}
}

func TestRelink_DryRunDoesNotWriteJournalEntry(t *testing.T) {
	root := buildRelinkFixture(t)
	notePath := filepath.Join(root, "Notes", "Ref.md")

	if _, err := runCLI(t, "", "relink", notePath, "--dry-run"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := len(loadJournalEntries(t, root)); got != 0 {
		t.Fatalf("len(entries) = %d, want 0", got)
	}
}

func loadJournalEntries(t *testing.T, vaultRoot string) []journal.Entry {
	t.Helper()
	entries, err := journal.ListEntries(journalRootForVault(vaultRoot))
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	return entries
}

func entryRecordForPath(t *testing.T, entry journal.Entry, relPath string) journal.FileRecord {
	t.Helper()
	for _, rec := range entry.Files {
		if rec.Path == relPath {
			return rec
		}
	}
	t.Fatalf("entry %q missing record for %q", entry.ID, relPath)
	return journal.FileRecord{}
}

func loadSnapshot(t *testing.T, vaultRoot string, entry journal.Entry, rec journal.FileRecord) []byte {
	t.Helper()
	if rec.Before.Snapshot == "" {
		return nil
	}
	snapshotPath := filepath.Join(
		journalRootForVault(vaultRoot),
		entry.ID,
		filepath.FromSlash(rec.Before.Snapshot),
	)
	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatalf("read snapshot %q: %v", snapshotPath, err)
	}
	return data
}
