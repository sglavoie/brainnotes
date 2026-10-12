package vaultexport

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// TestMergeCommitHalts refuses a history a linear replay cannot express. Two
// parents is not a boundary the protocol can describe, and guessing which parent
// to diff against would publish a set of changes that never happened as one
// operation.
func TestMergeCommitHalts(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})
	base := f.vault.Head(t)

	f.write("Alpha.md", "on main\n")
	f.commit("main edit")
	f.vault.Git(t, "checkout", "-q", "-b", "side", base)
	f.write("Beta.md", "on side\n")
	f.commit("side edit")
	f.vault.Git(t, "checkout", "-q", "main")
	f.vault.Git(t, "merge", "--no-ff", "-q", "-m", "merge side", "side")

	_, err := f.export(Options{})
	if !errors.Is(err, ErrUnsupportedHistory) {
		t.Fatalf("error: got %v, want ErrUnsupportedHistory", err)
	}
	if got := f.state().Commit; got == "" || got == f.vault.Head(t) {
		t.Fatalf("the cursor advanced past a history the exporter refused: %q", got)
	}
}

// TestBranchChangeHalts refuses a cursor recorded against another branch. The
// commits it names may all be present and still describe a different line of
// history.
func TestBranchChangeHalts(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	f.vault.Git(t, "checkout", "-q", "-b", "other")
	f.write("Alpha.md", "on the other branch\n")
	f.commit("other branch edit")

	cursor := f.state().Commit
	result, err := f.export(Options{})
	if !errors.Is(err, ErrBranchChanged) {
		t.Fatalf("error: got %v, want ErrBranchChanged", err)
	}
	// A halt reports the cursor it halted on, including a halt that refuses before
	// it reaches a commit. An empty Cursor here would read as "no baseline has
	// finished", which is a different vault state and the wrong one to hand
	// whoever has to decide what to do about the branch.
	if result.Cursor != cursor {
		t.Fatalf("reported cursor: got %q, want %q", result.Cursor, cursor)
	}
}

// TestEpochChangeHalts refuses a cursor from a previous enrollment generation.
// The epoch advances when a vault is restored from a backup, which is exactly
// when the history the cursor describes may no longer be this vault's.
func TestEpochChangeHalts(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	advanced, err := vaultsync.AdvanceEpoch(f.root(), vaultsync.Options{Now: f.clock.Now})
	if err != nil {
		t.Fatalf("AdvanceEpoch: %v", err)
	}
	f.local = advanced

	if _, err := f.export(Options{}); !errors.Is(err, ErrEpochChanged) {
		t.Fatalf("error: got %v, want ErrEpochChanged", err)
	}

	// Reset is the remedy, and it re-baselines rather than resuming.
	sequence := f.state().Sequence
	if err := Reset(f.root()); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	result := f.mustExport(Options{})
	if !result.Baseline {
		t.Fatal("after Reset the next export must be a baseline")
	}
	if result.Packages[0].Sequence != sequence+1 {
		t.Fatalf("sequence: got %d, want %d — Reset must not reuse a number the peer already has",
			result.Packages[0].Sequence, sequence+1)
	}
}

// TestHistoryRewriteHalts refuses to continue against a branch that no longer
// contains the commits this device already reported.
func TestHistoryRewriteHalts(t *testing.T) {
	f := newFixture(t)
	base := f.vault.Head(t)

	f.write("Alpha.md", "the version that was exported\n")
	f.commit("edit Alpha")
	f.mustExport(Options{})

	f.vault.Git(t, "reset", "--hard", "-q", base)
	f.write("Alpha.md", "a different history\n")
	f.commit("rewritten")

	_, err := f.export(Options{})
	if !errors.Is(err, ErrHistoryRewritten) {
		t.Fatalf("error: got %v, want ErrHistoryRewritten", err)
	}

	if err := Reset(f.root()); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	result := f.mustExport(Options{})
	if !result.Baseline {
		t.Fatal("after Reset the next export must be a baseline")
	}
	if got := f.payload(result, "Alpha.md"); got != "a different history\n" {
		t.Fatalf("the re-baseline carried %q", got)
	}
}

// TestIdentityChangeHalts refuses an export record copied from another replica.
// Continuing would make this device publish into somebody else's stream.
func TestIdentityChangeHalts(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	foreign := f.state().clone()
	foreign.DeviceID = f.peer.DeviceID
	// saveState validates rather than checking identity, so the foreign record is
	// written the way a file copy would have arrived.
	if err := saveState(f.root(), foreign); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	if _, err := f.export(Options{}); !errors.Is(err, ErrIdentityChanged) {
		t.Fatalf("error: got %v, want ErrIdentityChanged", err)
	}
}

// TestUnsupportedEntryHalts refuses content the protocol cannot describe. A
// symlink is the case that actually occurs; skipping it would leave a replica
// quietly incomplete while every receipt said otherwise.
func TestUnsupportedEntryHalts(t *testing.T) {
	f := newFixture(t)
	if err := symlink("Alpha.md", f.vault.Path("Link.md")); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}
	f.commit("add a symlink")

	_, err := f.export(Options{})
	if !errors.Is(err, ErrUnsupportedEntry) {
		t.Fatalf("baseline error: got %v, want ErrUnsupportedEntry", err)
	}
	if !strings.Contains(err.Error(), "Link.md") {
		t.Fatalf("the refusal must name the path a person has to decide about: %v", err)
	}
}

// TestUnsupportedEntryHaltsIncrementally is the same refusal on the replay path
// rather than the baseline one.
func TestUnsupportedEntryHaltsIncrementally(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	if err := symlink("Alpha.md", f.vault.Path("Link.md")); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}
	f.commit("add a symlink")

	_, err := f.export(Options{})
	if !errors.Is(err, ErrUnsupportedEntry) {
		t.Fatalf("error: got %v, want ErrUnsupportedEntry", err)
	}
}

// TestRemovingAnUnsupportedEntryIsReportedAsWhatItIs covers the other side of
// checkChangeModes, which the adding case never reaches: a commit that *removes*
// a symlink is refused before the deletion-authority check can complain, because
// no journal record could ever match it. A symlink has no bytes a snapshot could
// have hashed, so "this deletion carries no authority" would be an accurate
// sentence and the wrong complaint — nothing a person did would ever satisfy it.
func TestRemovingAnUnsupportedEntryIsReportedAsWhatItIs(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	if err := symlink("Alpha.md", f.vault.Path("Link.md")); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}
	added := f.commit("add a symlink")
	f.remove("Link.md")
	f.commit("remove the symlink")

	// The cursor is placed by hand at the commit that added it, because the
	// exporter would never have got past that commit on its own — and the removal
	// is the commit under test. A vault reaches this state by being restored, or by
	// a person clearing the halt the wrong way.
	record := f.state()
	record.Commit = added
	f.putState(record)

	_, err := f.export(Options{})
	if !errors.Is(err, ErrUnsupportedEntry) {
		t.Fatalf("error: got %v, want ErrUnsupportedEntry", err)
	}
	if errors.Is(err, ErrUnauthorizedDeletion) {
		t.Fatalf("the refusal blames the journal for a file a journal cannot describe: %v", err)
	}
	if !strings.Contains(err.Error(), "Link.md") {
		t.Fatalf("the refusal must name the path: %v", err)
	}
}

// TestACommitCarryingBothAnImportAndALocalEntryHalts is the mixed shape
// importedOperation refuses rather than guesses at.
//
// internal/noteops commits exactly one journal entry's paths, so this cannot come
// out of this codebase — which is exactly why it must not be interpreted. Reading
// it as an import would silently drop the local half; reading it as local would
// echo the peer's change straight back at it.
func TestACommitCarryingBothAnImportAndALocalEntryHalts(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	const importedEntry = "20260304T050610-apply"
	if err := RecordImport(f.root(), importedEntry, Import{
		OperationID:  "op-" + strings.Repeat("ab", 16),
		OriginDevice: f.peer.DeviceID,
	}); err != nil {
		t.Fatalf("RecordImport: %v", err)
	}

	const peerBody = "the peer wrote this\n"
	const localBody = "and this device wrote this\n"
	f.write("Beta.md", peerBody)
	f.journalWrite(importedEntry, "Beta.md", peerBody)
	f.write("Alpha.md", localBody)
	f.journalWrite("20260304T050612-set", "Alpha.md", localBody)
	f.commit("one commit carrying two entries")

	_, err := f.export(Options{})
	if !errors.Is(err, ErrUnsupportedHistory) {
		t.Fatalf("error: got %v, want ErrUnsupportedHistory", err)
	}
	// Named, because the sibling refusal carries the same sentinel and the two
	// tests must not both be passing on whichever branch fires first.
	if !strings.Contains(err.Error(), "both imported and local") {
		t.Fatalf("the refusal does not name the mixed shape: %v", err)
	}
	if len(f.channelPaths()) != len(sortedSeed()) {
		t.Fatalf("the refused commit published something: %v", f.channelPaths())
	}
}

// TestACommitCarryingTwoImportedOperationsHalts is the same refusal for the shape
// where both entries are imports and they disagree about which peer operation the
// commit applied. One commit is one operation; there is no answer to "which".
func TestACommitCarryingTwoImportedOperationsHalts(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	entries := map[string]string{
		"20260304T050610-apply": "op-" + strings.Repeat("ab", 16),
		"20260304T050611-apply": "op-" + strings.Repeat("cd", 16),
	}
	for entryID, operationID := range entries {
		if err := RecordImport(f.root(), entryID, Import{OperationID: operationID, OriginDevice: f.peer.DeviceID}); err != nil {
			t.Fatalf("RecordImport %s: %v", entryID, err)
		}
	}

	const peerBody = "the peer wrote this\n"
	f.write("Beta.md", peerBody)
	for entryID := range entries {
		f.journalWrite(entryID, "Beta.md", peerBody)
	}
	f.commit("one commit carrying two imported entries")

	_, err := f.export(Options{})
	if !errors.Is(err, ErrUnsupportedHistory) {
		t.Fatalf("error: got %v, want ErrUnsupportedHistory", err)
	}
	if !strings.Contains(err.Error(), "two different imported operations") {
		t.Fatalf("the refusal does not name the ambiguity: %v", err)
	}
	// Neither ledger entry was consumed: nothing here established which commit
	// applied which operation.
	for entryID, operationID := range entries {
		if got := f.state().Imported[entryID].OperationID; got != operationID {
			t.Errorf("import ledger for %s: got %q, want %q", entryID, got, operationID)
		}
	}
}

// TestMaxPackagesBoundsOnePass proves a bounded pass is a resumable one: the
// cursor is durable after every package, so what is left is exported next time
// and nothing is exported twice.
func TestMaxPackagesBoundsOnePass(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	var heads []string
	for _, body := range []string{"one\n", "two\n", "three\n"} {
		f.write("Alpha.md", body)
		heads = append(heads, f.commit("edit "+body))
	}

	first := f.mustExport(Options{MaxPackages: 2})
	if len(first.Packages) != 2 {
		t.Fatalf("packages: got %d, want 2", len(first.Packages))
	}
	if first.PendingCommits != 1 {
		t.Fatalf("pending commits: got %d, want 1", first.PendingCommits)
	}
	if got := f.state().Commit; got != heads[1] {
		t.Fatalf("cursor: got %q, want %q", got, heads[1])
	}

	second := f.mustExport(Options{MaxPackages: 2})
	if len(second.Packages) != 1 {
		t.Fatalf("resumed packages: got %d, want 1", len(second.Packages))
	}
	if second.Packages[0].Commit != heads[2] {
		t.Fatalf("resumed commit: got %q, want %q", second.Packages[0].Commit, heads[2])
	}
	if got := f.payload(second, "Alpha.md"); got != "three\n" {
		t.Fatalf("payload: got %q", got)
	}
}
