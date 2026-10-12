package vaultimport

import (
	"os"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// The bodies one replica writes while the other is writing its own. They differ
// from apply_test.go's, which are what the *peer* sends: a conflict needs a third
// version of the path, and a test that reused the packaged body would be
// asserting about convergence without noticing.
const (
	localAlphaBody = "---\nstatus: draft\ntitle: Alpha\n---\n\nAlpha body, edited here and not committed.\n"
	localGammaBody = "---\nstatus: draft\ntitle: Gamma\n---\n\nGamma body, edited here and not committed.\n"
	peerGammaBody  = "---\nstatus: review\ntitle: Gamma\n---\n\nGamma body, rewritten on the peer.\n"
)

// TestApplyHoldsAConcurrentEditWithEveryVersionOfThePath is the session's
// central claim in its plainest form: both replicas edited one note from one
// starting point, the import is held rather than refused-and-forgotten, and all
// three versions survive somewhere a resolution can reach them.
//
// The baseline assertion is the one that is easy to lose. The remote branch is
// in the package and the local branch is on disk, so both are trivially
// available; the version the two diverged *from* exists only in this vault's
// content history, and a three-way merge without it is a two-way merge.
func TestApplyHoldsAConcurrentEditWithEveryVersionOfThePath(t *testing.T) {
	r := enroll(t)
	baseline := r.pair.Local.Read(t, "Alpha.md")
	packaged := r.believes(t, "Alpha.md")
	head := r.pair.Local.Head(t)
	commits := commitCount(t, r.pair.Local)
	r.pair.Local.Write(t, "Alpha.md", localAlphaBody)

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), packaged)
	})

	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying a package onto a note edited here")

	record := heldOn(t, r, manifest.PackageID)
	if record.OperationID == "" || record.OriginDevice != manifest.OriginDevice {
		t.Fatalf("the hold records %+v, want the package's own operation and origin", record)
	}
	if record.Sequence != manifest.Sequence {
		t.Fatalf("the hold records sequence %d, want %d", record.Sequence, manifest.Sequence)
	}
	if record.DetectedAt.IsZero() {
		t.Fatal("the hold records no detection time, so nothing can say how long it has waited")
	}

	contested := onlyContested(t, record)
	if contested.Path != "Alpha.md" || contested.Kind != ConflictConcurrentEdit {
		t.Fatalf("held %q as %q, want a concurrent edit on Alpha.md", contested.Path, contested.Kind)
	}
	requireBranch(t, r, manifest.PackageID, contested.Remote, alphaBody, "the remote branch")
	requireBranch(t, r, manifest.PackageID, contested.Local, localAlphaBody, "the local branch")
	requireBranch(t, r, manifest.PackageID, contested.Baseline, baseline, "the baseline")

	if got := r.pair.Local.Read(t, "Alpha.md"); got != localAlphaBody {
		t.Fatalf("the contested note holds %q, want the local edit untouched", got)
	}
	requireHeldQuietly(t, r, head, commits)
}

// TestApplyHoldsADeletionThisVaultHasEditedSince is the delete/edit race. The
// peer removed a note this vault has since changed, so applying the tombstone
// would delete the only copy of an edit nothing else holds.
func TestApplyHoldsADeletionThisVaultHasEditedSince(t *testing.T) {
	r := enroll(t)
	baseline := r.pair.Local.Read(t, "Notes/Gamma.md")
	packaged := r.believes(t, "Notes/Gamma.md")
	r.pair.Local.Write(t, "Notes/Gamma.md", localGammaBody)

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Delete("Notes/Gamma.md", packaged)
	})

	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying a tombstone onto a note edited here")

	contested := onlyContested(t, heldOn(t, r, manifest.PackageID))
	if contested.Path != "Notes/Gamma.md" || contested.Kind != ConflictDeleteEdit {
		t.Fatalf("held %q as %q, want a delete/edit race on Notes/Gamma.md", contested.Path, contested.Kind)
	}
	// A deletion is a branch, and it is not an unknown one: the record says the
	// peer's side of this path is "gone" rather than declining to answer.
	requireAbsentBranch(t, contested.Remote, "the remote branch of a deletion")
	requireBranch(t, r, manifest.PackageID, contested.Local, localGammaBody, "the local branch")
	requireBranch(t, r, manifest.PackageID, contested.Baseline, baseline, "the baseline")

	if got := r.pair.Local.Read(t, "Notes/Gamma.md"); got != localGammaBody {
		t.Fatalf("the contested note holds %q, want the local edit untouched", got)
	}
}

// TestApplyHoldsAWriteOntoANoteThisVaultDeleted is the mirror. Applying it would
// resurrect a note somebody removed on purpose, which no expectation the package
// carries can distinguish from an ordinary create.
func TestApplyHoldsAWriteOntoANoteThisVaultDeleted(t *testing.T) {
	r := enroll(t)
	baseline := r.pair.Local.Read(t, "Notes/Gamma.md")
	packaged := r.believes(t, "Notes/Gamma.md")
	if err := os.Remove(r.pair.Local.Path("Notes/Gamma.md")); err != nil {
		t.Fatalf("delete the note locally: %v", err)
	}

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Gamma.md", []byte(peerGammaBody), packaged)
	})

	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying a write onto a note deleted here")

	contested := onlyContested(t, heldOn(t, r, manifest.PackageID))
	if contested.Path != "Notes/Gamma.md" || contested.Kind != ConflictEditDelete {
		t.Fatalf("held %q as %q, want an edit/delete race on Notes/Gamma.md", contested.Path, contested.Kind)
	}
	requireBranch(t, r, manifest.PackageID, contested.Remote, peerGammaBody, "the remote branch")
	requireAbsentBranch(t, contested.Local, "the local branch of a deleted note")
	requireBranch(t, r, manifest.PackageID, contested.Baseline, baseline, "the baseline")

	if exists(t, r.pair.Local, "Notes/Gamma.md") {
		t.Fatal("a held import resurrected the deleted note")
	}
}

// TestApplyHoldsARenameThisVaultDivergedFrom covers the case a per-path
// comparison gets wrong on its own. The source of the rename is gone here, which
// reads as "already done"; it is only by asking about the *destination* that the
// vault can tell an identical rename from this vault having moved the same note
// somewhere else entirely.
func TestApplyHoldsARenameThisVaultDivergedFrom(t *testing.T) {
	r := enroll(t)
	moved := r.pair.Local.Read(t, "Alpha.md")
	packaged := r.believes(t, "Alpha.md")

	// This vault moved the same note under a name of its own choosing.
	r.pair.Local.Write(t, "Archive/Alpha.md", moved)
	if err := os.Remove(r.pair.Local.Path("Alpha.md")); err != nil {
		t.Fatalf("move the note locally: %v", err)
	}

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Delete("Alpha.md", packaged)
		b.Write("Notes/Alpha.md", []byte(moved), vaultsync.Absent())
	})

	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying a rename of a note this vault moved elsewhere")

	contested := onlyContested(t, heldOn(t, r, manifest.PackageID))
	if contested.Path != "Alpha.md" || contested.Kind != ConflictDivergentRename {
		t.Fatalf("held %q as %q, want a divergent rename of Alpha.md", contested.Path, contested.Kind)
	}
	if contested.RenamedTo != "Notes/Alpha.md" {
		t.Fatalf("the hold names %q as the destination, want Notes/Alpha.md", contested.RenamedTo)
	}
	// Both live branches of a rename are absences — the note is gone from the
	// source here and was never created at the destination — so the baseline is
	// the only content there is to keep, and the package itself is carrying it.
	requireAbsentBranch(t, contested.Remote, "the remote branch of a rename source")
	requireAbsentBranch(t, contested.Local, "the local branch of a moved note")
	requireBranch(t, r, manifest.PackageID, contested.Baseline, moved, "the baseline")

	if exists(t, r.pair.Local, "Notes/Alpha.md") {
		t.Fatal("a held rename created a second copy under the peer's chosen name")
	}
	if got := r.pair.Local.Read(t, "Archive/Alpha.md"); got != moved {
		t.Fatalf("this vault's own copy holds %q, want it untouched", got)
	}
}

// TestApplyDoesNotCallItADivergentRenameWhenTheDestinationIsTaken is the
// boundary of the rule above. The source of the rename is gone here too, so the
// signature looks the same from the source alone — but the destination is
// occupied, by content this vault put there and the peer never saw.
//
// That is a real disagreement and it is the destination's own change that says
// so. Reporting the source as a divergent rename as well would hold one
// operation twice for one event, and would tell a person this vault moved the
// note somewhere unknown while it is plainly sitting at the name the package
// chose.
func TestApplyDoesNotCallItADivergentRenameWhenTheDestinationIsTaken(t *testing.T) {
	r := enroll(t)
	moved := r.pair.Local.Read(t, "Alpha.md")
	packaged := r.believes(t, "Alpha.md")

	// This vault made its own move to the very name the peer chose, carrying
	// content of its own rather than the bytes the peer is sending.
	r.pair.Local.Write(t, "Notes/Alpha.md", localAlphaBody)
	if err := os.Remove(r.pair.Local.Path("Alpha.md")); err != nil {
		t.Fatalf("move the note locally: %v", err)
	}

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Delete("Alpha.md", packaged)
		b.Write("Notes/Alpha.md", []byte(moved), vaultsync.Absent())
	})

	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying a rename onto a destination this vault has filled")

	contested := onlyContested(t, heldOn(t, r, manifest.PackageID))
	if contested.Path != "Notes/Alpha.md" || contested.Kind != ConflictConcurrentEdit {
		t.Fatalf("held %q as %q, want a concurrent edit on Notes/Alpha.md", contested.Path, contested.Kind)
	}
	if contested.RenamedTo != "" {
		t.Fatalf("the hold names %q as a rename destination, want none", contested.RenamedTo)
	}
	requireBranch(t, r, manifest.PackageID, contested.Remote, moved, "the remote branch")
	requireBranch(t, r, manifest.PackageID, contested.Local, localAlphaBody, "the local branch")
	requireAbsentBranch(t, contested.Baseline, "the baseline of a path the peer believed was free")

	if exists(t, r.pair.Local, "Alpha.md") {
		t.Fatal("a held import restored the note at the source it was moved from")
	}
	if got := r.pair.Local.Read(t, "Notes/Alpha.md"); got != localAlphaBody {
		t.Fatalf("the destination holds %q, want this vault's own content untouched", got)
	}
}

// TestApplyDoesNotGuessWhichCopyARenameMeantWhenThereAreTwo is the limit of
// recovering a rename from a tombstone and a creation of the same bytes.
//
// One operation that removes a note and creates its content at two names — a
// note moved and also duplicated in one commit — offers two equally good
// matches, and picking one is a coin toss decided by the manifest's ordering.
// The unpaired tombstone is judged as the deletion it plainly is, which is the
// answer that is merely incomplete rather than the answer that is wrong about
// which note went where: here the note is already gone locally, so the deletion
// has converged and both copies land.
func TestApplyDoesNotGuessWhichCopyARenameMeantWhenThereAreTwo(t *testing.T) {
	r := enroll(t)
	moved := r.pair.Local.Read(t, "Alpha.md")
	packaged := r.believes(t, "Alpha.md")
	// Removed and committed rather than merely deleted: an unexplained missing
	// tracked path is what the Git preflight exists to refuse, and this test is
	// about the rename pairing rather than about that.
	r.pair.Local.Git(t, "rm", "--quiet", "Alpha.md")
	r.pair.Local.Commit(t, "remove the note this vault no longer wants")

	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Delete("Alpha.md", packaged)
		b.Write("Notes/Alpha.md", []byte(moved), vaultsync.Absent())
		b.Write("Archive/Alpha.md", []byte(moved), vaultsync.Absent())
	})

	r.mustApply(t, dir)

	if state := importState(t, r); len(state.Conflicts) != 0 {
		t.Fatalf("a duplicated creation was held as a divergent rename: %v", state.Conflicts)
	}
	for _, rel := range []string{"Notes/Alpha.md", "Archive/Alpha.md"} {
		if got := r.pair.Local.Read(t, rel); got != moved {
			t.Fatalf("%s holds %q, want the moved note", rel, got)
		}
	}
	if exists(t, r.pair.Local, "Alpha.md") {
		t.Fatal("the tombstone was paired with a creation instead of being applied")
	}
}

// TestAHoldDropsTheRetainedVersionsItNoLongerNames is what keeps a conflict that
// nobody resolves from growing without bound.
//
// A hold is re-decided on every pass and the local branch is re-read from the
// vault each time, so a person editing the contested note while they think about
// it produces a new local version per edit. The sweep cannot collect them: the
// record still names the package, so the directory is correctly skipped. The
// record is the only thing that knows which copies it has stopped pointing at.
func TestAHoldDropsTheRetainedVersionsItNoLongerNames(t *testing.T) {
	r := enroll(t)
	packaged := r.believes(t, "Alpha.md")
	r.pair.Local.Write(t, "Alpha.md", localAlphaBody)

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), packaged)
	})

	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying a package onto a note edited here")
	superseded := onlyContested(t, heldOn(t, r, manifest.PackageID)).Local
	if superseded.Bytes == "" {
		t.Fatal("the first hold kept no local branch, so there is nothing for the second to supersede")
	}

	// The person edits the contested note again while the conflict waits, and the
	// transport delivers the package a second time.
	reconsidered := localAlphaBody + "\nA further edit, made while the conflict waited.\n"
	r.pair.Local.Write(t, "Alpha.md", reconsidered)
	_, err = r.apply(t, dir)
	requireIs(t, err, ErrConflict, "re-applying a package onto a note edited again")

	contested := onlyContested(t, heldOn(t, r, manifest.PackageID))
	requireBranch(t, r, manifest.PackageID, contested.Local, reconsidered, "the local branch after the second edit")

	kept := retainedFiles(t, r, manifest.PackageID)
	want := sortedNames(contested.Remote.Bytes, contested.Local.Bytes, contested.Baseline.Bytes)
	requireEqual(t, kept, want, "the retained branches after the second hold")
	for _, name := range kept {
		if name == superseded.Bytes {
			t.Fatalf("the superseded local branch %s is still retained", superseded.Bytes)
		}
	}
}
