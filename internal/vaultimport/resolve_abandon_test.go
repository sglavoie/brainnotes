package vaultimport

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultfixture"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// TestAnInterruptedResolutionIsAbandonedWhenNothingWasApplied is the other half
// of the same durability question, and it is the half where finishing would be
// wrong.
//
// The decision is durable before the application starts, so an interruption
// *inside* the application leaves a promise to revise an operation that then
// rolls back. Carrying it out would write somebody's choice on top of a vault
// that never received the change it was a choice about. So it is abandoned and
// the package goes back to being held — which is the only honest offer, since
// the reading the choice was made against is gone.
func TestAnInterruptedResolutionIsAbandonedWhenNothingWasApplied(t *testing.T) {
	vaultfixture.Isolate(t)
	report := interruptResolution(t, crashDeclared)

	// The promise is durable and the application is not: an attempt declared,
	// nothing written, and the note still holding this vault's own version.
	requireState(t, report.Root, map[string]string{"Alpha.md": localAlphaBody}, "after an interrupted application")
	before, err := LoadState(report.Root)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if _, promised := before.Resolutions[report.PackageID]; !promised {
		t.Fatalf("the decision was not durable before the application it revises: %+v", before)
	}
	if before.Pending == nil {
		t.Fatal("the helper died before declaring the application, so this proves nothing about the promise")
	}

	recovery, err := Recover(report.Root, Options{Command: "bn sync recover --test"})
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if recovery.Outcome != OutcomeDiscarded {
		t.Fatalf("recovery reported %q, want the application discarded", recovery.Outcome)
	}
	if len(recovery.Revised) != 0 {
		t.Fatalf("recovery revised %v on top of an application that never landed", recovery.Revised)
	}
	// Abandoning it silently would leave a person to work out from an unchanged
	// note that the choice they made is no longer anywhere, so the package is
	// named and the reason says which of the two abandonments this was.
	if !slices.Contains(recovery.Abandoned, report.PackageID) {
		t.Fatalf("recovery abandoned %v, which does not name %s", recovery.Abandoned, report.PackageID)
	}
	if why := recovery.AbandonedBecause[report.PackageID]; !errors.Is(why, errApplicationNeverLanded) {
		t.Fatalf("the abandonment is reported as %v, not as the application never landing", why)
	}
	requireHoldRestored(t, report)
}

// TestDecidingAgainSaysTheEarlierDecisionWasDropped covers the way that news
// actually reaches somebody.
//
// Every entry point runs the recovery pass, so the person who resolves a
// conflict a second time is usually the one whose first attempt is being
// abandoned — and they never call Recover, so the Recovery carrying the reason
// is made and thrown away inside Resolve. Carrying it onto the Resolution is
// what keeps "the choice you made before was not kept" from being something a
// person has to infer from a note that looks fine.
func TestDecidingAgainSaysTheEarlierDecisionWasDropped(t *testing.T) {
	vaultfixture.Isolate(t)
	report := interruptResolution(t, crashDeclared)

	plan, err := Preview(report.Root, report.PackageID, Choices{Default: KeepCurrent})
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	resolution, err := Resolve(report.Root, report.PackageDir, plan, Options{Command: "bn sync resolve --test"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	why := resolution.AbandonedResolutions[report.PackageID]
	if !errors.Is(why, errApplicationNeverLanded) {
		t.Fatalf("the second decision reports %v about the first, want the application never landing", why)
	}
	// And the decision made now is carried out all the same: the abandonment is
	// news about the past, not a refusal of the present.
	if resolution.JournalEntry == "" {
		t.Fatal("the second decision applied the package without revising it")
	}
	requireState(t, report.Root, map[string]string{"Alpha.md": localAlphaBody}, "after deciding a second time")
}

// TestAnAbandonmentSurvivesTheFailureThatHappenedWithIt is the ordering the two
// halves of an abandonment are written in, asserted where it can go wrong.
//
// Giving a promise up is two steps: the state file is rewritten without it, and
// then the bytes it staged are removed. Only the first makes the abandonment
// real, and once it has happened no later pass ever sees that promise again.
// So a failure in the *second* step must not swallow the news from the first —
// and it very nearly does, because the failure travels as an error and the news
// travels as a return value that every entry point but Recover throws away.
// Here the staged-revision store is made unremovable, which is the one failure
// that fits in that gap, and the decision has to still be named.
func TestAnAbandonmentSurvivesTheFailureThatHappenedWithIt(t *testing.T) {
	vaultfixture.Isolate(t)
	report := interruptResolution(t, crashDeclared)
	sealStagedRevisions(t, report.Root)

	recovery, err := Recover(report.Root, Options{Command: "bn sync recover --test"})
	if err == nil {
		t.Fatal("removing the staged content succeeded, so this proves nothing about the failure path")
	}
	if !slices.Contains(recovery.Abandoned, report.PackageID) {
		t.Fatalf("a failing pass reported %v abandoned, which does not name the decision it dropped", recovery.Abandoned)
	}
	if why := recovery.AbandonedBecause[report.PackageID]; !errors.Is(why, errApplicationNeverLanded) {
		t.Fatalf("the abandonment is reported as %v, not as the application never landing", why)
	}
	// The error is the only carrier the callers that drop the Recovery have left,
	// so it has to carry the reason and not merely the housekeeping. The package
	// name alone would prove nothing — discardStaged's own message already has it.
	if !strings.Contains(err.Error(), errApplicationNeverLanded.Error()) {
		t.Fatalf("the failure reads %q and never says why the decision it took with it was dropped", err)
	}
	// And the promise really is gone, which is what makes losing the news
	// unrecoverable rather than merely untidy.
	state, loadErr := LoadState(report.Root)
	if loadErr != nil {
		t.Fatalf("LoadState: %v", loadErr)
	}
	if _, promised := state.Resolutions[report.PackageID]; promised {
		t.Fatal("the promise survived, so the pass could have reported it again later")
	}
}

// TestDecidingAgainHearsAnAbandonmentThroughTheError is the same fact from the
// door a person actually uses.
//
// Resolve returns no Resolution when its recovery pass fails, so the map naming
// the dropped decision is built and discarded inside the call. Folding it into
// the error is what keeps it reachable.
func TestDecidingAgainHearsAnAbandonmentThroughTheError(t *testing.T) {
	vaultfixture.Isolate(t)
	report := interruptResolution(t, crashDeclared)
	plan, err := Preview(report.Root, report.PackageID, Choices{Default: KeepCurrent})
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	sealStagedRevisions(t, report.Root)

	if _, err := Resolve(report.Root, report.PackageDir, plan, Options{Command: "bn sync resolve --test"}); err == nil {
		t.Fatal("the resolution succeeded, so this proves nothing about the failure path")
	} else if !strings.Contains(err.Error(), report.PackageID) ||
		!strings.Contains(err.Error(), errApplicationNeverLanded.Error()) {
		t.Fatalf("resolving again failed with %q, which never says the earlier decision was dropped", err)
	}
}

// TestAnAbandonmentSurvivesADecisionThatWasRefused is the same news on the path a
// person is likeliest to take, and the one where nothing failed at all.
//
// Both tests above are about a recovery pass that *broke*. This is a pass that
// worked — it gave the interrupted promise up, exactly as designed — followed by a
// refusal of the decision being made now, which is the ordinary answer to a package
// the channel no longer holds. There is no Resolution for an abandonment to travel
// on and nothing wrong for anybody to go looking behind, so a refusal that did not
// carry it is where somebody's earlier choice disappears with no trace anywhere.
func TestAnAbandonmentSurvivesADecisionThatWasRefused(t *testing.T) {
	vaultfixture.Isolate(t)
	report := interruptResolution(t, crashDeclared)
	plan, err := Preview(report.Root, report.PackageID, Choices{Default: KeepCurrent})
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	// The package goes out of the channel between the decision and its being
	// carried out, which is a real race rather than a contrivance: a drain in
	// another process cleans the directory up behind the plan.
	if err := os.RemoveAll(report.PackageDir); err != nil {
		t.Fatalf("remove the package directory: %v", err)
	}

	resolution, err := Resolve(report.Root, report.PackageDir, plan, Options{Command: "bn sync resolve --test"})
	if resolution != nil {
		t.Fatalf("a refused resolution described an outcome: %+v", resolution)
	}
	if err == nil {
		t.Fatal("resolving against a package that is gone succeeded, so this proves nothing")
	}
	if !strings.Contains(err.Error(), report.PackageID) ||
		!strings.Contains(err.Error(), errApplicationNeverLanded.Error()) {
		t.Fatalf("the refusal reads %q and never says the earlier decision was dropped", err)
	}
	// And it really is gone, which is what makes the error the last carrier.
	state, loadErr := LoadState(report.Root)
	if loadErr != nil {
		t.Fatalf("LoadState: %v", loadErr)
	}
	if _, promised := state.Resolutions[report.PackageID]; promised {
		t.Fatal("the promise survived, so a later pass would have reported it again")
	}
}

// sealStagedRevisions makes the staged-revision store unremovable from, which is
// how discardStaged is made to fail: a child cannot be unlinked from a directory
// this process cannot write. The mode is put back for the fixture's own cleanup.
func sealStagedRevisions(t *testing.T, root string) {
	t.Helper()
	dir := resolutionsRoot(root)
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("the staged-revision store is not there to seal: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, info.Mode().Perm()) })
}

// TestAbandonmentIsAnnouncedOnlyOnceItIsTrue is the rule abandonmentOf keeps,
// asserted directly because the failure it guards against — a clearResolution
// that could not save the state file — has no fixture.
//
// Announcing an uncleared promise would be news about something that has not
// happened, and the next pass, which decides it again, would announce it twice.
func TestAbandonmentIsAnnouncedOnlyOnceItIsTrue(t *testing.T) {
	if why := abandonmentOf(true, errApplicationNeverLanded); !errors.Is(why, errApplicationNeverLanded) {
		t.Fatalf("a cleared promise reports %v, want the reason it was given up", why)
	}
	if why := abandonmentOf(false, errApplicationNeverLanded); why != nil {
		t.Fatalf("a promise still in the state file reports %v, want nothing at all", why)
	}
}

// requireHoldRestored is the state an abandoned resolution has to leave: the
// promise gone, the hold back, the note untouched, and the package resolvable
// again from a fresh reading.
func requireHoldRestored(t *testing.T, report crashReport) {
	t.Helper()
	requireState(t, report.Root, map[string]string{"Alpha.md": localAlphaBody}, "after the resolution was abandoned")
	state, err := LoadState(report.Root)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.Resolutions) != 0 {
		t.Fatalf("an abandoned resolution is still promised: %v", state.Resolutions)
	}
	if _, held := state.Conflicts[report.PackageID]; !held {
		t.Fatalf("%s is neither applied nor held, so nothing can ever resolve it", report.PackageID)
	}
	staged, err := resolutionDir(report.Root, report.PackageID)
	if err != nil {
		t.Fatalf("resolutionDir: %v", err)
	}
	if _, err := os.Lstat(staged); !os.IsNotExist(err) {
		t.Fatalf("the abandoned revision's staged content survived (%v)", err)
	}

	// Recoverable means resolvable, not merely tidy: the same conflict answered
	// again from a fresh reading has to go all the way through.
	plan, err := Preview(report.Root, report.PackageID, Choices{Default: KeepCurrent})
	if err != nil {
		t.Fatalf("previewing an abandoned resolution again: %v", err)
	}
	resolution, err := Resolve(report.Root, report.PackageDir, plan, Options{Command: "bn sync resolve --test"})
	if err != nil {
		t.Fatalf("resolving after an abandoned attempt: %v", err)
	}
	if resolution.JournalEntry == "" {
		t.Fatal("the second attempt applied the package without revising it")
	}
	requireState(t, report.Root, map[string]string{"Alpha.md": localAlphaBody}, "after resolving a second time")
}

// TestTheRevisionNamesTheResolvedOperationAsItsPredecessor is what convergence
// rests on, asserted at the wire rather than inferred from the outcome.
//
// The peer applies the revision with nothing to decide for two reasons, and
// both are facts about the package this vault publishes: the change expects the
// content the peer itself sent (so its disk already matches), and it names the
// resolved operation as its causal predecessor (so a replica that has not seen
// that operation waits for it instead of calling the revision a conflict).
// Convergence tests would still pass if either were wrong in a way the other
// covered, which is why this one reads the manifest.
func TestTheRevisionNamesTheResolvedOperationAsItsPredecessor(t *testing.T) {
	r := enroll(t)
	r.baseline(t)
	manifest, dir := contest(t, r)

	plan := previewed(t, r, manifest.PackageID, Choices{Default: KeepCurrent})
	resolution := resolved(t, r, dir, plan)

	published := r.exportToPeer(t)
	if len(published) != 1 {
		t.Fatalf("the resolution published %d packages, want only the revision", len(published))
	}
	sent, err := vaultsync.ReadManifest(published[0].Dir)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if len(sent.Operations) != 1 || len(sent.Operations[0].Changes) != 1 {
		t.Fatalf("the revision travelled as %+v, want one change of one operation", sent.Operations)
	}
	change := sent.Operations[0].Changes[0]
	if change.Path != "Alpha.md" {
		t.Fatalf("the revision carries %q, want the contested note", change.Path)
	}
	requireEqual(t, change.Predecessors, []string{resolution.OperationID},
		"the revision's causal predecessors")

	// The version it expects is the peer's own, which is what the application
	// underneath the resolution put on this disk.
	if !change.ExpectExists || change.ExpectSHA256 != vaultsync.HashBytes([]byte(alphaBody)) {
		t.Fatalf("the revision expects exists=%v %s, want the peer's own version of Alpha.md",
			change.ExpectExists, change.ExpectSHA256)
	}
}

// TestResolveRefusesAPackageThePlanIsNotAbout is the pairing nothing else
// checks. The promise is keyed on the plan's package and the application reads
// its manifest out of the directory it is handed, so a caller that crosses the
// two would apply one package under a decision made about another — and a
// resolution replaces the producer's expectations with the branches its own
// plan pinned, which makes that an overwrite nobody authorized.
func TestResolveRefusesAPackageThePlanIsNotAbout(t *testing.T) {
	r := enroll(t)
	manifest, _ := contest(t, r)
	plan := previewed(t, r, manifest.PackageID, Choices{Default: KeepCurrent})

	// A second package from the same peer, delivered while the conflict is held
	// and contesting nothing at all.
	_, other := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})
	head := r.pair.Local.Head(t)
	commits := commitCount(t, r.pair.Local)

	resolution, err := Resolve(r.root(), other, plan, r.resolveOptions())
	requireIs(t, err, ErrResolutionUnavailable, "resolving one package under another package's decision")
	if resolution != nil {
		t.Fatalf("the mismatched resolution reported %+v, want nothing applied", resolution)
	}
	if exists(t, r.pair.Local, "Notes/Delta.md") {
		t.Fatal("the mismatched package was applied under a decision about a different one")
	}
	requirePreviewChangedNothingBut(t, r, manifest.PackageID, head, commits)
}
