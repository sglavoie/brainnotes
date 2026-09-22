package vaultimport

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// The bodies the scheduling tests move around. Two successive versions of one
// note are what an ordering claim is made of: applying them in the wrong order
// is not merely untidy, it reports a conflict where there is none, because the
// second edit's baseline is the first edit's result.
const (
	betaOnceBody   = "---\nstatus: review\ntitle: Beta\n---\n\nBeta body, first edit on the peer.\n"
	betaTwiceBody  = "---\nstatus: done\ntitle: Beta\n---\n\nBeta body, second edit on the peer.\n"
	betaThriceBody = "---\nstatus: done\ntitle: Beta\n---\n\nBeta body, third edit on the peer.\n"
	// localBetaBody is what this vault holds while both of those are in the
	// channel, which is what makes the first of them a conflict.
	localBetaBody = "---\nstatus: draft\ntitle: Beta\n---\n\nBeta body, edited here and not committed.\n"
)

// TestDrainAppliesIndependentPackagesWhileOneIsHeld is the half of the session's
// scope that is easy to get wrong in the safe direction. Stopping the whole
// drain at the first conflict would be simple and correct about content, and one
// contested note would keep a hundred unrelated ones out of the vault for as
// long as nobody looked.
func TestDrainAppliesIndependentPackagesWhileOneIsHeld(t *testing.T) {
	r := enroll(t)
	packaged := r.believes(t, "Alpha.md")
	r.pair.Local.Write(t, "Alpha.md", localAlphaBody)

	contested, _ := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), packaged)
	})
	created, _ := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})
	edited, _ := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Beta.md", []byte(betaOnceBody), r.believes(t, "Beta.md"))
	})

	drained := mustDrain(t, r)

	requireEqual(t, appliedIDs(drained), []string{created.PackageID, edited.PackageID},
		"the packages the drain applied")
	if len(drained.Held) != 1 {
		t.Fatalf("the drain held %v, want only the contested package", heldIDs(drained))
	}
	held := drained.Held[0]
	if held.PackageID != contested.PackageID || held.Reason != HoldConflict {
		t.Fatalf("held %s for %q, want %s held as a conflict", held.PackageID, held.Reason, contested.PackageID)
	}
	if held.Conflict == nil || len(held.Conflict.Paths) != 1 {
		t.Fatalf("the report carries %+v, want the contested path and its branches", held.Conflict)
	}
	if held.Conflict.Paths[0].Kind != ConflictConcurrentEdit {
		t.Fatalf("the report calls it %q, want a concurrent edit", held.Conflict.Paths[0].Kind)
	}

	if got := r.pair.Local.Read(t, "Notes/Delta.md"); got != deltaBody {
		t.Fatalf("the independent creation holds %q", got)
	}
	if got := r.pair.Local.Read(t, "Beta.md"); got != betaOnceBody {
		t.Fatalf("the independent edit holds %q", got)
	}
	if got := r.pair.Local.Read(t, "Alpha.md"); got != localAlphaBody {
		t.Fatalf("the contested note holds %q, want the local edit untouched", got)
	}
}

// TestDrainAppliesAnOutOfOrderDeliveryInTheProducersOrder is the other half:
// out-of-order delivery cannot lose a dependency. A channel is a directory and a
// directory has no order, so the later edit is published first here on purpose —
// applying it against a vault that never saw the first would refuse a package
// nothing is wrong with.
func TestDrainAppliesAnOutOfOrderDeliveryInTheProducersOrder(t *testing.T) {
	r := enroll(t)

	first := r.build(t)
	first.BeginOperation("", r.clock.Now())
	first.Write("Beta.md", []byte(betaOnceBody), r.believes(t, "Beta.md"))

	second := r.build(t)
	second.BeginOperation("", r.clock.Now())
	second.Write("Beta.md", []byte(betaTwiceBody), vaultsync.Existing(vaultsync.HashBytes([]byte(betaOnceBody))))

	// Published backwards: the transport moved the second package first.
	later, _ := r.publish(t, second)
	earlier, _ := r.publish(t, first)

	drained := mustDrain(t, r)

	requireEqual(t, appliedIDs(drained), []string{earlier.PackageID, later.PackageID},
		"the order the drain applied in")
	if len(drained.Held) != 0 {
		t.Fatalf("the drain held %v, want nothing", heldIDs(drained))
	}
	if got := r.pair.Local.Read(t, "Beta.md"); got != betaTwiceBody {
		t.Fatalf("the note holds %q, want the second edit", got)
	}
}

// TestDrainHoldsAPackageWhoseStatedDependencyHasNotArrived covers the producer's
// own claim about what must come first, and the position is matched exactly
// rather than against a high-water mark: a vault that has applied 1 and 3 cannot
// tell whether 2 was skipped or has not arrived.
func TestDrainHoldsAPackageWhoseStatedDependencyHasNotArrived(t *testing.T) {
	r := enroll(t)

	// Position 1 of the peer's stream is published nowhere; the transport has not
	// delivered it and may never.
	r.sequence++
	dependent := r.build(t)
	dependent.Require(r.peer.DeviceID, 1)
	dependent.BeginOperation("", r.clock.Now())
	dependent.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	waiting, _ := r.publish(t, dependent)

	independent, _ := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Beta.md", []byte(betaOnceBody), r.believes(t, "Beta.md"))
	})

	drained := mustDrain(t, r)

	requireEqual(t, appliedIDs(drained), []string{independent.PackageID}, "the packages the drain applied")
	if len(drained.Held) != 1 || drained.Held[0].PackageID != waiting.PackageID {
		t.Fatalf("the drain held %v, want only %s", heldIDs(drained), waiting.PackageID)
	}
	if drained.Held[0].Reason != HoldDependency {
		t.Fatalf("held for %q, want a stated dependency", drained.Held[0].Reason)
	}
	if exists(t, r.pair.Local, "Notes/Delta.md") {
		t.Fatal("a package with an unmet dependency was applied anyway")
	}
}

// TestDrainSatisfiesAStatedDependencyWithinOnePass is why the pass is a
// fixed-point loop rather than a single sweep. Applying one package satisfies
// what another was waiting on, and re-deciding is cheap next to leaving the
// second one in the channel until somebody runs the command again.
func TestDrainSatisfiesAStatedDependencyWithinOnePass(t *testing.T) {
	r := enroll(t)

	first := r.build(t)
	first.BeginOperation("", r.clock.Now())
	first.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())

	second := r.build(t)
	second.Require(r.peer.DeviceID, 1)
	second.BeginOperation("", r.clock.Now())
	second.Write("Beta.md", []byte(betaOnceBody), r.believes(t, "Beta.md"))

	dependent, _ := r.publish(t, second)
	required, _ := r.publish(t, first)

	drained := mustDrain(t, r)

	requireEqual(t, appliedIDs(drained), []string{required.PackageID, dependent.PackageID},
		"the order the drain applied in")
	if len(drained.Held) != 0 {
		t.Fatalf("the drain held %v, want nothing", heldIDs(drained))
	}
	if got := r.pair.Local.Read(t, "Beta.md"); got != betaOnceBody {
		t.Fatalf("the dependent package's note holds %q", got)
	}
}

// TestDrainHoldsTheDependentsOfAHeldOperation is the session's scope sentence in
// one test: a conflict holds its own operation and everything downstream of it,
// and nothing else.
func TestDrainHoldsTheDependentsOfAHeldOperation(t *testing.T) {
	r := enroll(t)
	packaged := r.believes(t, "Alpha.md")
	r.pair.Local.Write(t, "Alpha.md", localAlphaBody)

	operationID, err := vaultsync.NewOperationID()
	if err != nil {
		t.Fatalf("NewOperationID: %v", err)
	}
	upstream := r.build(t)
	upstream.BeginOperation(operationID, r.clock.Now())
	upstream.Write("Alpha.md", []byte(alphaBody), packaged)
	contested, _ := r.publish(t, upstream)

	// A later change on an entirely different path, which the producer says
	// follows the contested operation.
	downstream := r.build(t)
	downstream.BeginOperation("", r.clock.Now())
	downstream.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent(), operationID)
	follower, _ := r.publish(t, downstream)

	independent, _ := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Beta.md", []byte(betaOnceBody), r.believes(t, "Beta.md"))
	})

	drained := mustDrain(t, r)

	requireEqual(t, appliedIDs(drained), []string{independent.PackageID}, "the packages the drain applied")
	requireEqual(t, heldIDs(drained), []string{contested.PackageID, follower.PackageID},
		"the packages the drain held")
	requireHeldFor(t, drained, contested.PackageID, HoldConflict)
	requireHeldFor(t, drained, follower.PackageID, HoldPredecessor)

	if exists(t, r.pair.Local, "Notes/Delta.md") {
		t.Fatal("a package following a held operation was applied anyway")
	}
	if got := r.pair.Local.Read(t, "Beta.md"); got != betaOnceBody {
		t.Fatalf("the independent package's note holds %q", got)
	}
}

// TestDrainWaitsForAPackageStillArriving covers the commonest thing in a live
// channel that is not a package: a directory the transport is still filling. It
// is held rather than reported as broken, and it decides nothing about where the
// readable packages went.
func TestDrainWaitsForAPackageStillArriving(t *testing.T) {
	r := enroll(t)
	ready, _ := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})

	arriving, err := vaultsync.NewPackageID()
	if err != nil {
		t.Fatalf("NewPackageID: %v", err)
	}
	partial := filepath.Join(vaultsync.IncomingDir(r.root()), arriving)
	if err := os.Mkdir(partial, 0o755); err != nil {
		t.Fatalf("stage a half-copied package: %v", err)
	}

	drained := mustDrain(t, r)

	requireEqual(t, appliedIDs(drained), []string{ready.PackageID}, "the packages the drain applied")
	if len(drained.Held) != 1 {
		t.Fatalf("the drain held %d packages, want the one still arriving", len(drained.Held))
	}
	held := drained.Held[0]
	if held.Reason != HoldUnreadable || held.Dir != partial {
		t.Fatalf("held %+v, want %q reported as unreadable", held, partial)
	}
	if held.PackageID != "" {
		t.Fatalf("an unreadable package was reported under identity %q", held.PackageID)
	}
}

// TestDrainForgetsAHoldWhosePackageLeftTheChannel is the sweep. A record
// describing a conflict whose remote branch is gone cannot be acted on, and the
// retained bytes behind it are a directory nothing points at — neither is
// dangerous and both grow without bound.
func TestDrainForgetsAHoldWhosePackageLeftTheChannel(t *testing.T) {
	r := enroll(t)
	packaged := r.believes(t, "Alpha.md")
	r.pair.Local.Write(t, "Alpha.md", localAlphaBody)

	contested, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), packaged)
	})

	if drained := mustDrain(t, r); len(drained.Held) != 1 {
		t.Fatalf("the first drain held %v, want the contested package", heldIDs(drained))
	}
	heldOn(t, r, contested.PackageID)
	retained, err := conflictDir(r.root(), contested.PackageID)
	if err != nil {
		t.Fatalf("conflictDir: %v", err)
	}
	if _, err := os.Lstat(retained); err != nil {
		t.Fatalf("the retained branches are not on disk: %v", err)
	}

	// The package is withdrawn: the peer was restored from a backup, or somebody
	// emptied the channel by hand.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("withdraw the package: %v", err)
	}

	drained := mustDrain(t, r)
	if len(drained.Applied) != 0 || len(drained.Held) != 0 {
		t.Fatalf("the second drain applied %v and held %v, want an empty channel",
			appliedIDs(drained), heldIDs(drained))
	}
	if state := importState(t, r); len(state.Conflicts) != 0 {
		t.Fatalf("the hold outlived the package it was about: %v", state.Conflicts)
	}
	if _, err := os.Lstat(retained); !os.IsNotExist(err) {
		t.Fatalf("the retained branches outlived the hold (%v)", err)
	}
}

// TestDrainHoldsALaterPackageThatChangesAHeldPackagesPath is the backstop the
// other three rules cannot state.
//
// Two packages edit one note in succession and neither says so: the second
// carries no stated dependency, and its change names a predecessor this vault
// has no package for. The first is held on a conflict, and applying the second
// anyway would compare the later edit against a baseline this vault has not
// reached — reporting a second conflict where there is only one, and inviting a
// person to resolve a disagreement that the first resolution makes disappear.
func TestDrainHoldsALaterPackageThatChangesAHeldPackagesPath(t *testing.T) {
	r := enroll(t)
	packaged := r.believes(t, "Beta.md")
	r.pair.Local.Write(t, "Beta.md", localBetaBody)

	contested, _ := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Beta.md", []byte(betaOnceBody), packaged)
	})
	later, _ := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Beta.md", []byte(betaTwiceBody), vaultsync.Existing(vaultsync.HashBytes([]byte(betaOnceBody))))
	})

	drained := mustDrain(t, r)

	if len(drained.Applied) != 0 {
		t.Fatalf("the drain applied %v, want nothing", appliedIDs(drained))
	}
	requireHeldFor(t, drained, contested.PackageID, HoldConflict)
	requireHeldFor(t, drained, later.PackageID, HoldOrder)

	// Held on order means never attempted, which is the point: the vault has
	// written down one disagreement rather than two, and only the one that is real.
	state := importState(t, r)
	if _, held := state.Conflicts[later.PackageID]; held {
		t.Fatalf("the later package was inspected and held as a conflict: %v", state.Conflicts[later.PackageID])
	}
	if got := r.pair.Local.Read(t, "Beta.md"); got != localBetaBody {
		t.Fatalf("the contested note holds %q, want the local edit untouched", got)
	}
}

// TestDrainAppliesAPackageTheOrderRuleWouldHoldOnItsFollower is the stall the
// total order and the causal edges produce between them, and the one this
// scheduler is least allowed to have: nothing clears it. No delivery, no
// resolution, no second run.
//
// The scheduler needs exactly two facts to reach it: one package sorts before
// another, and the earlier one causally follows the later. In production they
// arrive together when a peer imports a third device's operation and then edits
// the same note, since origin device sorts first and sorts lexically. This
// fixture has two devices, so the same pair is made with one origin whose
// positions are handed to the channel out of order — what the rules see is
// identical, and building the lexical version would be asserting about the
// fixture's device identities rather than about the rules.
//
// The follower is held on its predecessor; the predecessor is held for changing
// a path an "earlier" package changes; neither can ever leave. Causality wins,
// because it is a claim the producer made about two events where the total order
// is a tie-break this package invented.
func TestDrainAppliesAPackageTheOrderRuleWouldHoldOnItsFollower(t *testing.T) {
	r := enroll(t)
	operationID := mustOperationID(t)

	// The follower takes the earlier position in the producer's stream, so the
	// drain's own total order puts it first.
	follower := r.build(t)
	follower.BeginOperation("", r.clock.Now())
	follower.Write("Beta.md", []byte(betaTwiceBody),
		vaultsync.Existing(vaultsync.HashBytes([]byte(betaOnceBody))), operationID)
	second, _ := r.publish(t, follower)

	followed := r.build(t)
	followed.BeginOperation(operationID, r.clock.Now())
	followed.Write("Beta.md", []byte(betaOnceBody), r.believes(t, "Beta.md"))
	first, _ := r.publish(t, followed)

	drained := mustDrain(t, r)

	requireEqual(t, appliedIDs(drained), []string{first.PackageID, second.PackageID},
		"the order the drain applied in")
	if len(drained.Held) != 0 {
		t.Fatalf("the drain held %v, want nothing", heldIDs(drained))
	}
	if got := r.pair.Local.Read(t, "Beta.md"); got != betaTwiceBody {
		t.Fatalf("the note holds %q, want the follower's edit", got)
	}
}

// TestDrainKeepsAHoldWhosePackageIsBeingRewritten is the sweep's other half, and
// the one that is destructive if it is wrong.
//
// The sweep asks which packages are still in the channel, and a package the
// transport is halfway through rewriting is in the channel while being unreadable
// — it has no manifest for as long as the copy takes. Asking for its *identity*
// would answer the empty string, the hold would look withdrawn, and the local
// branch would be collected: the one version of the contested content that exists
// nowhere else, removed because a file was being copied.
func TestDrainKeepsAHoldWhosePackageIsBeingRewritten(t *testing.T) {
	r := enroll(t)
	packaged := r.believes(t, "Alpha.md")
	r.pair.Local.Write(t, "Alpha.md", localAlphaBody)

	contested, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), packaged)
	})

	if drained := mustDrain(t, r); len(drained.Held) != 1 {
		t.Fatalf("the first drain held %v, want the contested package", heldIDs(drained))
	}
	heldOn(t, r, contested.PackageID)
	retained, err := conflictDir(r.root(), contested.PackageID)
	if err != nil {
		t.Fatalf("conflictDir: %v", err)
	}

	// The transport re-delivers the package over itself, and this drain runs while
	// the manifest is not there yet.
	if err := os.Remove(filepath.Join(dir, vaultsync.ManifestName)); err != nil {
		t.Fatalf("stage a package being rewritten: %v", err)
	}

	drained := mustDrain(t, r)
	if len(drained.Applied) != 0 || len(drained.Held) != 1 {
		t.Fatalf("the drain applied %v and held %v, want only the package being rewritten",
			appliedIDs(drained), heldIDs(drained))
	}
	if drained.Held[0].Reason != HoldUnreadable {
		t.Fatalf("held for %q, want the package reported as unreadable", drained.Held[0].Reason)
	}
	if drained.Cleanup != nil {
		t.Fatalf("the drain reported a housekeeping failure: %v", drained.Cleanup)
	}

	heldOn(t, r, contested.PackageID)
	if _, err := os.Lstat(retained); err != nil {
		t.Fatalf("the retained branches were collected under a live hold: %v", err)
	}
}

// TestDrainKeepsEveryHoldWhenTheChannelItselfIsGone is the sweep's precondition,
// and the reason it is a precondition rather than a special case.
//
// An absent incoming directory makes every held package "not present", so one
// sweep would drop every hold in the vault and collect every retained branch —
// over a directory that says only that nothing has ever been delivered here. It
// is what an unmounted share looks like, and the local branches it would take
// with it are the versions of the contested notes that exist nowhere else.
func TestDrainKeepsEveryHoldWhenTheChannelItselfIsGone(t *testing.T) {
	r := enroll(t)
	packaged := r.believes(t, "Alpha.md")
	r.pair.Local.Write(t, "Alpha.md", localAlphaBody)

	contested, _ := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), packaged)
	})
	if drained := mustDrain(t, r); len(drained.Held) != 1 {
		t.Fatalf("the first drain held %v, want the contested package", heldIDs(drained))
	}
	retained, err := conflictDir(r.root(), contested.PackageID)
	if err != nil {
		t.Fatalf("conflictDir: %v", err)
	}

	// The share holding the channel is unmounted, or has not been created yet on a
	// vault that has only ever been imported into by hand.
	if err := os.RemoveAll(vaultsync.IncomingDir(r.root())); err != nil {
		t.Fatalf("unmount the channel: %v", err)
	}

	drained := mustDrain(t, r)
	if len(drained.Applied) != 0 || len(drained.Held) != 0 {
		t.Fatalf("the drain applied %v and held %v, want an absent channel to decide nothing",
			appliedIDs(drained), heldIDs(drained))
	}
	heldOn(t, r, contested.PackageID)
	if _, err := os.Lstat(retained); err != nil {
		t.Fatalf("an absent channel collected the retained branches: %v", err)
	}
}

// TestDrainAppliesAChainOfFollowersNoMatterHowTheySort is the transitive half of
// the causal exception, which one link cannot demonstrate.
//
// Three packages, each following the one before it, delivered in exactly the
// order that makes the drain's own tie-break disagree with all of them. If the
// walk stopped at one hop the first package would be held for changing a path a
// "earlier" package changes, that package would be held on its predecessor, and
// the chain would stall with nothing applied and nothing able to clear it.
func TestDrainAppliesAChainOfFollowersNoMatterHowTheySort(t *testing.T) {
	r := enroll(t)
	firstOp, secondOp := mustOperationID(t), mustOperationID(t)

	// Published in reverse: the last link of the chain takes the first position in
	// the producer's stream, so the total order proposes exactly the wrong answer.
	third := r.build(t)
	third.BeginOperation("", r.clock.Now())
	third.Write("Beta.md", []byte(betaThriceBody),
		vaultsync.Existing(vaultsync.HashBytes([]byte(betaTwiceBody))), secondOp)
	last, _ := r.publish(t, third)

	second := r.build(t)
	second.BeginOperation(secondOp, r.clock.Now())
	second.Write("Beta.md", []byte(betaTwiceBody),
		vaultsync.Existing(vaultsync.HashBytes([]byte(betaOnceBody))), firstOp)
	middle, _ := r.publish(t, second)

	first := r.build(t)
	first.BeginOperation(firstOp, r.clock.Now())
	first.Write("Beta.md", []byte(betaOnceBody), r.believes(t, "Beta.md"))
	head, _ := r.publish(t, first)

	drained := mustDrain(t, r)

	requireEqual(t, appliedIDs(drained), []string{head.PackageID, middle.PackageID, last.PackageID},
		"the order the drain applied the chain in")
	if len(drained.Held) != 0 {
		t.Fatalf("the drain held %v, want nothing", heldIDs(drained))
	}
	if got := r.pair.Local.Read(t, "Beta.md"); got != betaThriceBody {
		t.Fatalf("the note holds %q, want the last edit of the chain", got)
	}
}

// TestDrainCallsAPackageStillBeingCopiedInUnreadableRatherThanFailed is about
// the one refusal that reaches the scheduler from two different places.
//
// The scan reads the manifest and nothing else, so a package whose payloads are
// still being written looks whole to it — the incompleteness is found later, by
// Accept, on the far side of the scan's own unreadable check. Both are the same
// situation and need the same answer: wait. Calling the late one a failure would
// put "applying it failed" in front of somebody whose transport is simply still
// running, and the remedy for a failure is to go looking at the vault.
func TestDrainCallsAPackageStillBeingCopiedInUnreadableRatherThanFailed(t *testing.T) {
	r := enroll(t)
	arriving, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})
	independent, _ := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Beta.md", []byte(betaOnceBody), r.believes(t, "Beta.md"))
	})
	emptyPayloads(t, dir)

	drained := mustDrain(t, r)

	requireEqual(t, appliedIDs(drained), []string{independent.PackageID},
		"the packages the drain applied past the one still arriving")
	requireHeldFor(t, drained, arriving.PackageID, HoldUnreadable)
	if got := r.pair.Local.Read(t, "Beta.md"); got != betaOnceBody {
		t.Fatalf("the independent edit holds %q", got)
	}
}

// TestDrainSaysAPackageForAnotherVaultIsRejectedRatherThanFailed draws the line
// between a wait and a remedy.
//
// Every other hold resolves itself: the transport delivers the dependency, or a
// person edits the note. A package this vault will not accept resolves never, and
// reporting it in the same bucket as a failed application invites the one useless
// response — running the command again.
func TestDrainSaysAPackageForAnotherVaultIsRejectedRatherThanFailed(t *testing.T) {
	r := enroll(t)
	foreign, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})
	misaddress(t, dir)

	drained := mustDrain(t, r)

	if len(drained.Applied) != 0 {
		t.Fatalf("the drain applied %v, want nothing", appliedIDs(drained))
	}
	requireHeldFor(t, drained, foreign.PackageID, HoldRejected)
	if _, err := os.Stat(r.pair.Local.Path("Notes/Delta.md")); !os.IsNotExist(err) {
		t.Fatalf("the refused creation landed anyway: %v", err)
	}
}

// TestDrainSaysAPackageInAnUnknownFormatIsRejectedRatherThanStillArriving is
// the scan's half of the same distinction, and it is the one that costs the most
// to get wrong.
//
// A peer that upgrades starts publishing a format this build cannot read, and
// the scan — which reads the manifest and nothing else — is where that is found.
// Reporting it as a package still being copied in tells a person to wait for
// something no wait will fix, and the peer's every later change queues behind it.
func TestDrainSaysAPackageInAnUnknownFormatIsRejectedRatherThanStillArriving(t *testing.T) {
	r := enroll(t)
	newer, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})
	rewriteManifest(t, dir, func(manifest map[string]any) { manifest["format"] = "brainnotes-sync-v2" })

	drained := mustDrain(t, r)

	if len(drained.Applied) != 0 {
		t.Fatalf("the drain applied %v, want nothing", appliedIDs(drained))
	}
	if len(drained.Held) != 1 {
		t.Fatalf("the drain held %v, want the one package", heldIDs(drained))
	}
	// The manifest is what carries the identity, and this build refused to read it,
	// so the report can only name the directory — which is enough to act on.
	held := drained.Held[0]
	if held.Reason != HoldRejected {
		t.Fatalf("the package is held for %q, want %q: %s", held.Reason, HoldRejected, held.Detail)
	}
	if held.PackageID != "" || held.Dir != dir {
		t.Fatalf("the report identifies it as %+v, want the directory of %s", held, newer.PackageID)
	}
}

// TestDrainReportsAPackageThatLandedEvenWhenItsOwnChannelIsUnreadable is about
// the one fact that outranks the error: a Result means the content is durable.
//
// A receipt written by a newer build sitting in this vault's *outgoing* channel
// makes the acknowledgment fail with the same sentinel a package this vault
// refuses to accept carries. Classifying on the error first would report a
// package that is committed, journaled and snapshotted as one this vault will
// never accept — and drop both the Result and the failure out of the report, so
// nothing would say the peer has not been told.
func TestDrainReportsAPackageThatLandedEvenWhenItsOwnChannelIsUnreadable(t *testing.T) {
	r := enroll(t)
	landing, _ := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})
	plantUnreadableReceipt(t, r, landing.PackageID)

	drained := mustDrain(t, r)

	requireEqual(t, appliedIDs(drained), []string{landing.PackageID}, "the packages the drain applied")
	if len(drained.Held) != 0 {
		t.Fatalf("the drain held %v, want nothing", heldIDs(drained))
	}
	if got := r.pair.Local.Read(t, "Notes/Delta.md"); got != deltaBody {
		t.Fatalf("the created note holds %q, want the package's bytes", got)
	}
	if drained.Unfinished == nil {
		t.Fatalf("the drain reported nothing unfinished, want the acknowledgment failure")
	}
	if !strings.Contains(drained.Unfinished.Error(), landing.PackageID+" was applied and not acknowledged") {
		t.Fatalf("the drain reported %q, want it to name %s as unacknowledged",
			drained.Unfinished, landing.PackageID)
	}
	if result := drained.Applied[0]; result.Receipt == nil || result.ReceiptPath != "" {
		t.Fatalf("the result reports the unpublished receipt as %+v at %q", result.Receipt, result.ReceiptPath)
	}
}

// plantUnreadableReceipt puts a receipt this build cannot read where this vault
// publishes its own, under the name the package's acknowledgment will take. That
// is what a vault whose peer half was upgraded and rolled back holds, and it is
// the cheapest way to make acknowledging fail after the content is durable.
func plantUnreadableReceipt(t *testing.T, r *replicas, packageID string) {
	t.Helper()
	channel := vaultsync.OutgoingDir(r.root())
	path, err := vaultsync.ReceiptPath(channel, packageID)
	if err != nil {
		t.Fatalf("ReceiptPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create the outgoing channel: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"format":"brainnotes-sync-v2"}`), 0o644); err != nil {
		t.Fatalf("write the receipt: %v", err)
	}
}

// TestHoldsNamesThePackagesAPassNeverReached covers the partial report, which is
// the only place a verdict can be missing.
//
// A blank reason renders as a hold whose explanation went missing, and what it
// actually means is the opposite of a decision: the drain stopped before this
// package's turn. It is driven through the struct rather than through a refusal
// because the refusals that end a pass — corrupt state, a changed identity — are
// not reachable from a fixture without corrupting the vault first, and the
// substitution is the whole of what is being asserted.
func TestHoldsNamesThePackagesAPassNeverReached(t *testing.T) {
	decided := &candidate{
		dir:      "decided",
		manifest: &vaultsync.Manifest{PackageID: "pkg-decided", OriginDevice: "device-peer", Sequence: 2},
		reason:   HoldOrder,
		detail:   `changes "Beta.md", which pkg-earlier changes first`,
	}
	unreached := &candidate{
		dir:      "unreached",
		manifest: &vaultsync.Manifest{PackageID: "pkg-unreached", OriginDevice: "device-peer", Sequence: 3},
	}
	landed := &candidate{
		dir:      "landed",
		manifest: &vaultsync.Manifest{PackageID: "pkg-landed", OriginDevice: "device-peer", Sequence: 1},
		done:     true,
	}
	d := &drain{pending: []*candidate{landed, decided, unreached}}

	held := d.holds()

	if len(held) != 2 {
		t.Fatalf("holds reported %+v, want only the two that are still in the channel", held)
	}
	if held[0].Reason != HoldOrder || held[0].Detail != decided.detail {
		t.Fatalf("the decided package reports %q/%q, want its own verdict", held[0].Reason, held[0].Detail)
	}
	if held[1].Reason != HoldNotAttempted {
		t.Fatalf("the unreached package reports %q, want %q", held[1].Reason, HoldNotAttempted)
	}
	if held[1].Detail == "" {
		t.Fatalf("the unreached package reports no detail, want one naming the stopped pass")
	}
	if held[1].PackageID != "pkg-unreached" || held[1].Sequence != 3 {
		t.Fatalf("the unreached package is reported as %+v, want it identified", held[1])
	}
}

// TestUnfinishedWorkSaysWhichStepDidNotHappen is the discriminator behind
// Drained.Unfinished, and the receipt is what it reads rather than the error.
//
// Both shapes come back from Apply as a Result *with* an error, and they send a
// person to opposite places: one to a peer that was never told, the other to
// whatever came after telling it — the import record, or the channel the
// acknowledgment could not be written back into. Guessing from the error would
// describe a receipt this vault holds as one it never minted.
func TestUnfinishedWorkSaysWhichStepDidNotHappen(t *testing.T) {
	const acknowledged = "was applied and acknowledged and the work after that did not finish"
	cases := []struct {
		name   string
		result *Result
		cause  error
		want   string
	}{
		{
			name:   "the receipt was never minted",
			result: &Result{PackageID: "pkg-unacknowledged"},
			cause:  errors.New("mint receipt: read back the recovery entry"),
			want:   "pkg-unacknowledged was applied and not acknowledged",
		},
		{
			name:   "the receipt is published and the record is not saved",
			result: &Result{PackageID: "pkg-unrecorded", Receipt: &vaultsync.Receipt{PackageID: "pkg-unrecorded"}, ReceiptPath: "/outgoing/pkg-unrecorded.json"},
			cause:  errors.New("write import state: no space left on device"),
			want:   "pkg-unrecorded " + acknowledged,
		},
		{
			// A redelivery whose retained receipt could not be put back in the
			// channel. The statement exists locally, but the peer still cannot read it.
			name:   "the receipt exists and could not be republished",
			result: &Result{PackageID: "pkg-unpublished", Duplicate: true, Receipt: &vaultsync.Receipt{PackageID: "pkg-unpublished"}},
			cause:  errors.New("publish receipt: read-only file system"),
			want:   "pkg-unpublished was applied and not acknowledged",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := unfinishedWork(tc.result, tc.cause)
			if err == nil {
				t.Fatalf("unfinishedWork answered nothing for a failure")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unfinishedWork said %q, want it to name %q", err, tc.want)
			}
			if !errors.Is(err, tc.cause) {
				t.Fatalf("unfinishedWork dropped the cause: %v", err)
			}
		})
	}
	if err := unfinishedWork(&Result{PackageID: "pkg-clean"}, nil); err != nil {
		t.Fatalf("unfinishedWork answered %v for an application that finished", err)
	}
}

// emptyPayloads leaves a package's manifest in place and takes away the bytes it
// names, which is what the channel holds while the transport is mid-copy.
func emptyPayloads(t *testing.T, dir string) {
	t.Helper()
	payloads := filepath.Join(dir, vaultsync.PayloadsName)
	entries, err := os.ReadDir(payloads)
	if err != nil {
		t.Fatalf("read the payload directory: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("the package carries no payload to take away")
	}
	for _, entry := range entries {
		if err := os.Remove(filepath.Join(payloads, entry.Name())); err != nil {
			t.Fatalf("remove payload %s: %v", entry.Name(), err)
		}
	}
}

// misaddress readdresses a delivered package to a vault that is not this one,
// which is what a misrouted transport share puts in the channel. The manifest is
// rewritten in place because nothing signs it: enrollment here is a routing fact
// rather than an authentication one, which is exactly why a package that fails
// the check is refused rather than merged.
func misaddress(t *testing.T, dir string) {
	t.Helper()
	other, err := vaultsync.NewVaultID()
	if err != nil {
		t.Fatalf("NewVaultID: %v", err)
	}
	rewriteManifest(t, dir, func(manifest map[string]any) { manifest["vault_id"] = other })
}

// rewriteManifest edits a delivered package's manifest in place, which is how
// these tests produce a package no honest producer of this build would write.
func rewriteManifest(t *testing.T, dir string, mutate func(map[string]any)) {
	t.Helper()
	path := filepath.Join(dir, vaultsync.ManifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the manifest: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode the manifest: %v", err)
	}
	mutate(manifest)
	rewritten, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode the manifest: %v", err)
	}
	if err := os.WriteFile(path, rewritten, 0o644); err != nil {
		t.Fatalf("write the manifest: %v", err)
	}
}

func mustDrain(t *testing.T, r *replicas) *Drained {
	t.Helper()
	drained, err := Drain(r.root(), Options{Command: "bn sync drain --test", Now: r.clock.Now})
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	return drained
}

// appliedIDs and heldIDs report a drain's two outcomes in the order it decided
// them, which is the order the assertions above are about.
func appliedIDs(drained *Drained) []string {
	ids := make([]string, 0, len(drained.Applied))
	for _, result := range drained.Applied {
		ids = append(ids, result.PackageID)
	}
	return ids
}

func heldIDs(drained *Drained) []string {
	ids := make([]string, 0, len(drained.Held))
	for _, held := range drained.Held {
		ids = append(ids, held.PackageID)
	}
	return ids
}

func requireHeldFor(t *testing.T, drained *Drained, packageID string, reason HoldReason) {
	t.Helper()
	for _, held := range drained.Held {
		if held.PackageID != packageID {
			continue
		}
		if held.Reason != reason {
			t.Fatalf("%s is held for %q, want %q: %s", packageID, held.Reason, reason, held.Detail)
		}
		return
	}
	t.Fatalf("%s is not held at all", packageID)
}
