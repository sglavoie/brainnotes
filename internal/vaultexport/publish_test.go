package vaultexport

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// TestReservedSequenceWithNoPackageIsReused is the common interruption: the
// reservation is durable before Publish is called at all, so a pass that died
// there created nothing and the number was never spent.
func TestReservedSequenceWithNoPackageIsReused(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	f.write("Alpha.md", "edited\n")
	head := f.commit("edit Alpha")
	f.reserve(&Reservation{
		Sequence:  f.state().Sequence + 1,
		PackageID: "pkg-" + strings.Repeat("0", 32),
		Commit:    head,
	})

	result := f.mustExport(Options{})
	if len(result.BurnedSequences) != 0 {
		t.Fatalf("burned sequences: got %v, want none", result.BurnedSequences)
	}
	if len(result.Packages) != 1 || result.Packages[0].Sequence != 2 {
		t.Fatalf("packages: %+v, want one at sequence 2", result.Packages)
	}
	if state := f.state(); state.Pending != nil || state.Sequence != 2 || state.Commit != head {
		t.Fatalf("state: %+v", state)
	}
}

// TestReservedPackageThatLandedIsAdopted covers the publication whose manifest
// arrived and whose record did not. The manifest's arrival is the commit point,
// so the peer may already hold it: the sequence is spent, the cursor advances,
// and the path ledger is rebuilt from the manifest itself.
func TestReservedPackageThatLandedIsAdopted(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})
	prior := f.vault.Read(t, "Alpha.md")

	f.write("Alpha.md", "landed but unrecorded\n")
	head := f.commit("edit Alpha")

	operation := vaultsync.DeriveOperationID(f.local.VaultID, head)
	landed := f.publishByHand(2, operation, "Alpha.md", "landed but unrecorded\n",
		vaultsync.Existing(vaultsync.HashBytes([]byte(prior))))
	f.reserve(&Reservation{Sequence: 2, PackageID: landed.PackageID, Commit: head})

	result := f.mustExport(Options{})
	if len(result.Packages) != 1 {
		t.Fatalf("packages: %+v, want only the adopted one", result.Packages)
	}
	if result.Packages[0].PackageID != landed.PackageID {
		t.Fatalf("the landed package was republished as %q", result.Packages[0].PackageID)
	}
	if result.Packages[0].Changes != 1 {
		t.Fatalf("adopted changes: got %d, want 1", result.Packages[0].Changes)
	}
	state := f.state()
	if state.Pending != nil || state.Sequence != 2 || state.Commit != head {
		t.Fatalf("state: %+v", state)
	}
	if got := state.Paths["Alpha.md"]; got != operation {
		t.Fatalf("path ledger: got %q, want the adopted operation %q", got, operation)
	}
	// Nothing was sent twice, and the baseline's other paths are untouched.
	requirePaths(t, f.channelPaths(), []string{
		"Alpha.md", "Alpha.md", "Beta.md", "Notes/Gamma.md", "Unrelated.md", "brainnotes.toml",
	})
	if next := f.mustExport(Options{}); len(next.Packages) != 0 {
		t.Fatalf("the adopted commit was exported again: %+v", next.Packages)
	}
}

// TestReservedPackageWithoutAManifestBurnsItsSequence covers the interruption
// between the package directory existing and its manifest landing. A peer reads
// that directory as an incomplete package forever and the identity cannot be
// reused, so the sequence is spent and the loss is reported rather than
// swallowed.
func TestReservedPackageWithoutAManifestBurnsItsSequence(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	f.write("Alpha.md", "edited\n")
	head := f.commit("edit Alpha")

	packageID := "pkg-" + strings.Repeat("1", 32)
	if err := os.MkdirAll(filepath.Join(f.channel, packageID), 0o755); err != nil {
		t.Fatalf("stage an interrupted publication: %v", err)
	}
	f.reserve(&Reservation{Sequence: 2, PackageID: packageID, Commit: head})

	result := f.mustExport(Options{})
	if len(result.BurnedSequences) != 1 || result.BurnedSequences[0] != 2 {
		t.Fatalf("burned sequences: got %v, want [2]", result.BurnedSequences)
	}
	if len(result.Packages) != 1 || result.Packages[0].Sequence != 3 {
		t.Fatalf("packages: %+v, want one at sequence 3", result.Packages)
	}
	if result.Packages[0].PackageID == packageID {
		t.Fatal("the burned identity was reused")
	}
	if got := f.payload(result, "Alpha.md"); got != "edited\n" {
		t.Fatalf("the re-exported commit carried %q", got)
	}
}

// TestReservedPackageFromAnotherProducerIsRefused covers a second writer in this
// device's channel. Continuing would interleave two producers' sequences under
// one device identity, which no receiver could untangle.
func TestReservedPackageFromAnotherProducerIsRefused(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	f.write("Alpha.md", "edited\n")
	head := f.commit("edit Alpha")

	foreign := f.publishByHand(5, "", "Alpha.md", "somebody else's bytes\n", vaultsync.Absent())
	f.reserve(&Reservation{Sequence: 2, PackageID: foreign.PackageID, Commit: head})

	if _, err := f.export(Options{}); !errors.Is(err, ErrChannelConflict) {
		t.Fatalf("error: got %v, want ErrChannelConflict", err)
	}
}

// TestReservedPathThatIsNotADirectoryIsRefused covers the other shape of channel
// conflict: something that is not a package directory occupying a reserved name.
func TestReservedPathThatIsNotADirectoryIsRefused(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	f.write("Alpha.md", "edited\n")
	head := f.commit("edit Alpha")

	packageID := "pkg-" + strings.Repeat("2", 32)
	if err := os.WriteFile(filepath.Join(f.channel, packageID), []byte("not a package\n"), 0o644); err != nil {
		t.Fatalf("stage the conflict: %v", err)
	}
	f.reserve(&Reservation{Sequence: 2, PackageID: packageID, Commit: head})

	if _, err := f.export(Options{}); !errors.Is(err, ErrChannelConflict) {
		t.Fatalf("error: got %v, want ErrChannelConflict", err)
	}
}

// TestReservationIsNotAdoptedOntoAnotherBranch is why the branch is checked
// before the reservation is resolved rather than after.
//
// Adopting a publication records the current branch as the cursor's. Checked
// afterwards, the cursor would be compared against a branch it had just been
// moved to and the halt could never fire — on exactly the pass that most needs
// it, because switching branches under a running export is a plausible reason it
// was interrupted in the first place.
func TestReservationIsNotAdoptedOntoAnotherBranch(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})
	prior := f.vault.Read(t, "Alpha.md")
	followed := f.state().Branch

	f.write("Alpha.md", "landed but unrecorded\n")
	head := f.commit("edit Alpha")
	landed := f.publishByHand(2, vaultsync.DeriveOperationID(f.local.VaultID, head), "Alpha.md",
		"landed but unrecorded\n", vaultsync.Existing(vaultsync.HashBytes([]byte(prior))))
	f.reserve(&Reservation{Sequence: 2, PackageID: landed.PackageID, Commit: head})

	// A descendant of the cursor, so nothing else about the history is wrong: only
	// the name of the branch HEAD is on has changed.
	f.vault.Git(t, "switch", "-q", "-c", "elsewhere")

	_, err := f.export(Options{})
	if !errors.Is(err, ErrBranchChanged) {
		t.Fatalf("error: got %v, want ErrBranchChanged", err)
	}
	state := f.state()
	if state.Branch != followed {
		t.Fatalf("the refused pass moved the cursor's branch to %q", state.Branch)
	}
	if state.Pending == nil || state.Pending.PackageID != landed.PackageID {
		t.Fatalf("the refused pass consumed the reservation: %+v", state.Pending)
	}
	if state.Commit == head {
		t.Fatal("the refused pass advanced the cursor onto the other branch")
	}
}

// TestAHaltStillReportsWhatItAlreadySpent covers the report of a pass that did
// some durable work and then stopped.
//
// A burned sequence is the case that matters: the identity is gone, the gap it
// leaves in this device's stream is permanent, and this Result is the only place
// it is ever named. Dropping the report because a later commit turned out to be
// unpublishable would lose the one record of where the gap came from, and nothing
// a retry does can produce it again — the reservation is resolved by then.
func TestAHaltStillReportsWhatItAlreadySpent(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	// An interrupted publication: the directory exists, the manifest never landed.
	f.write("Alpha.md", "edited\n")
	head := f.commit("edit Alpha")
	packageID := "pkg-" + strings.Repeat("4", 32)
	if err := os.MkdirAll(filepath.Join(f.channel, packageID), 0o755); err != nil {
		t.Fatalf("stage an interrupted publication: %v", err)
	}
	f.reserve(&Reservation{Sequence: 2, PackageID: packageID, Commit: head})

	// And then a commit this pass will refuse, after the burn is already durable.
	f.remove("Beta.md")
	f.commit("remove Beta.md by hand")

	result, err := f.export(Options{})
	if !errors.Is(err, ErrUnauthorizedDeletion) {
		t.Fatalf("error: got %v, want ErrUnauthorizedDeletion", err)
	}
	if result == nil {
		t.Fatal("the halt discarded the report of everything already spent")
	}
	if len(result.BurnedSequences) != 1 || result.BurnedSequences[0] != 2 {
		t.Fatalf("burned sequences: got %v, want [2]", result.BurnedSequences)
	}
	// The package the pass did publish before halting is reported too, and it is
	// durable: the cursor stopped at it rather than at the refused commit.
	if len(result.Packages) != 1 || result.Packages[0].Commit != head {
		t.Fatalf("packages: %+v, want the one commit that did publish", result.Packages)
	}
	if got := f.state().Commit; got != head {
		t.Fatalf("cursor: got %q, want %q", got, head)
	}
	// Reported, not just durable. An empty Cursor here would say "a baseline is
	// still unfinished", which is a different vault state and the wrong thing to
	// tell whoever has to decide what to do about the halt.
	if result.Cursor != head {
		t.Fatalf("reported cursor: got %q, want %q", result.Cursor, head)
	}
}

// TestAdoptedBaselineSliceDoesNotAdvanceTheCursor pins what Reservation.Baseline
// decides. A baseline slice that landed records the paths it carried and nothing
// more: the rest of the baseline has not been sent, so the commit it describes is
// not yet fully accounted for.
func TestAdoptedBaselineSliceDoesNotAdvanceTheCursor(t *testing.T) {
	f := newFixture(t)
	pinned := f.vault.Head(t)
	bounded := Options{MaxBaselineBytes: 1, MaxPackages: 1}

	f.mustExport(bounded)
	afterFirstSlice := f.state().clone()

	second := f.mustExport(bounded)
	slice := second.Packages[0]
	carried := f.paths(second)

	// Rewound to before the second slice was recorded, with only the reservation
	// that publication would have written.
	afterFirstSlice.Pending = &Reservation{
		Sequence:  slice.Sequence,
		PackageID: slice.PackageID,
		Commit:    slice.Commit,
		Baseline:  true,
	}
	f.putState(afterFirstSlice)

	result := f.mustExport(bounded)
	if len(result.Packages) != 1 || result.Packages[0].PackageID != slice.PackageID {
		t.Fatalf("packages: %+v, want only the adopted slice %q", result.Packages, slice.PackageID)
	}
	state := f.state()
	if state.Commit != "" {
		t.Fatalf("an adopted baseline slice advanced the cursor to %q", state.Commit)
	}
	if state.Baseline != pinned {
		t.Fatalf("baseline pin: got %q, want %q", state.Baseline, pinned)
	}
	if state.Sequence != slice.Sequence {
		t.Fatalf("sequence: got %d, want %d", state.Sequence, slice.Sequence)
	}
	for _, p := range carried {
		if _, known := state.Paths[p]; !known {
			t.Errorf("the adopted slice's path %q is missing from the ledger", p)
		}
	}

	// And the baseline still finishes, carrying every seeded path exactly once.
	f.mustExport(Options{MaxBaselineBytes: 1})
	requirePaths(t, f.channelPaths(), sortedSeed())
}
