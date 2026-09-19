package vaultimport

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// pendingFor is the inspection's answer about one package, or a failure naming
// what it did answer about.
func pendingFor(t *testing.T, root, packageID string) Pending {
	t.Helper()
	found, err := Inspect(root)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	for _, entry := range found {
		if entry.PackageID == packageID {
			return entry
		}
	}
	t.Fatalf("the inspection did not report %s; it reported %+v", packageID, found)
	return Pending{}
}

// TestInspectReportsAHeldConflictWithoutTouchingTheVault is the claim a status
// surface rests on: the conflict a drain recorded is readable again afterwards,
// with the contested paths and their branches, by a reader that applies nothing.
func TestInspectReportsAHeldConflictWithoutTouchingTheVault(t *testing.T) {
	r := enroll(t)
	manifest, _ := contest(t, r)
	before := r.pair.Local.Head(t)

	entry := pendingFor(t, r.root(), manifest.PackageID)
	if entry.Reason != HoldConflict {
		t.Fatalf("the held conflict reads as %q: %+v", entry.Reason, entry)
	}
	if entry.Conflict == nil {
		t.Fatal("a held conflict was reported without the record that says which paths are contested")
	}
	if len(entry.Conflict.Paths) != 1 || entry.Conflict.Paths[0].Path != "Alpha.md" {
		t.Fatalf("the contested set is %+v, want Alpha.md alone", entry.Conflict.Paths)
	}
	if entry.OriginDevice != manifest.OriginDevice || entry.Sequence != manifest.Sequence {
		t.Fatalf("the package's position reads as %s/%d, want %s/%d",
			entry.OriginDevice, entry.Sequence, manifest.OriginDevice, manifest.Sequence)
	}
	if after := r.pair.Local.Head(t); after != before {
		t.Fatalf("inspecting the channel committed something: %s became %s", before, after)
	}
	if dirty := dirtyInScope(t, r.pair.Local); len(dirty) != 1 {
		// The one expected entry is the local divergence contest left in the working
		// tree. Anything else is the inspection having written to the vault.
		t.Fatalf("inspecting the channel changed the working tree: %v", dirty)
	}
}

// TestInspectNamesThePackageADependentIsWaitingOn is what "resolve this and
// these arrive with it" is built from. The dependent's blocker is carried as an
// identity rather than only inside the sentence, so a caller can invert the
// relation instead of parsing English.
func TestInspectNamesThePackageADependentIsWaitingOn(t *testing.T) {
	r := enroll(t)
	manifest, _ := contest(t, r)

	descendant := r.build(t)
	descendant.BeginOperation("", r.clock.Tick())
	descendant.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent(), manifest.Operations[0].OperationID)
	later, _ := r.publish(t, descendant)

	if _, err := Drain(r.root(), Options{Command: "bn sync drain --test", Now: r.clock.Now}); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	dependent := pendingFor(t, r.root(), later.PackageID)
	if dependent.Reason != HoldPredecessor {
		t.Fatalf("the dependent reads as %q: %+v", dependent.Reason, dependent)
	}
	if dependent.Blocker != manifest.PackageID {
		t.Fatalf("the dependent waits on %q, want %q", dependent.Blocker, manifest.PackageID)
	}
	if dependent.Conflict != nil {
		t.Fatal("a package held on a predecessor was given the predecessor's conflict record")
	}
}

// TestInspectCallsAPackageItWouldApplyUnheld separates the two answers a caller
// renders differently: a package waiting for a person, and one waiting only for
// the next drain. Nothing a read-only inspection can see decides whether the
// second will conflict, and claiming otherwise would put a package in front of
// somebody with no decision to make about it.
func TestInspectCallsAPackageItWouldApplyUnheld(t *testing.T) {
	r := enroll(t)
	_, _ = r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})

	found, err := Inspect(r.root())
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("the inspection reported %d packages, want the one delivered: %+v", len(found), found)
	}
	if found[0].Reason != "" {
		t.Fatalf("an applicable delivery reads as held for %q: %+v", found[0].Reason, found[0])
	}
}

// TestInspectSaysAPackageStillArrivingIsUnreadable keeps the wait a transport
// owns out of the surface a person acts on.
func TestInspectSaysAPackageStillArrivingIsUnreadable(t *testing.T) {
	r := enroll(t)
	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})
	if err := os.Remove(filepath.Join(dir, vaultsync.ManifestName)); err != nil {
		t.Fatalf("remove the manifest: %v", err)
	}

	found, err := Inspect(r.root())
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(found) != 1 || found[0].Reason != HoldUnreadable {
		t.Fatalf("a half-copied delivery reads as %+v, want one unreadable package", found)
	}
	if found[0].PackageID != "" {
		t.Fatalf("a package with no readable manifest was given the identity %q", found[0].PackageID)
	}
}

// TestVersionReadsBothVersionsOfAContestedPath is the whole reason the accessor
// is exported: a review surface has to show the peer's bytes, which exist nowhere
// but the copy the hold kept, beside this vault's own version.
func TestVersionReadsBothVersionsOfAContestedPath(t *testing.T) {
	r := enroll(t)
	manifest, _ := contest(t, r)

	entry := pendingFor(t, r.root(), manifest.PackageID)
	contested := entry.Conflict.Paths[0]
	if got := string(Version(r.root(), manifest.PackageID, contested, contested.Remote)); got != alphaBody {
		t.Fatalf("the remote branch reads %q, want the peer's version", got)
	}
	if got := string(Version(r.root(), manifest.PackageID, contested, contested.Local)); got != localAlphaBody {
		t.Fatalf("the local branch reads %q, want this vault's version", got)
	}
}

// TestVersionFollowsTheBranchRatherThanTheDisk is the case a review surface would
// otherwise get wrong. Once somebody edits the contested note, the working tree
// holds a third version, and asking for the branch a resolution was planned
// against has to answer that branch — from the retained copy — rather than
// whatever the file says now.
func TestVersionFollowsTheBranchRatherThanTheDisk(t *testing.T) {
	r := enroll(t)
	manifest, _ := contest(t, r)
	contested := pendingFor(t, r.root(), manifest.PackageID).Conflict.Paths[0]

	if err := os.WriteFile(filepath.Join(r.root(), "Alpha.md"), []byte("a third version\n"), 0o644); err != nil {
		t.Fatalf("edit the contested note: %v", err)
	}
	if got := string(Version(r.root(), manifest.PackageID, contested, contested.Local)); got != localAlphaBody {
		t.Fatalf("the recorded local branch reads %q, want the version the hold kept", got)
	}
	if got := string(Version(r.root(), manifest.PackageID, contested, contested.Remote)); got != alphaBody {
		t.Fatalf("the remote branch reads %q after a local edit, want the peer's version", got)
	}
}

// TestVersionAnswersNothingForBytesThatAreGone pins the answer a review surface
// has to handle rather than render: a branch the record names whose bytes are in
// neither place is a version that cannot be shown, and must not read as an empty
// note somebody might then choose.
func TestVersionAnswersNothingForBytesThatAreGone(t *testing.T) {
	r := enroll(t)
	manifest, _ := contest(t, r)

	entry := pendingFor(t, r.root(), manifest.PackageID)
	contested := entry.Conflict.Paths[0]
	dir, err := conflictDir(r.root(), manifest.PackageID)
	if err != nil {
		t.Fatalf("conflictDir: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, contested.Remote.Bytes)); err != nil {
		t.Fatalf("remove the retained remote branch: %v", err)
	}
	if got := Version(r.root(), manifest.PackageID, contested, contested.Remote); got != nil {
		t.Fatalf("a branch whose bytes are gone answered %q", string(got))
	}
}

// TestVersionRefusesAnIdentityThatIsNotAPackage keeps the accessor's containment
// where conflictDir put it: the directory is derived from a checked identity, so a
// caller that passes a path fragment reaches nothing.
func TestVersionRefusesAnIdentityThatIsNotAPackage(t *testing.T) {
	r := enroll(t)
	manifest, _ := contest(t, r)
	contested := pendingFor(t, r.root(), manifest.PackageID).Conflict.Paths[0]

	if got := Version(r.root(), "../../Alpha.md", contested, contested.Remote); got != nil {
		t.Fatalf("a traversal answered %d bytes", len(got))
	}
}

// collide holds a package whose creation lands on a name a directory occupies,
// which is the one filename collision every filesystem agrees on.
func collide(t *testing.T, r *replicas) *vaultsync.Manifest {
	t.Helper()
	if err := os.Mkdir(r.pair.Local.Path("Notes/Delta.md"), 0o755); err != nil {
		t.Fatalf("occupy the name locally: %v", err)
	}
	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})
	requireIs(t, applyErrorOf(t, r, dir), ErrConflict, "applying a note onto a name a directory holds")
	return manifest
}

// contestRename holds a package that moves a note this vault moved elsewhere.
func contestRename(t *testing.T, r *replicas) *vaultsync.Manifest {
	t.Helper()
	moved := r.pair.Local.Read(t, "Alpha.md")
	packaged := r.believes(t, "Alpha.md")
	r.pair.Local.Write(t, "Archive/Alpha.md", moved)
	if err := os.Remove(r.pair.Local.Path("Alpha.md")); err != nil {
		t.Fatalf("move the note locally: %v", err)
	}
	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Delete("Alpha.md", packaged)
		b.Write("Notes/Alpha.md", []byte(moved), vaultsync.Absent())
	})
	requireIs(t, applyErrorOf(t, r, dir), ErrConflict, "applying a rename of a note this vault moved")
	return manifest
}

// contestDeletion holds a package that removes a note this vault has since
// edited, which is the kind with no incoming version to keep anything beside.
func contestDeletion(t *testing.T, r *replicas) *vaultsync.Manifest {
	t.Helper()
	packaged := r.believes(t, "Notes/Gamma.md")
	r.pair.Local.Write(t, "Notes/Gamma.md", localGammaBody)
	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Delete("Notes/Gamma.md", packaged)
	})
	requireIs(t, applyErrorOf(t, r, dir), ErrConflict, "applying a tombstone onto a note edited here")
	return manifest
}

func applyErrorOf(t *testing.T, r *replicas, packageDir string) error {
	t.Helper()
	_, err := r.apply(t, packageDir)
	return err
}

// offerForPath is the offer about one path, or a failure naming what was offered.
func offerForPath(t *testing.T, root, packageID, rel string) Offer {
	t.Helper()
	offers, err := Offers(root, packageID)
	if err != nil {
		t.Fatalf("Offers: %v", err)
	}
	for _, offer := range offers {
		if offer.Contested.Path == rel {
			return offer
		}
	}
	t.Fatalf("no offer was made about %s; %d were made", rel, len(offers))
	return Offer{}
}

// available reports whether the offer allows a choice.
func (o Offer) available(choice Choice) bool {
	for _, allowed := range o.Available {
		if allowed == choice {
			return true
		}
	}
	return false
}

// TestOffersAllowsEveryChoiceOnAConcurrentEdit is the ordinary case, and it is
// the one that says the probe is asking the real planner: all four fit a
// concurrent edit of a note both replicas hold.
func TestOffersAllowsEveryChoiceOnAConcurrentEdit(t *testing.T) {
	r := enroll(t)
	manifest, _ := contest(t, r)

	offer := offerForPath(t, r.root(), manifest.PackageID, "Alpha.md")
	if len(offer.Available) != len(choiceOrder) {
		t.Fatalf("a concurrent edit offers %v and refuses %v", offer.Available, offer.Refused)
	}
	if !offer.Local.Exists || offer.Local.SHA256 != vaultsync.HashBytes([]byte(localAlphaBody)) {
		t.Fatalf("the offer names this vault's version as %+v", offer.Local)
	}
	if offer.Contested.Remote.SHA256 != vaultsync.HashBytes([]byte(alphaBody)) {
		t.Fatalf("the offer names the peer's version as %+v", offer.Contested.Remote)
	}
}

// TestOffersReadsTheVersionOnTheDiskRatherThanTheOneTheHoldKept is why the offer
// carries its own Local. A person deciding now is deciding about the file as it
// is, and that is also the version Preview pins — so an offer built from the
// hold's reading would show one version and resolve against another.
func TestOffersReadsTheVersionOnTheDiskRatherThanTheOneTheHoldKept(t *testing.T) {
	r := enroll(t)
	manifest, _ := contest(t, r)
	later := "a third version\n"
	if err := os.WriteFile(filepath.Join(r.root(), "Alpha.md"), []byte(later), 0o644); err != nil {
		t.Fatalf("edit the contested note: %v", err)
	}

	offer := offerForPath(t, r.root(), manifest.PackageID, "Alpha.md")
	if offer.Local.SHA256 != vaultsync.HashBytes([]byte(later)) {
		t.Fatalf("the offer names %+v, want the edit that is on the disk now", offer.Local)
	}
	if offer.Contested.Local.SHA256 == offer.Local.SHA256 {
		t.Fatal("the hold's reading and the disk's reading were the same, so this proves nothing")
	}
}

// TestOffersRefusesEveryChoiceOnAFilenameCollision is the refusal a surface must
// render rather than discover: there is no decision to make here, and the offer
// says so with the remedy attached.
func TestOffersRefusesEveryChoiceOnAFilenameCollision(t *testing.T) {
	r := enroll(t)
	manifest := collide(t, r)

	offers, err := Offers(r.root(), manifest.PackageID)
	if err != nil {
		t.Fatalf("Offers: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("a collision offered %d paths: %+v", len(offers), offers)
	}
	offer := offers[0]
	if offer.Contested.Kind != ConflictFilenameCollision {
		t.Fatalf("the hold reads as %s, want a filename collision", offer.Contested.Kind)
	}
	if len(offer.Available) != 0 {
		t.Fatalf("a filename collision offered %v", offer.Available)
	}
	for _, choice := range choiceOrder {
		reason, refused := offer.Refused[choice]
		if !refused || reason == "" {
			t.Fatalf("%s was neither offered nor refused with a reason", choice)
		}
		if strings.Contains(reason, ErrResolutionUnavailable.Error()) {
			t.Fatalf("the reason for %s repeats the sentinel: %q", choice, reason)
		}
	}
}

// TestOffersRefusesADoubleKeepOfTheDeclarations is the one refusal that is about
// meaning rather than about bytes, and the reason the table is asked rather than
// assumed: two copies of the configuration would leave the conflict unresolved
// under a name that looks resolved.
func TestOffersRefusesADoubleKeepOfTheDeclarations(t *testing.T) {
	r := enroll(t)
	manifest := contestConfiguration(t, r)

	offer := offerForPath(t, r.root(), manifest.PackageID, config.FileName)
	if offer.available(KeepBoth) {
		t.Fatalf("a configuration conflict offered keep-both: %+v", offer)
	}
	if offer.Refused[KeepBoth] == "" {
		t.Fatal("keep-both was refused with no reason a person could read")
	}
	if !offer.available(ManualMerge) {
		t.Fatalf("a configuration conflict refused a merge: %q", offer.Refused[ManualMerge])
	}
}

// TestOffersRefusesAMergeOfARename separates the two things a rename is not: it
// is not a pair of versions to merge, and it is not a path with a second copy to
// keep beside it. Both refusals are about the same fact — the note is somewhere
// else here — and a surface that offered the editor would ask somebody to merge
// two names.
func TestOffersRefusesAMergeOfARename(t *testing.T) {
	r := enroll(t)
	manifest := contestRename(t, r)

	offer := offerForPath(t, r.root(), manifest.PackageID, "Alpha.md")
	if offer.Contested.Kind != ConflictDivergentRename {
		t.Fatalf("the hold reads as %s, want a divergent rename", offer.Contested.Kind)
	}
	if offer.available(ManualMerge) || offer.available(KeepBoth) {
		t.Fatalf("a divergent rename offered %v", offer.Available)
	}
	if !offer.available(KeepCurrent) || !offer.available(AcceptIncoming) {
		t.Fatalf("a divergent rename refused the two choices it has: %+v", offer.Refused)
	}
}

// TestOffersRefusesKeepingBothOfADeletion is the deletion half of the same rule.
// The peer removed the note, so there is no incoming version for a second copy to
// sit beside — "both" would be one file under a derived name and nothing under the
// name the note has. A merge is still open, because supplying content is a person
// saying the note should exist and hold this.
func TestOffersRefusesKeepingBothOfADeletion(t *testing.T) {
	r := enroll(t)
	manifest := contestDeletion(t, r)

	offer := offerForPath(t, r.root(), manifest.PackageID, "Notes/Gamma.md")
	if offer.Contested.Kind != ConflictDeleteEdit {
		t.Fatalf("the hold reads as %s, want a delete-edit", offer.Contested.Kind)
	}
	if offer.Contested.Remote.Exists {
		t.Fatalf("the offer says the peer sent a version: %+v", offer.Contested.Remote)
	}
	if !offer.Local.Exists {
		t.Fatalf("the offer cannot name the edit this vault made: %+v", offer.Local)
	}
	if offer.available(KeepBoth) {
		t.Fatalf("a path the peer deleted offered keep-both: %+v", offer)
	}
	if !offer.available(ManualMerge) || !offer.available(KeepCurrent) {
		t.Fatalf("a delete-edit refused a choice it has: %+v", offer.Refused)
	}
}

// TestOffersRefusesAPackageThatIsNotHeld keeps the surface's two questions apart:
// Inspect says what is waiting, and this says what can be decided about one thing
// that is. A package with no hold has no decisions, and answering with an empty
// list would read as a conflict with nothing to do about it.
func TestOffersRefusesAPackageThatIsNotHeld(t *testing.T) {
	r := enroll(t)
	manifest, _ := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})

	if _, err := Offers(r.root(), manifest.PackageID); !errors.Is(err, ErrNoConflict) {
		t.Fatalf("Offers about an unheld package: %v", err)
	}
}
