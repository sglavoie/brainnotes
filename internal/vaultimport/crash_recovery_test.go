package vaultimport

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultfixture"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// A process killed at any of the five gaps must converge on exactly one of two
// states: the import never happened, or it happened completely. The undo
// manifest is what decides which, and it is the only thing that can: it is what
// `bn undo` reads, what authorizes committing a deletion, and what the receipt
// promises the peer. Everything before it rolls back; everything after it rolls
// forward.
//
// Each case asserts the four failures this session exists to prevent. No
// duplicate mutation — the content is one of the two states and the vault holds
// one commit per completed import, never two. No premature acknowledgment — a
// receipt exists only where the change is durable recovery history. No missing
// history — a completed import has its entry and its commit. And convergence,
// checked twice, because a recovery pass runs before every import and a second
// one that changed anything would mean the first did not finish.
func TestImportRecoversFromEveryDurableTransition(t *testing.T) {
	cases := []killPoint{
		// The attempt is durable and the transaction has not been committed, so no
		// content moved, no entry directory exists, and there is nothing to undo.
		{point: crashDeclared, interrupted: crashBefore(), wantOutcome: OutcomeDiscarded, wantState: crashBefore()},
		// The peer's bytes are on the disk and the journal captured what they
		// replaced, with no manifest to say so. Not history yet, so it comes back out.
		{point: crashApplied, interrupted: crashAfter(), wantOutcome: OutcomeRolledBack, wantState: crashBefore(), entryDir: true},
		// Same, with the provenance ledger written. The ledger record outlives the
		// rollback and is harmless: it is keyed on a journal entry that no longer
		// exists and can never be committed, so the only reader — the exporter,
		// asking whether a commit it is about to publish came from a peer — never
		// looks it up. Removing it would mean a second durable write on the recovery
		// path to tidy something unreachable.
		{point: crashProvenance, interrupted: crashAfter(), wantOutcome: OutcomeRolledBack, wantState: crashBefore(), provenance: true, entryDir: true},
		// The manifest and the commit are both durable. The change is this vault's
		// own history now, and the peer has not been told.
		{point: crashFinalized, interrupted: crashAfter(), wantOutcome: OutcomeCompleted, wantState: crashAfter(), applied: true, provenance: true, entryDir: true},
		// The receipt is published too, and only the ledger is missing. The peer may
		// already have acted on that receipt, so recovery adopts it rather than
		// minting a second statement about the same package.
		{point: crashAcknowledged, interrupted: crashAfter(), wantOutcome: OutcomeCompleted, wantState: crashAfter(), applied: true, provenance: true, entryDir: true},
	}

	for _, tc := range cases {
		t.Run(tc.point, func(t *testing.T) {
			vaultfixture.Isolate(t)
			report := interrupt(t, tc.point)
			entry := requireInterrupted(t, report, tc)
			requireRecovered(t, report, tc, entry)
			requireConverged(t, report)
		})
	}
}

// A rollback is three durable steps of its own, and the middle of it is the one
// place where "nothing happened here" and "everything happened here and was put
// back" look the same from the disk.
//
// They look the same because a rollback's whole job is to make them the same: the
// content is back at what the package expected to find, which is exactly what a
// path that was never written holds. That costs nothing while the vault is
// undisturbed — a second pass re-derives an empty plan and finishes the tidying —
// and it costs everything the moment a person edits one of those notes first,
// because then the path holds neither state, which is the signature of an edit
// that must be refused rather than overwritten. The refusal is right for a
// rollback that has not happened and wrong for one that has, and it blocks every
// later import until somebody intervenes.
//
// So the rollback writes down that it finished, and this is the evidence that the
// record is load-bearing: the same vault, the same edit, resolved once with the
// record cleared and once with it intact.
func TestAnInterruptedRollbackFinishesWithoutStranding(t *testing.T) {
	vaultfixture.Isolate(t)
	report := interrupt(t, crashApplied)
	interruptRecovery(t, report.Root, crashRestored)

	entry := requireRollbackRecorded(t, report.Root)

	// Somebody opens the vault and saves one of the restored notes.
	edited := "a person edited this after the rollback and before it was tidied up\n"
	writeNote(t, report.Root, crashModifiedPath, edited)

	requireStrandedWithoutTheRecord(t, report.Root, edited)
	requireRollbackResumed(t, report, entry, edited)
}

// requireRollbackRecorded checks what the second dead process left: the content
// restored, and every artifact that says the rollback is unfinished still
// standing. It returns the journal entry the attempt still names.
func requireRollbackRecorded(t *testing.T, root string) string {
	t.Helper()
	requireState(t, root, crashBefore(), "after an interrupted rollback")
	state, err := LoadState(root)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.Pending == nil {
		t.Fatal("an interrupted rollback cleared the attempt it had not finished")
	}
	if state.Pending.Restored == nil {
		t.Fatal("a rollback that put three paths back left no record of having done so")
	}
	if got := len(state.Pending.Restored.Paths); got != len(crashPaths) {
		t.Fatalf("the rollback recorded %d restored paths, want %d", got, len(crashPaths))
	}
	if !entryDirExists(t, root, state.Pending.JournalEntry) {
		t.Fatal("an interrupted rollback removed the entry it restored from")
	}
	return state.Pending.JournalEntry
}

// requireRollbackResumed runs the pass that finds the rollback's own record and
// checks that it converges: rolled back, the newer edit untouched, and every
// artifact of the attempt gone.
func requireRollbackResumed(t *testing.T, report crashReport, entry, edited string) {
	t.Helper()
	recovery, err := Recover(report.Root, Options{})
	if err != nil {
		t.Fatalf("resuming a finished rollback over a newer edit: %v", err)
	}
	if recovery.Outcome != OutcomeRolledBack {
		t.Fatalf("the resumed rollback reported %q, want %q", recovery.Outcome, OutcomeRolledBack)
	}
	requireState(t, report.Root, map[string]string{crashModifiedPath: edited}, "after the rollback was resumed")
	if entryDirExists(t, report.Root, entry) {
		t.Fatal("the resumed rollback left the entry directory behind")
	}
	// Not requireBooks: the edit is deliberately left in the working tree, so this
	// is the one recovered vault in this file that is legitimately dirty.
	requireHistory(t, report, Record{}, false)
	requireImports(t, report.Root, false)
	settled, err := LoadState(report.Root)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if settled.Pending != nil {
		t.Fatalf("the resumed rollback left attempt %q pending", settled.Pending.PackageID())
	}
	if _, remembered := settled.Applied[report.PackageID]; remembered {
		t.Fatal("a rolled-back package was remembered as applied")
	}
}

// requireStrandedWithoutTheRecord clears the finished rollback's record and shows
// what the next pass does without it: refuses, permanently, over a rollback that
// is already done. The record is put back afterwards so the caller can watch the
// same pass succeed with it.
func requireStrandedWithoutTheRecord(t *testing.T, root, edited string) {
	t.Helper()
	local, err := vaultsync.Load(root)
	if err != nil {
		t.Fatalf("vaultsync.Load: %v", err)
	}
	state, err := loadState(root, local)
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}
	restored := state.Pending.Restored
	state.Pending.Restored = nil
	if err := saveState(root, state); err != nil {
		t.Fatalf("clear the rollback record: %v", err)
	}

	_, err = Recover(root, Options{})
	if !errors.Is(err, ErrStrandedImport) {
		t.Fatalf("a pass with nothing but the disk to go on resolved the edit as %v, want ErrStrandedImport", err)
	}
	requireState(t, root, map[string]string{crashModifiedPath: edited}, "after a stranded pass")

	state.Pending.Restored = restored
	if err := saveState(root, state); err != nil {
		t.Fatalf("restore the rollback record: %v", err)
	}
}

// TestARollbackRecordNamingAnUntouchedPathIsRefused is the other side of the
// record's cost.
//
// A record that is merely present is believed: the next pass skips the
// restoration, deletes the journal entry holding the only copy of the
// pre-change bytes, and settles the attempt. So a record this package's own
// rollback could not have written — naming a path the operation never changed —
// has to be refused rather than acted on, or a damaged state file would destroy
// the evidence for an application that is still on the disk.
func TestARollbackRecordNamingAnUntouchedPathIsRefused(t *testing.T) {
	vaultfixture.Isolate(t)
	report := interrupt(t, crashApplied)

	local, err := vaultsync.Load(report.Root)
	if err != nil {
		t.Fatalf("vaultsync.Load: %v", err)
	}
	state, err := loadState(report.Root, local)
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}
	entry := state.Pending.JournalEntry
	state.Pending.Restored = &Rollback{Paths: []string{"Nobody Touched This.md"}}

	// The writer refuses it too, which is why the file has to be forged by hand
	// to get a reader anywhere near it.
	if err := saveState(report.Root, state); !errors.Is(err, ErrCorruptState) {
		t.Fatalf("writing a forged rollback record reported %v, want ErrCorruptState", err)
	}
	forgeState(t, report.Root, state)

	if _, err := Recover(report.Root, Options{}); !errors.Is(err, ErrCorruptState) {
		t.Fatalf("a forged rollback record resolved as %v, want ErrCorruptState", err)
	}
	// Everything the forged record would have licensed destroying is still here.
	requireState(t, report.Root, crashAfter(), "after a forged rollback record was refused")
	if !entryDirExists(t, report.Root, entry) {
		t.Fatal("a refused rollback record still cost the journal entry its snapshots")
	}
}

// forgeState writes the record straight to the state file, past the validation
// saveState runs — the way a hand-edited or half-written file reaches a reader.
func forgeState(t *testing.T, root string, record *State) {
	t.Helper()
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatalf("encode forged import state: %v", err)
	}
	if err := os.WriteFile(StatePath(root), append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write forged import state: %v", err)
	}
}

// writeNote edits a note in a vault the parent did not build, the way a person
// with an editor would.
func writeNote(t *testing.T, root, rel, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
		t.Fatalf("edit %s: %v", rel, err)
	}
}
