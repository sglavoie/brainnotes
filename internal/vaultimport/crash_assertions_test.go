package vaultimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/journal"
	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/vaultexport"
	"github.com/sglavoie/brainnotes/internal/vaultfixture"
)

// killPoint is one durable transition and the two states either side of it.
type killPoint struct {
	point string
	// interrupted is what the dead process left on the disk, asserted before
	// anything recovers it. Without it a rollback case would pass just as well
	// against a crash that never applied anything, and the test would be
	// evidence about nothing.
	interrupted map[string]string
	wantOutcome Outcome
	wantState   map[string]string
	// applied says the import became this vault's own history: a recovery
	// entry, a commit, a ledger record, and a receipt for the peer.
	applied bool
	// provenance says the anti-echo ledger already knew about the entry when
	// the process died. It survives a rollback deliberately — see the comment
	// on the rolled-back cases in the table.
	provenance bool
	// entryDir says the dead process left a journal entry directory behind.
	// Asserted before recovery, because a rollback that removed a directory
	// which was never there proves nothing, and listing entries cannot tell
	// the difference: journal.ListEntries skips a directory with no manifest,
	// so an un-finalized entry is invisible to a count of recovery history.
	entryDir bool
}

// requireInterrupted asserts what the dead process left and returns the entry
// the attempt names, which is the only way the parent can know it.
func requireInterrupted(t *testing.T, report crashReport, tc killPoint) string {
	t.Helper()
	// The attempt is what makes the interruption explicable at all.
	interrupted, err := LoadState(report.Root)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if interrupted.Pending.PackageID() != report.PackageID {
		t.Fatalf("the interrupted vault holds attempt %q, want %s",
			interrupted.Pending.PackageID(), report.PackageID)
	}
	requireState(t, report.Root, tc.interrupted, "as the dead process left it")
	entry := interrupted.Pending.JournalEntry
	if got := entryDirExists(t, report.Root, entry); got != tc.entryDir {
		t.Fatalf("the dead process left an entry directory = %v, want %v", got, tc.entryDir)
	}
	return entry
}

// requireRecovered runs the recovery pass and asserts every record it touched.
func requireRecovered(t *testing.T, report crashReport, tc killPoint, entry string) {
	t.Helper()
	recovery, err := Recover(report.Root, Options{})
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if recovery.Outcome != tc.wantOutcome {
		t.Fatalf("recovery outcome = %q, want %q", recovery.Outcome, tc.wantOutcome)
	}
	requireState(t, report.Root, tc.wantState, "after recovery")
	// A rollback owes the disk more than a restored note: the entry directory and
	// its snapshots are the undo record for a change that no longer exists, and
	// leaving them is how `bn undo` ends up offering to revert something nobody
	// did.
	if got := entryDirExists(t, report.Root, entry); got != tc.applied {
		t.Fatalf("entry directory %s exists = %v after recovery, want %v", entry, got, tc.applied)
	}
	requireBooks(t, report, tc.applied, tc.provenance)
}

// requireConverged asserts that a second recovery pass finds nothing and changes
// nothing, which is what makes recovery safe to run ahead of every import.
func requireConverged(t *testing.T, report crashReport) {
	t.Helper()
	state := crashState(t, report.Root)
	commits := crashCommitCount(t, report.Root)
	again, err := Recover(report.Root, Options{})
	if err != nil {
		t.Fatalf("second Recover: %v", err)
	}
	if again.Outcome != OutcomeNone {
		t.Fatalf("a second recovery pass found %q to do", again.Outcome)
	}
	requireState(t, report.Root, state, "after a second recovery pass")
	if now := crashCommitCount(t, report.Root); now != commits {
		t.Fatalf("a second recovery pass added %d commits", now-commits)
	}
}

// requireBooks asserts that the vault's four records of an import — content
// history, recovery history, provenance ledger, and the receipt owed to the peer
// — all say the same thing about it.
func requireBooks(t *testing.T, report crashReport, applied, provenance bool) {
	t.Helper()
	state, err := LoadState(report.Root)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.Pending != nil {
		t.Fatalf("recovery left attempt %q pending", state.Pending.PackageID())
	}

	record, remembered := state.Applied[report.PackageID]
	if remembered != applied {
		t.Fatalf("the import ledger remembers %s = %v, want %v", report.PackageID, remembered, applied)
	}
	if dirty := crashDirty(t, report.Root); len(dirty) != 0 {
		t.Fatalf("the vault is dirty after recovery: %v", dirty)
	}
	requireHistory(t, report, record, applied)
	requireImports(t, report.Root, provenance)
}

// requireHistory asserts that the three records an applied import leaves behind
// — the commit, the recovery entry, and the published receipt — exist together
// and name each other, or that none of them exist at all.
//
// They are worth checking as one claim because their value is in the agreement.
// A receipt naming an entry the journal does not hold tells the peer an import
// is this vault's history when it is not, and no single record can be caught
// being wrong about that on its own.
func requireHistory(t *testing.T, report crashReport, record Record, applied bool) {
	t.Helper()
	// One seed commit, plus one for an import that became history. Two would mean
	// the recovery pass committed the same change again.
	wantCommits := 1
	wantEntries := 0
	if applied {
		wantCommits, wantEntries = 2, 1
	}
	if got := crashCommitCount(t, report.Root); got != wantCommits {
		t.Fatalf("the vault holds %d commits, want %d", got, wantCommits)
	}
	entries := crashEntries(t, report.Root)
	if len(entries) != wantEntries {
		t.Fatalf("the vault holds %d recovery entries, want %d", len(entries), wantEntries)
	}

	receipt := publishedReceipt(t, report.Root, report.PackageID)
	if applied != (receipt != nil) {
		t.Fatalf("a receipt for %s is published = %v, want %v", report.PackageID, receipt != nil, applied)
	}
	if !applied {
		return
	}
	if record.Receipt == nil {
		t.Fatalf("the import ledger records no receipt for an acknowledged package")
	}
	if receipt.RecoveryEntry != record.JournalEntry || receipt.RecoveryEntry != entries[0].ID {
		t.Fatalf("the receipt names entry %s, the ledger %s, the journal %s",
			receipt.RecoveryEntry, record.JournalEntry, entries[0].ID)
	}
}

// requireImports asserts the anti-echo ledger holds exactly the expected number
// of provenance records, which is what stops the peer's own change being
// published back to it.
func requireImports(t *testing.T, root string, provenance bool) {
	t.Helper()
	state, err := vaultexport.LoadState(root)
	if err != nil {
		t.Fatalf("vaultexport.LoadState: %v", err)
	}
	want := 0
	if provenance {
		want = 1
	}
	if len(state.Imported) != want {
		t.Fatalf("the provenance ledger holds %d records, want %d: %v", len(state.Imported), want, state.Imported)
	}
}

// A redelivery of a package a recovered vault already applied must change
// nothing and must republish the receipt it already minted, because the reason a
// peer re-sends is almost always that the reply went missing.
func TestRedeliveryAfterRecoveryChangesNothing(t *testing.T) {
	vaultfixture.Isolate(t)
	report := interrupt(t, crashAcknowledged)

	if _, err := Recover(report.Root, Options{}); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	first := publishedReceipt(t, report.Root, report.PackageID)
	if first == nil {
		t.Fatal("recovery adopted no receipt for an import that had published one")
	}
	state := crashState(t, report.Root)
	commits := crashCommitCount(t, report.Root)

	result, err := Apply(report.Root, report.PackageDir, Options{})
	if err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if !result.Duplicate {
		t.Fatal("a redelivery was not reported as one")
	}
	if result.Receipt == nil || result.Receipt.AppliedAt != first.AppliedAt {
		t.Fatalf("the redelivery answered with a different receipt than the one already published")
	}
	requireState(t, report.Root, state, "after a redelivery")
	if now := crashCommitCount(t, report.Root); now != commits {
		t.Fatalf("a redelivery added %d commits", now-commits)
	}
	if entries := crashEntries(t, report.Root); len(entries) != 1 {
		t.Fatalf("a redelivery left %d recovery entries, want 1", len(entries))
	}
}

// An import interrupted before it became history is rolled back — but never over
// somebody else's newer edit. The crash, a person opening the note and saving it,
// and the next import are three events with no ordering between them, and the
// edit is the only copy of itself.
//
// Every artifact is kept on purpose: the attempt says the import is unresolved,
// the journal entry holds what the paths held before it, and the working tree
// holds what the person wrote. A person with all three can choose; a process that
// guessed would have destroyed one.
func TestRecoveryWillNotRollBackOverANewerEdit(t *testing.T) {
	vaultfixture.Isolate(t)
	report := interrupt(t, crashApplied)

	const foreign = "---\ntitle: Alpha\n---\n\nSomebody saved this after the crash.\n"
	if err := os.WriteFile(filepath.Join(report.Root, crashModifiedPath), []byte(foreign), 0o644); err != nil {
		t.Fatalf("foreign edit: %v", err)
	}

	_, err := Recover(report.Root, Options{})
	requireIs(t, err, ErrStrandedImport, "recovering over a newer edit")
	if !strings.Contains(err.Error(), crashModifiedPath) {
		t.Fatalf("the refusal does not name the contested path: %v", err)
	}

	if got := crashState(t, report.Root)[crashModifiedPath]; got != foreign {
		t.Fatalf("recovery overwrote a newer edit: %q", got)
	}
	state, err := LoadState(report.Root)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.Pending.PackageID() != report.PackageID {
		t.Fatal("a stranded import cleared its own attempt")
	}
	finalized, err := journal.Finalized(noteops.JournalRoot(report.Root), state.Pending.JournalEntry)
	if err != nil {
		t.Fatalf("Finalized: %v", err)
	}
	if finalized {
		t.Fatal("a stranded import finalized its entry")
	}
	if _, err := journal.ReadSnapshot(noteops.JournalRoot(report.Root),
		state.Pending.JournalEntry, crashModifiedPath); err != nil {
		t.Fatalf("the stranded entry no longer holds what the path used to say: %v", err)
	}

	// And it stays refused, rather than resolving itself on a later pass: the
	// contested content is a decision, and nothing here is entitled to make it.
	_, again := Recover(report.Root, Options{})
	requireIs(t, again, ErrStrandedImport, "a second pass over a stranded import")

	// Which also means no further import can run, so the evidence cannot be
	// buried by the next package the transport delivers.
	_, applyErr := Apply(report.Root, report.PackageDir, Options{})
	requireIs(t, applyErr, ErrStrandedImport, "applying over a stranded import")
}

// A vault whose import died before it started must be usable again immediately,
// with the same package: nothing was applied, so nothing stands in the way.
func TestApplyAfterAnInterruptionResolvesItAndProceeds(t *testing.T) {
	vaultfixture.Isolate(t)
	report := interrupt(t, crashApplied)

	result, err := Apply(report.Root, report.PackageDir, Options{})
	if err != nil {
		t.Fatalf("Apply after an interruption: %v", err)
	}
	if result.Duplicate {
		t.Fatal("a rolled-back import was reported as already applied")
	}
	requireState(t, report.Root, crashAfter(), "after re-applying a rolled-back import")
	if got := crashCommitCount(t, report.Root); got != 2 {
		t.Fatalf("the vault holds %d commits, want 2", got)
	}
	if entries := crashEntries(t, report.Root); len(entries) != 1 {
		t.Fatalf("the vault holds %d recovery entries, want 1", len(entries))
	}
	if publishedReceipt(t, report.Root, report.PackageID) == nil {
		t.Fatal("re-applying published no receipt")
	}
	if dirty := crashDirty(t, report.Root); len(dirty) != 0 {
		t.Fatalf("the vault is dirty after re-applying: %v", dirty)
	}
}
