package vaultimport

import (
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// contestTwo puts the replicas in disagreement over two notes carried by one
// package, which is what makes a per-path decision a thing a person can make.
func contestTwo(t *testing.T, r *replicas) (*vaultsync.Manifest, string) {
	t.Helper()
	alphaPackaged := r.believes(t, "Alpha.md")
	gammaPackaged := r.believes(t, "Notes/Gamma.md")
	r.pair.Local.Write(t, "Alpha.md", localAlphaBody)
	r.pair.Local.Write(t, "Notes/Gamma.md", localGammaBody)
	r.pair.Peer.Write(t, "Alpha.md", alphaBody)
	r.pair.Peer.Write(t, "Notes/Gamma.md", peerGammaBody)

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), alphaPackaged)
		b.Write("Notes/Gamma.md", []byte(peerGammaBody), gammaPackaged)
	})
	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying a package onto two notes edited here")
	return manifest, dir
}

// TestResolveDecidesEachContestedPathSeparately covers Choices.Paths, which is
// the difference between resolving a conflict and resolving a package. One
// operation can contest several notes and a person has no reason to feel the
// same way about all of them, so the default is only a default.
//
// It is also the case where the two halves of a resolution disagree: one path
// keeps what the peer sent and the other rejects it, so the revision must name
// exactly the second and the peer must still converge on both.
func TestResolveDecidesEachContestedPathSeparately(t *testing.T) {
	r := enroll(t)
	r.baseline(t)
	manifest, dir := contestTwo(t, r)

	plan := previewed(t, r, manifest.PackageID, Choices{
		Default: AcceptIncoming,
		Paths:   map[string]Choice{"Notes/Gamma.md": KeepCurrent},
	})
	if len(plan.Paths) != 2 {
		t.Fatalf("the plan decides %d paths, want both contested notes", len(plan.Paths))
	}
	for _, decided := range plan.Paths {
		want := AcceptIncoming
		if decided.Path == "Notes/Gamma.md" {
			want = KeepCurrent
		}
		if decided.Choice != want {
			t.Fatalf("the plan decides %s as %q, want %q", decided.Path, decided.Choice, want)
		}
	}

	resolution := resolved(t, r, dir, plan)
	requireEqual(t, resolution.Paths, []string{"Notes/Gamma.md"}, "the revision names")
	requireResolutionSettled(t, r, manifest.PackageID)

	r.converge(t)
	requireAgreed(t, r, "Alpha.md", alphaBody)
	requireAgreed(t, r, "Notes/Gamma.md", localGammaBody)
}

// TestPreviewRefusesKeepBothWhereTheIncomingVersionIsADeletion is the missing
// half of keep-both's refusals. The peer deleted the note rather than editing
// it, so applying the operation removes it and "keeping both" would leave one
// file under a derived name and nothing under the name the note had — a rename
// dressed up as keeping everything, and not what anybody choosing it meant.
func TestPreviewRefusesKeepBothWhereTheIncomingVersionIsADeletion(t *testing.T) {
	r := enroll(t)
	packaged := r.believes(t, "Notes/Gamma.md")
	r.pair.Local.Write(t, "Notes/Gamma.md", localGammaBody)

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Delete("Notes/Gamma.md", packaged)
	})
	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying a deletion onto a note edited here")

	_, err = Preview(r.root(), manifest.PackageID, Choices{Default: KeepBoth})
	requireIs(t, err, ErrResolutionUnavailable, "keeping both versions of a note the peer deleted")
}

// TestAPlanRefusesStepsThatWouldFightEachOther drives distinct directly, since
// the plans it refuses are ones Preview will not build out of any conflict a
// real package can produce — two paths whose derived names collide need content
// that hashes the same under names that fold together.
//
// It is checked anyway because the cost of being wrong is silent: a transaction
// cannot write one path twice, so the second decision would win and the first
// would be reported as carried out.
func TestAPlanRefusesStepsThatWouldFightEachOther(t *testing.T) {
	write := func(rel string) []Step {
		return []Step{{Path: rel, Action: vaultsync.ActionWrite, SHA256: vaultsync.HashBytes([]byte(rel))}}
	}
	t.Run("two decisions writing one name", func(t *testing.T) {
		// Folded rather than identical, so this also asserts the comparison is the
		// one vaultsync uses everywhere else: two files here and one on a folding
		// replica is a disagreement about how many notes exist.
		plan := &Plan{PackageID: "pkg", Paths: []PathPlan{
			{Path: "Alpha.md", Steps: write("Shared.md")},
			{Path: "Beta.md", Steps: write("shared.md")},
		}}
		requireIs(t, plan.distinct(), ErrResolutionUnavailable, "two resolutions writing one name")
	})
	t.Run("a step landing on another contested path", func(t *testing.T) {
		plan := &Plan{PackageID: "pkg", Paths: []PathPlan{
			{Path: "Alpha.md", Steps: write("Beta.md")},
			{Path: "Beta.md"},
		}}
		requireIs(t, plan.distinct(), ErrResolutionUnavailable, "a resolution writing a path contested in its own right")
	})
}

// TestResolveKeepCurrentUndoesARenameThisVaultDivergedFrom is keep-current
// applied to the conflict whose subject is a name rather than a version. The
// peer moved the note one way and this vault moved it another, so the contested
// path is a source that is gone on both sides; keeping the current arrangement
// means removing the destination the peer chose and leaving this vault's own
// copy where it is.
func TestResolveKeepCurrentUndoesARenameThisVaultDivergedFrom(t *testing.T) {
	r := enroll(t)
	moved := r.pair.Local.Read(t, "Alpha.md")
	packaged := r.believes(t, "Alpha.md")
	// Committed rather than merely moved in the working tree. A tracked path that
	// is simply gone is a refusal in its own right — the Git safety engine will
	// not let any mutation proceed over an unexplained deletion — so an
	// uncommitted move would test that preflight instead of this resolution.
	r.pair.Local.Write(t, "Archive/Alpha.md", moved)
	r.pair.Local.Git(t, "rm", "-q", "Alpha.md")
	r.pair.Local.Git(t, "add", "Archive/Alpha.md")
	r.pair.Local.Git(t, "commit", "-q", "-m", "move the note under a name of this vault's own choosing")

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Delete("Alpha.md", packaged)
		b.Write("Notes/Alpha.md", []byte(moved), vaultsync.Absent())
	})
	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying a rename of a note this vault moved elsewhere")

	plan := previewed(t, r, manifest.PackageID, Choices{Default: KeepCurrent})
	resolution := resolved(t, r, dir, plan)
	// The step is about the peer's destination, not about the contested path:
	// the source is already gone here and there is nothing to do to it.
	requireEqual(t, resolution.Paths, []string{"Notes/Alpha.md"}, "the revision names")
	if exists(t, r.pair.Local, "Notes/Alpha.md") {
		t.Fatal("keeping this vault's arrangement left the peer's copy behind, so the note is in two places")
	}
	if got := r.pair.Local.Read(t, "Archive/Alpha.md"); got != moved {
		t.Fatalf("this vault's own copy holds %q, want it untouched", got)
	}
	// The peer's arrangement is in the history rather than discarded unseen,
	// which is what makes rejecting it reviewable.
	if got := r.pair.Local.Git(t, "show", "HEAD~1:Notes/Alpha.md"); got != strings.TrimSpace(moved) {
		t.Fatalf("the applying commit holds %q at the peer's destination, want the version that was rejected", got)
	}
	requireResolutionSettled(t, r, manifest.PackageID)
}
