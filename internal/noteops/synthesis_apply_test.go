package noteops

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/journal"
	"github.com/sglavoie/brainnotes/internal/vault"
)

func TestApplySynthesisNote_CreatesNoteAndJournal(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	plan, err := PlanSynthesisNote(root, cfg, synthesisRequest("Synthesis"))
	if err != nil {
		t.Fatalf("PlanSynthesisNote: %v", err)
	}
	result, err := ApplySynthesisNote(root, plan)
	if err != nil {
		t.Fatalf("ApplySynthesisNote: %v", err)
	}
	assertCreateTestFile(t, root, "Synthesis.md", string(plan.Content))
	if string(result.Content) != string(plan.Content) {
		t.Fatal("result content diverged from the plan")
	}
	if result.Journal == nil {
		t.Fatal("expected a journal entry")
	}
	if result.Journal.Command != "bn web synthesize" {
		t.Fatalf("command = %q, want %q", result.Journal.Command, "bn web synthesize")
	}
	record := createRecordForPath(t, *result.Journal, "Synthesis.md")
	if record.Before.Kind != journal.KindMissing {
		t.Fatalf("before = %v, want missing", record.Before.Kind)
	}
	if record.After.Kind != journal.KindFile || record.After.SHA256 != vault.HashBytes(plan.Content) {
		t.Fatalf("after = %+v, want the created bytes", record.After)
	}
}

func TestApplySynthesisNote_CreatesMissingFolder(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	req := synthesisRequest("Deep")
	req.Folder = "New/Deeper"
	plan, err := PlanSynthesisNote(root, cfg, req)
	if err != nil {
		t.Fatalf("PlanSynthesisNote: %v", err)
	}
	if _, err := ApplySynthesisNote(root, plan); err != nil {
		t.Fatalf("ApplySynthesisNote: %v", err)
	}
	assertCreateTestFile(t, root, "New/Deeper/Deep.md", string(plan.Content))
}

func TestApplySynthesisNote_ConcurrentDestinationLeavesNothing(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	plan, err := PlanSynthesisNote(root, cfg, synthesisRequest("Synthesis"))
	if err != nil {
		t.Fatalf("PlanSynthesisNote: %v", err)
	}
	writeCreateTestFile(t, root, "Synthesis.md", "written by someone else\n")

	_, err = ApplySynthesisNote(root, plan)
	if CodeOf(err) != CodeConflict {
		t.Fatalf("code = %v (%v), want CodeConflict", CodeOf(err), err)
	}
	assertCreateTestFile(t, root, "Synthesis.md", "written by someone else\n")
	assertNoJournalEntries(t, root)
}

func TestApplySynthesisNote_SourceConflictLeavesNothing(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	anchor, err := os.ReadFile(filepath.Join(root, "Anchor.md"))
	if err != nil {
		t.Fatalf("read anchor: %v", err)
	}
	req := synthesisRequest("Synthesis")
	req.Sources[0].SHA256 = vault.HashBytes(anchor)
	plan, err := PlanSynthesisNote(root, cfg, req)
	if err != nil {
		t.Fatalf("PlanSynthesisNote: %v", err)
	}
	writeCreateTestFile(t, root, "Anchor.md", "edited after planning\n")

	_, err = ApplySynthesisNote(root, plan)
	if CodeOf(err) != CodeConflict {
		t.Fatalf("code = %v (%v), want CodeConflict", CodeOf(err), err)
	}
	if _, err := os.Lstat(filepath.Join(root, "Synthesis.md")); !os.IsNotExist(err) {
		t.Fatalf("destination was written: %v", err)
	}
	assertNoJournalEntries(t, root)
}

func TestApplySynthesisNote_ConfigConflictLeavesNothing(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	tomlBytes, err := os.ReadFile(filepath.Join(root, config.FileName))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	req := synthesisRequest("Synthesis")
	req.Requires = []SynthesisRequirement{{Path: config.FileName, Expect: vault.ExpectHash(vault.HashBytes(tomlBytes))}}
	plan, err := PlanSynthesisNote(root, cfg, req)
	if err != nil {
		t.Fatalf("PlanSynthesisNote: %v", err)
	}
	if len(plan.Requirements) != 3 {
		t.Fatalf("requirements = %+v, want the sources plus the config", plan.Requirements)
	}
	writeCreateTestFile(t, root, config.FileName, "ignore = []\n")

	_, err = ApplySynthesisNote(root, plan)
	if CodeOf(err) != CodeConflict {
		t.Fatalf("code = %v (%v), want CodeConflict", CodeOf(err), err)
	}
	if _, err := os.Lstat(filepath.Join(root, "Synthesis.md")); !os.IsNotExist(err) {
		t.Fatalf("destination was written: %v", err)
	}
	assertNoJournalEntries(t, root)
}

func TestApplySynthesisNote_RejectsNonMissingExpectation(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	plan, err := PlanSynthesisNote(root, cfg, synthesisRequest("Synthesis"))
	if err != nil {
		t.Fatalf("PlanSynthesisNote: %v", err)
	}
	plan.Expect = vault.ExpectAnyContent()
	if _, err := ApplySynthesisNote(root, plan); CodeOf(err) != CodeInvalidRequest {
		t.Fatalf("code = %v (%v), want CodeInvalidRequest", CodeOf(err), err)
	}
}

func TestApplySynthesisNote_UndoRemovesTheNote(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	plan, err := PlanSynthesisNote(root, cfg, synthesisRequest("Synthesis"))
	if err != nil {
		t.Fatalf("PlanSynthesisNote: %v", err)
	}
	result, err := ApplySynthesisNote(root, plan)
	if err != nil {
		t.Fatalf("ApplySynthesisNote: %v", err)
	}

	if _, err := journal.Restore(root, JournalRoot(root), result.Journal.ID, journal.RestoreOptions{}); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := os.Lstat(plan.Destination.AbsPath); !os.IsNotExist(err) {
		t.Fatalf("undo left the note behind: %v", err)
	}
}

func TestApplySynthesisNote_UndoRefusesChangedRevision(t *testing.T) {
	root, cfg := synthesisTestVault(t)

	plan, err := PlanSynthesisNote(root, cfg, synthesisRequest("Synthesis"))
	if err != nil {
		t.Fatalf("PlanSynthesisNote: %v", err)
	}
	result, err := ApplySynthesisNote(root, plan)
	if err != nil {
		t.Fatalf("ApplySynthesisNote: %v", err)
	}
	writeCreateTestFile(t, root, "Synthesis.md", "edited by hand\n")

	_, err = journal.Restore(root, JournalRoot(root), result.Journal.ID, journal.RestoreOptions{})
	if !journal.IsRestoreConflict(err) {
		t.Fatalf("err = %v, want a restore conflict", err)
	}
	assertCreateTestFile(t, root, "Synthesis.md", "edited by hand\n")
}
