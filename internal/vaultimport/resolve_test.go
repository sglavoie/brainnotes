package vaultimport

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/vaultexport"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

// mergedAlphaBody is what a person hands back after reconciling the two
// versions by hand. It is neither branch, so a test asserting on it cannot pass
// by accident on a resolution that silently picked a side.
const mergedAlphaBody = "---\nstatus: review\ntitle: Alpha\n---\n\nAlpha body, reconciled by hand from both replicas, see [[Beta]].\n"

// resolveOptions is what a command would pass, with the fixture clock wired in.
func (r *replicas) resolveOptions() Options {
	return Options{Command: "bn sync resolve --test", Now: r.clock.Now}
}

// contest puts the two replicas in disagreement over Alpha.md and hands the
// resulting hold back.
//
// The peer's version is written to the peer's *disk* as well as into the
// package, which the conflict tests do not need and every convergence test
// here does: a peer that never held the version it sent cannot be asked whether
// it converged on the answer.
func contest(t *testing.T, r *replicas) (*vaultsync.Manifest, string) {
	t.Helper()
	packaged := r.believes(t, "Alpha.md")
	r.pair.Local.Write(t, "Alpha.md", localAlphaBody)
	r.pair.Peer.Write(t, "Alpha.md", alphaBody)

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), packaged)
	})
	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying a package onto a note edited here")
	return manifest, dir
}

// previewed builds a plan for the one package this vault is holding.
func previewed(t *testing.T, r *replicas, packageID string, choices Choices) *Plan {
	t.Helper()
	plan, err := Preview(r.root(), packageID, choices)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	return plan
}

// resolved carries out a plan, failing the test unless it completed.
func resolved(t *testing.T, r *replicas, packageDir string, plan *Plan) *Resolution {
	t.Helper()
	resolution, err := Resolve(r.root(), packageDir, plan, r.resolveOptions())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolution.Applied == nil || resolution.Applied.Receipt == nil {
		t.Fatal("a resolution applied the package without acknowledging it, so the peer redelivers forever")
	}
	return resolution
}

// exportToPeer publishes everything this vault has committed straight into the
// peer's incoming channel, which is the one seam a test may move: there is no
// transport here, and copying the directory by hand would assert about the copy.
//
// It loops because one pass is bounded — a baseline is sent in budgeted slices
// and a long history in bounded batches — and a test that ran a single pass
// would silently stop asserting the moment the fixture grew.
func (r *replicas) exportToPeer(t *testing.T) []vaultexport.Published {
	t.Helper()
	published := make([]vaultexport.Published, 0)
	for pass := 0; pass < 16; pass++ {
		result, err := vaultexport.Export(context.Background(), vaultexport.Options{
			Root:       r.root(),
			ChannelDir: vaultsync.IncomingDir(r.pair.Peer.Root),
			Now:        r.clock.Now,
		})
		if err != nil {
			t.Fatalf("Export: %v", err)
		}
		published = append(published, result.Packages...)
		if result.PendingPaths == 0 && result.PendingCommits == 0 {
			return published
		}
	}
	t.Fatal("the export never finished; the fixture outgrew the pass bound")
	return nil
}

// drainPeer applies everything waiting in the peer's channel.
func (r *replicas) drainPeer(t *testing.T) *Drained {
	t.Helper()
	drained, err := Drain(r.pair.Peer.Root, Options{Command: "bn sync drain --test", Now: r.clock.Now})
	if err != nil {
		t.Fatalf("Drain on the peer: %v", err)
	}
	return drained
}

// converge is the round trip: this vault publishes its history and the peer
// applies it. It returns what the peer did, so a test can assert that the
// revision travelled rather than that the two vaults happened to agree.
func (r *replicas) converge(t *testing.T) *Drained {
	t.Helper()
	r.exportToPeer(t)
	drained := r.drainPeer(t)
	// A held package is the round trip failing, and it fails here rather than at
	// a content comparison further down: "the peer refused what this vault sent"
	// and "the peer applied it and the bytes differ" are different bugs and the
	// second message would hide the first.
	if len(drained.Held) != 0 {
		t.Fatalf("the peer held %d of the packages this vault sent: %+v", len(drained.Held), drained.Held)
	}
	return drained
}

// baseline gets the two replicas into the state a real pair is in before a
// conflict: this vault has exported its history once and the peer has applied
// it, so a later export publishes commits rather than a first-export snapshot.
func (r *replicas) baseline(t *testing.T) {
	t.Helper()
	r.converge(t)
}

// requireAgreed asserts that both replicas hold the same bytes at a path,
// and that those bytes are the ones the resolution chose.
func requireAgreed(t *testing.T, r *replicas, rel, want string) {
	t.Helper()
	if got := r.pair.Local.Read(t, rel); got != want {
		t.Fatalf("this vault holds %q at %s, want %q", got, rel, want)
	}
	if got := r.pair.Peer.Read(t, rel); got != want {
		t.Fatalf("the peer holds %q at %s, want %q", got, rel, want)
	}
}

// TestResolveKeepCurrentConvergesBothReplicas is the choice with the most to
// prove. This vault rejects the peer's version, and the peer — which has no
// reason to think it was wrong — must still end up holding what this vault
// decided rather than reasserting its own.
//
// That only works because the revision travels as an ordinary change whose
// expectation is the peer's own result: the peer's copy of Alpha.md is exactly
// what the revision says it is replacing, so it applies cleanly instead of
// reading as a third conflict.
func TestResolveKeepCurrentConvergesBothReplicas(t *testing.T) {
	r := enroll(t)
	r.baseline(t)
	manifest, dir := contest(t, r)

	plan := previewed(t, r, manifest.PackageID, Choices{Default: KeepCurrent})
	resolution := resolved(t, r, dir, plan)
	if resolution.JournalEntry == "" {
		t.Fatal("keeping this vault's version recorded no revision, so there is nothing to send the peer")
	}
	requireEqual(t, resolution.Paths, []string{"Alpha.md"}, "the revision names")

	if got := r.pair.Local.Read(t, "Alpha.md"); got != localAlphaBody {
		t.Fatalf("Alpha.md holds %q, want this vault's own version", got)
	}
	requireResolutionSettled(t, r, manifest.PackageID)

	r.converge(t)
	requireAgreed(t, r, "Alpha.md", localAlphaBody)
}

// TestResolveAcceptIncomingConvergesBothReplicas is the choice that revises
// nothing: applying the operation is the whole of it, so the resolution must
// produce no second commit and nothing for the peer to apply.
func TestResolveAcceptIncomingConvergesBothReplicas(t *testing.T) {
	r := enroll(t)
	r.baseline(t)
	manifest, dir := contest(t, r)
	commits := commitCount(t, r.pair.Local)

	plan := previewed(t, r, manifest.PackageID, Choices{Default: AcceptIncoming})
	resolution := resolved(t, r, dir, plan)
	if resolution.JournalEntry != "" {
		t.Fatalf("accepting the peer's version wrote a revision (%s), which would send the peer its own change back", resolution.JournalEntry)
	}
	if len(resolution.Paths) != 0 {
		t.Fatalf("accepting the peer's version names %v as revised", resolution.Paths)
	}
	if got, want := commitCount(t, r.pair.Local), commits+1; got != want {
		t.Fatalf("the vault holds %d commits, want %d: accepting is one application and no revision", got, want)
	}
	requireResolutionSettled(t, r, manifest.PackageID)

	r.converge(t)
	requireAgreed(t, r, "Alpha.md", alphaBody)
}

// TestResolveKeepBothConvergesBothReplicas covers the choice that decides
// against neither branch. The contested path takes the peer's version and this
// vault's goes beside it under a derived name, and both must reach the peer —
// the derived copy as a create, the contested path as the change the peer
// already made.
func TestResolveKeepBothConvergesBothReplicas(t *testing.T) {
	r := enroll(t)
	r.baseline(t)
	manifest, dir := contest(t, r)

	plan := previewed(t, r, manifest.PackageID, Choices{Default: KeepBoth})
	beside := onlyDerived(t, plan, "Alpha.md")
	resolution := resolved(t, r, dir, plan)
	requireEqual(t, resolution.Paths, []string{beside}, "the revision names")
	requireResolutionSettled(t, r, manifest.PackageID)

	r.converge(t)
	requireAgreed(t, r, "Alpha.md", alphaBody)
	requireAgreed(t, r, beside, localAlphaBody)
}

// TestResolveManualMergeConvergesBothReplicas covers the choice whose content
// comes from neither replica. Both must end up holding the merged bytes, and
// neither may end up holding a branch.
func TestResolveManualMergeConvergesBothReplicas(t *testing.T) {
	r := enroll(t)
	r.baseline(t)
	manifest, dir := contest(t, r)

	plan := previewed(t, r, manifest.PackageID, Choices{
		Default: ManualMerge,
		Merged:  map[string][]byte{"Alpha.md": []byte(mergedAlphaBody)},
	})
	resolution := resolved(t, r, dir, plan)
	requireEqual(t, resolution.Paths, []string{"Alpha.md"}, "the revision names")
	requireResolutionSettled(t, r, manifest.PackageID)

	r.converge(t)
	requireAgreed(t, r, "Alpha.md", mergedAlphaBody)
}

// onlyDerived returns the single path a plan writes that is not the contested
// one, which is the copy KeepBoth derives.
func onlyDerived(t *testing.T, plan *Plan, contested string) string {
	t.Helper()
	derived := make([]string, 0)
	for _, step := range plan.steps() {
		if step.Path != contested {
			derived = append(derived, step.Path)
		}
	}
	if len(derived) != 1 {
		t.Fatalf("the plan derives %v, want exactly one path beside %s", derived, contested)
	}
	return derived[0]
}

// requireResolutionSettled asserts that a finished resolution left nothing
// durable behind: no hold, no promised revision, and none of the two stores the
// two of them keep content in.
func requireResolutionSettled(t *testing.T, r *replicas, packageID string) {
	t.Helper()
	requireResolutionSettledAt(t, r.root(), packageID)
}

// requireResolutionSettledAt is the same assertion for a vault a crash test has
// only a path to.
func requireResolutionSettledAt(t *testing.T, root, packageID string) {
	t.Helper()
	state, err := LoadState(root)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if _, held := state.Conflicts[packageID]; held {
		t.Fatalf("%s is still held after being resolved", packageID)
	}
	if _, promised := state.Resolutions[packageID]; promised {
		t.Fatalf("%s still promises a revision after being resolved", packageID)
	}
	if _, applied := state.Applied[packageID]; !applied {
		t.Fatalf("%s was resolved without being recorded as applied, so a dependent would wait on it forever", packageID)
	}
	retained, err := conflictDir(root, packageID)
	if err != nil {
		t.Fatalf("conflictDir: %v", err)
	}
	if _, err := os.Lstat(retained); !os.IsNotExist(err) {
		t.Fatalf("the retained branches survived the resolution (%v)", err)
	}
	staged, err := resolutionDir(root, packageID)
	if err != nil {
		t.Fatalf("resolutionDir: %v", err)
	}
	if _, err := os.Lstat(staged); !os.IsNotExist(err) {
		t.Fatalf("the staged revision content survived the resolution (%v)", err)
	}
}

// TestPreviewNamesEveryAffectedPathAndTouchesNothing is the "preview" half of
// the session in one test. A person has to be able to see what a resolution
// would do before it happens, which means both branches of every contested path
// and the paths a choice invents — and seeing it must not cost anything.
func TestPreviewNamesEveryAffectedPathAndTouchesNothing(t *testing.T) {
	r := enroll(t)
	manifest, _ := contest(t, r)
	head := r.pair.Local.Head(t)
	commits := commitCount(t, r.pair.Local)

	plan := previewed(t, r, manifest.PackageID, Choices{Default: KeepBoth})
	requirePlanIdentity(t, plan, manifest.PackageID)
	if len(plan.Paths) != 1 {
		t.Fatalf("the plan decides %d paths, want the one that is contested", len(plan.Paths))
	}
	decided := plan.Paths[0]
	if decided.Path != "Alpha.md" || decided.Kind != ConflictConcurrentEdit || decided.Choice != KeepBoth {
		t.Fatalf("the plan decides %+v, want keep-both on a concurrent edit of Alpha.md", decided)
	}
	requirePinnedBranches(t, decided)
	// The derived path is in the plan even though nothing on disk or in the hold
	// names it, which is the whole point: a person approving this must be able to
	// see the file it is about to create.
	beside := onlyDerived(t, plan, "Alpha.md")
	if !strings.HasPrefix(beside, "Alpha (conflicted copy ") || !strings.HasSuffix(beside, ".md") {
		t.Fatalf("the plan derives %q, want a conflicted copy beside Alpha.md", beside)
	}

	requirePreviewChangedNothing(t, r, manifest.PackageID, head, commits)
}

// requirePlanIdentity asserts a preview describes the package it was asked
// about and claims nothing a resolution has not done yet.
func requirePlanIdentity(t *testing.T, plan *Plan, packageID string) {
	t.Helper()
	if plan.PackageID != packageID || plan.OperationID == "" {
		t.Fatalf("the plan is %+v, want the held package and its operation", plan)
	}
	if plan.JournalEntry != "" {
		t.Fatalf("a preview named recovery entry %q, which only a resolution may mint", plan.JournalEntry)
	}
}

// requirePinnedBranches asserts both versions of the contested note are pinned:
// what the peer sent and what this vault holds right now — the local one read
// from the disk rather than copied out of the hold, which is as old as the hold
// is.
func requirePinnedBranches(t *testing.T, decided PathPlan) {
	t.Helper()
	if decided.Remote.SHA256 != vaultsync.HashBytes([]byte(alphaBody)) {
		t.Fatalf("the plan pins remote %s, which is not the version the peer sent", decided.Remote.SHA256)
	}
	if decided.Local.SHA256 != vaultsync.HashBytes([]byte(localAlphaBody)) {
		t.Fatalf("the plan pins local %s, which is not the version on this disk", decided.Local.SHA256)
	}
}

// requirePreviewChangedNothing is the read-only half stated as an assertion:
// same content, same history, same durable records, and none of the stores a
// resolution writes into.
func requirePreviewChangedNothing(t *testing.T, r *replicas, packageID, head string, commits int) {
	t.Helper()
	if got := r.pair.Local.Read(t, "Alpha.md"); got != localAlphaBody {
		t.Fatalf("previewing changed Alpha.md to %q", got)
	}
	if got := r.pair.Local.Head(t); got != head {
		t.Fatal("previewing moved the content history")
	}
	if got := commitCount(t, r.pair.Local); got != commits {
		t.Fatalf("the vault holds %d commits, want %d", got, commits)
	}
	state := importState(t, r)
	if len(state.Resolutions) != 0 {
		t.Fatalf("previewing promised a revision: %v", state.Resolutions)
	}
	if _, still := state.Conflicts[packageID]; !still {
		t.Fatal("previewing dropped the hold it was describing")
	}
	if entries := r.entries(t); len(entries) != 0 {
		t.Fatalf("previewing left %d recovery entries behind", len(entries))
	}
	if _, err := os.Lstat(resolutionsRoot(r.root())); !os.IsNotExist(err) {
		t.Fatalf("previewing staged revision content (%v)", err)
	}
}

// TestPreviewRefusesAPackageThisVaultIsNotHolding separates "there is nothing to
// resolve" from "what you previewed has moved". They arrive at the same command
// and mean different things: the first is a caller naming the wrong package, the
// second is a race.
func TestPreviewRefusesAPackageThisVaultIsNotHolding(t *testing.T) {
	r := enroll(t)
	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), r.believes(t, "Alpha.md"))
	})
	r.mustApply(t, dir)

	_, err := Preview(r.root(), manifest.PackageID, Choices{Default: KeepCurrent})
	requireIs(t, err, ErrNoConflict, "previewing a package that applied cleanly")
}

// TestPreviewRefusesAChoiceTheConflictCannotTake is the table's refusals. A
// choice that does not fit is rejected while it is still a question, not
// halfway through a resolution that has already applied the package.
func TestPreviewRefusesAChoiceTheConflictCannotTake(t *testing.T) {
	t.Run("keep-both on declarations", func(t *testing.T) {
		r := enroll(t)
		manifest := contestConfiguration(t, r)
		_, err := Preview(r.root(), manifest.PackageID, Choices{Default: KeepBoth})
		requireIs(t, err, ErrResolutionUnavailable, "keeping both copies of the vault's declarations")
	})
	t.Run("manual merge with no merge", func(t *testing.T) {
		r := enroll(t)
		manifest, _ := contest(t, r)
		_, err := Preview(r.root(), manifest.PackageID, Choices{Default: ManualMerge})
		requireIs(t, err, ErrResolutionUnavailable, "merging by hand without supplying the merge")
	})
	t.Run("no choice at all", func(t *testing.T) {
		r := enroll(t)
		manifest, _ := contest(t, r)
		_, err := Preview(r.root(), manifest.PackageID, Choices{})
		requireIs(t, err, ErrResolutionUnavailable, "resolving with nothing decided")
	})
}
