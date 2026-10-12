package vaultimport

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

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
