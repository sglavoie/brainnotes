package vaultimport

import (
	"os"
	"path/filepath"
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
