package vaultimport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultexport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultfixture"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
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

// contestConfiguration puts the replicas in disagreement over the portable
// configuration, which is the one conflict whose subject is not a note.
func contestConfiguration(t *testing.T, r *replicas) *vaultsync.Manifest {
	t.Helper()
	baseline := r.pair.Local.Read(t, config.FileName)
	shared := vaultsync.Existing(vaultsync.HashBytes([]byte(baseline)))
	r.pair.Local.Write(t, config.FileName, baseline+"\n[frontmatter.properties.area]\ntype = \"string\"\n")
	remote := baseline + "\n[frontmatter.properties.org]\ntype = \"string\"\n"

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write(config.FileName, []byte(remote), shared)
	})
	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying declarations this vault has also changed")
	return manifest
}

// TestResolveRefusesAnEditThatLandedAfterThePreview is the race the whole
// pinning design exists for. Somebody previews, goes to lunch, and saves the
// note in the meantime; carrying the plan out would decide in favour of a
// version nobody is looking at any more and destroy the newer one.
func TestResolveRefusesAnEditThatLandedAfterThePreview(t *testing.T) {
	r := enroll(t)
	manifest, dir := contest(t, r)
	plan := previewed(t, r, manifest.PackageID, Choices{Default: KeepCurrent})

	late := localAlphaBody + "\nAnd one more paragraph, written while the preview was on screen.\n"
	r.pair.Local.Write(t, "Alpha.md", late)
	head := r.pair.Local.Head(t)
	commits := commitCount(t, r.pair.Local)

	resolution, err := Resolve(r.root(), dir, plan, r.resolveOptions())
	requireIs(t, err, ErrStalePreview, "resolving against a note that moved after the preview")
	if resolution != nil {
		t.Fatalf("a refused resolution reported %+v, want nothing applied", resolution)
	}
	if got := r.pair.Local.Read(t, "Alpha.md"); got != late {
		t.Fatalf("Alpha.md holds %q, want the later edit intact", got)
	}
	requirePreviewChangedNothingBut(t, r, manifest.PackageID, head, commits)
}

// requirePreviewChangedNothingBut is requirePreviewChangedNothing without the
// content assertion, for the tests that changed the note themselves.
func requirePreviewChangedNothingBut(t *testing.T, r *replicas, packageID, head string, commits int) {
	t.Helper()
	if got := r.pair.Local.Head(t); got != head {
		t.Fatal("a refused resolution moved the content history")
	}
	if got := commitCount(t, r.pair.Local); got != commits {
		t.Fatalf("the vault holds %d commits, want %d", got, commits)
	}
	state := importState(t, r)
	if len(state.Resolutions) != 0 {
		t.Fatalf("a refused resolution promised a revision: %v", state.Resolutions)
	}
	if _, still := state.Conflicts[packageID]; !still {
		t.Fatal("a refused resolution dropped the hold, so the package could never be resolved")
	}
	if state.Pending != nil {
		t.Fatal("a refused resolution declared an import attempt, which strands every later import")
	}
}

// TestResolveRefusesAPlanForAConflictThatIsGone covers the other way a preview
// goes stale: somebody resolved the disagreement by hand — which is a supported
// resolution and always has been — and the plan now describes nothing.
func TestResolveRefusesAPlanForAConflictThatIsGone(t *testing.T) {
	r := enroll(t)
	manifest, dir := contest(t, r)
	plan := previewed(t, r, manifest.PackageID, Choices{Default: KeepCurrent})

	// The person decides in favour of the peer by hand, and the redelivery
	// applies with nothing left to contest.
	r.pair.Local.Write(t, "Alpha.md", alphaBody)
	r.mustApply(t, dir)

	_, err := Resolve(r.root(), dir, plan, r.resolveOptions())
	requireIs(t, err, ErrStalePreview, "resolving a conflict somebody already settled")
	if got := r.pair.Local.Read(t, "Alpha.md"); got != alphaBody {
		t.Fatalf("Alpha.md holds %q, want the version that settled it", got)
	}
}

// TestCompetingResolutionsLeaveTheVaultIntact is two people answering the same
// conflict. Both previewed against the same hold and chose differently; one wins
// and the other must be refused rather than applied on top, which would revise a
// revision nobody asked about.
func TestCompetingResolutionsLeaveTheVaultIntact(t *testing.T) {
	r := enroll(t)
	r.baseline(t)
	manifest, dir := contest(t, r)
	mine := previewed(t, r, manifest.PackageID, Choices{Default: KeepCurrent})
	theirs := previewed(t, r, manifest.PackageID, Choices{Default: AcceptIncoming})

	resolved(t, r, dir, mine)
	head := r.pair.Local.Head(t)
	commits := commitCount(t, r.pair.Local)

	resolution, err := Resolve(r.root(), dir, theirs, r.resolveOptions())
	requireIs(t, err, ErrStalePreview, "resolving a package somebody else just resolved")
	if resolution != nil {
		t.Fatalf("the losing resolution reported %+v, want nothing applied", resolution)
	}
	if got := r.pair.Local.Read(t, "Alpha.md"); got != localAlphaBody {
		t.Fatalf("Alpha.md holds %q, want the resolution that won", got)
	}
	if got := r.pair.Local.Head(t); got != head {
		t.Fatal("the losing resolution moved the content history")
	}
	if got := commitCount(t, r.pair.Local); got != commits {
		t.Fatalf("the vault holds %d commits, want %d", got, commits)
	}
	requireResolutionSettled(t, r, manifest.PackageID)

	r.converge(t)
	requireAgreed(t, r, "Alpha.md", localAlphaBody)
}

// TestResolutionKeepsTheRejectedVersionInHistory is the claim that makes
// rejecting a peer's version survivable: nothing is thrown away.
//
// This is the structural reason a resolution is two commits rather than one. The
// peer's version is applied first — really applied, into this vault's own
// content history and its own recovery journal — and only then revised. So the
// version that lost is a commit somebody can read, and the revision that beat it
// is an ordinary undoable operation that puts it back.
func TestResolutionKeepsTheRejectedVersionInHistory(t *testing.T) {
	r := enroll(t)
	manifest, dir := contest(t, r)
	before := commitCount(t, r.pair.Local)

	plan := previewed(t, r, manifest.PackageID, Choices{Default: KeepCurrent})
	resolution := resolved(t, r, dir, plan)

	// Two commits, in that order: the peer's operation, then the decision about it.
	if got, want := commitCount(t, r.pair.Local), before+2; got != want {
		t.Fatalf("the vault holds %d commits, want %d: one application and one revision", got, want)
	}
	if got := r.pair.Local.Git(t, "show", "HEAD~1:Alpha.md"); got != strings.TrimSpace(alphaBody) {
		t.Fatalf("the applying commit holds %q at Alpha.md, want the version that was rejected", got)
	}
	requireEqual(t, contentPaths(commitPaths(t, r.pair.Local, "HEAD")), []string{"Alpha.md"}, "the revising commit carries")

	// The rejected version is not only readable, it is restorable: the revision's
	// own undo entry is what puts it back, so a person who chose wrong runs the
	// same undo they would run for any other operation.
	entry := entryCovers(t, r, resolution.JournalEntry, "Alpha.md")
	if entry.Command != "bn sync resolve --test" {
		t.Fatalf("the revision's recovery entry names command %q", entry.Command)
	}
	if _, err := journal.Restore(r.root(), noteops.JournalRoot(r.root()), resolution.JournalEntry, journal.RestoreOptions{}); err != nil {
		t.Fatalf("undoing the resolution: %v", err)
	}
	if got := r.pair.Local.Read(t, "Alpha.md"); got != alphaBody {
		t.Fatalf("undoing the resolution left %q at Alpha.md, want the rejected version back", got)
	}
}

// TestResolvingAnOperationReleasesItsDescendants is the reconciliation half of
// the session's verification line. A held conflict holds the peer's later work
// behind it, which is correct — applying a change built on an operation this
// vault has not applied would apply it to the wrong thing — and it is only
// tolerable if resolving the conflict lets the queue move again.
func TestResolvingAnOperationReleasesItsDescendants(t *testing.T) {
	r := enroll(t)
	manifest, dir := contest(t, r)

	// The peer carries on working: a later operation, causally after the one this
	// vault is holding, touching a note nobody disagrees about.
	descendant := r.build(t)
	descendant.BeginOperation("", r.clock.Tick())
	descendant.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent(), manifest.Operations[0].OperationID)
	later, _ := r.publish(t, descendant)

	blocked, err := Drain(r.root(), Options{Command: "bn sync drain --test", Now: r.clock.Now})
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	requireHeldFor(t, blocked, manifest.PackageID, HoldConflict)
	requireHeldFor(t, blocked, later.PackageID, HoldPredecessor)
	if exists(t, r.pair.Local, "Notes/Delta.md") {
		t.Fatal("a change built on a held operation was applied anyway")
	}

	resolved(t, r, dir, previewed(t, r, manifest.PackageID, Choices{Default: KeepCurrent}))

	released, err := Drain(r.root(), Options{Command: "bn sync drain --test", Now: r.clock.Now})
	if err != nil {
		t.Fatalf("Drain after resolving: %v", err)
	}
	if len(released.Held) != 0 {
		t.Fatalf("resolving the conflict left %d packages held: %+v", len(released.Held), released.Held)
	}
	if got := r.pair.Local.Read(t, "Notes/Delta.md"); got != deltaBody {
		t.Fatalf("the descendant applied %q, want the packaged body", got)
	}
	if got := r.pair.Local.Read(t, "Alpha.md"); got != localAlphaBody {
		t.Fatalf("the descendant's application disturbed the resolution: Alpha.md holds %q", got)
	}
}

// TestResolveKeepCurrentRemovesANoteTheOperationResurrected is the choice
// applied to the conflict where "current" is an absence. The peer edited a note
// this vault deleted on purpose, so applying the operation brings it back and
// keeping the current version means deleting it a second time — which is what
// puts the peer's version into the history rather than discarding it unseen.
func TestResolveKeepCurrentRemovesANoteTheOperationResurrected(t *testing.T) {
	r := enroll(t)
	packaged := r.believes(t, "Notes/Gamma.md")
	// Committed rather than merely unlinked, and before the two replicas are
	// brought into step. A note removed from the working tree with nothing
	// explaining why is a refusal in its own right — the Git safety engine will
	// not commit an unexplained deletion — so an "already deleted here" fixture
	// that skipped the commit would be testing that refusal instead of this one.
	r.pair.Local.Git(t, "rm", "-q", "Notes/Gamma.md")
	r.pair.Local.Git(t, "commit", "-q", "-m", "remove the note this vault no longer wants")
	r.baseline(t)
	// The peer's edit is committed as well, and for the mirror-image reason: the
	// deletion that comes back has to drop exactly the bytes the peer's HEAD
	// holds, or the Git safety engine reads it as an unexplained removal. That is
	// what a real peer's autosave has already done by the time it exports.
	r.pair.Peer.Write(t, "Notes/Gamma.md", peerGammaBody)
	r.pair.Peer.Git(t, "add", "Notes/Gamma.md")
	r.pair.Peer.Git(t, "commit", "-q", "-m", "the peer edits the note this vault removed")

	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Gamma.md", []byte(peerGammaBody), packaged)
	})
	_, err := r.apply(t, dir)
	requireIs(t, err, ErrConflict, "applying a write onto a note deleted here")

	plan := previewed(t, r, manifest.PackageID, Choices{Default: KeepCurrent})
	resolution := resolved(t, r, dir, plan)
	requireEqual(t, resolution.Paths, []string{"Notes/Gamma.md"}, "the revision names")
	if exists(t, r.pair.Local, "Notes/Gamma.md") {
		t.Fatal("keeping this vault's version left the resurrected note behind")
	}
	// The resurrection is in the history even though it never outlived the
	// resolution, which is what makes the deletion reviewable and undoable.
	if got := r.pair.Local.Git(t, "show", "HEAD~1:Notes/Gamma.md"); got != strings.TrimSpace(peerGammaBody) {
		t.Fatalf("the applying commit holds %q, want the version that was rejected", got)
	}
	requireResolutionSettled(t, r, manifest.PackageID)

	r.converge(t)
	if exists(t, r.pair.Peer, "Notes/Gamma.md") {
		t.Fatal("the peer still holds the note the resolution deleted, so the replicas disagree")
	}
}

// TestResolveCrashHelper is the subprocess that dies between a resolution's two
// commits: the peer's version applied and acknowledged, the decision about it
// not yet made. It is the widest window the design knowingly opens, so it is the
// one an interruption has to be tested in.
func TestResolveCrashHelper(t *testing.T) {
	if os.Getenv("BN_RESOLVE_CRASH_HELPER") != "1" {
		t.Skip("subprocess helper; driven by TestAnInterruptedResolutionIsFinishedByTheNextPass")
	}
	r := enroll(t)
	manifest, dir := contest(t, r)
	report := crashReport{
		Root:       r.root(),
		Scratch:    filepath.Dir(r.pair.Dir),
		PackageDir: dir,
		PackageID:  manifest.PackageID,
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("encode report: %v", err)
	}
	if err := os.WriteFile(os.Getenv("BN_IMPORT_CRASH_REPORT"), data, 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}

	choice := KeepCurrent
	if os.Getenv("BN_RESOLVE_CRASH_CHOICE") == "keep-both" {
		choice = KeepBoth
	}
	plan := previewed(t, r, manifest.PackageID, Choices{Default: choice})
	point := os.Getenv("BN_IMPORT_CRASH_POINT")
	importCrashHook = func(fired string) {
		if fired == point {
			os.Exit(importCrashExitCode)
		}
	}
	if _, err := Resolve(r.root(), dir, plan, r.resolveOptions()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
}

// interruptResolution runs the helper to its death at point and returns what it
// left.
func interruptResolution(t *testing.T, point string) crashReport {
	t.Helper()
	return interruptChoosing(t, point, "keep-current")
}

// interruptChoosing is the same, for a resolution that made a different choice.
// Which one matters to the unwind: only a choice that *creates* a path leaves
// the interrupted revision something to delete.
func interruptChoosing(t *testing.T, point, choice string) crashReport {
	t.Helper()
	reportPath := filepath.Join(t.TempDir(), "report.json")
	output, err := runHelper(t, "TestResolveCrashHelper",
		"BN_RESOLVE_CRASH_HELPER=1",
		"BN_IMPORT_CRASH_POINT="+point,
		"BN_RESOLVE_CRASH_CHOICE="+choice,
		"BN_IMPORT_CRASH_REPORT="+reportPath,
	)
	if err != nil {
		t.Fatalf("a resolution interrupted at %q: %v\n%s", point, err, output)
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("the helper died before it said where it was working: %v\n%s", err, output)
	}
	var report crashReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	t.Cleanup(func() { discardScratch(t, report.Scratch) })
	return report
}

// TestAnInterruptedResolutionIsFinishedByTheNextPass is the durability claim
// that the two-commit design has to buy back.
//
// Between the two commits the vault holds the peer's version against somebody's
// explicit decision, and no amount of care makes that window zero — a content
// transaction and a state file cannot be made one write. So the decision is made
// durable *before* the application starts, and every entry point finishes it.
// Leaving it would silently resolve the conflict the other way, which is the
// worst outcome available: the person is told nothing and the vault holds the
// version they rejected.
func TestAnInterruptedResolutionIsFinishedByTheNextPass(t *testing.T) {
	vaultfixture.Isolate(t)
	report := interruptResolution(t, crashResolved)

	// What the dead process left: the operation applied, the decision promised.
	requireState(t, report.Root, map[string]string{"Alpha.md": alphaBody}, "after an interrupted resolution")
	promised := requirePromisedRevision(t, report.Root, report.PackageID)
	if publishedReceipt(t, report.Root, report.PackageID) == nil {
		t.Fatal("the application was interrupted before it acknowledged the package")
	}
	applied := crashCommitCount(t, report.Root)

	recovery, err := Recover(report.Root, Options{Command: "bn sync recover --test"})
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	requireEqual(t, recovery.Revised, []string{report.PackageID}, "the resumed resolutions")
	requireResolutionFinished(t, report, promised, applied)

	// Twice, because a recovery pass runs before every import and a second one
	// that changed anything would mean the first did not finish.
	again, err := Recover(report.Root, Options{Command: "bn sync recover --test"})
	if err != nil {
		t.Fatalf("Recover a second time: %v", err)
	}
	if len(again.Revised) != 0 {
		t.Fatalf("a second recovery pass revised %v", again.Revised)
	}
	requireResolutionFinished(t, report, promised, applied)
}

// requirePromisedRevision reads back the decision the dead process recorded, and
// returns the recovery entry it committed itself to writing under.
func requirePromisedRevision(t *testing.T, root, packageID string) *Plan {
	t.Helper()
	state, err := LoadState(root)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if _, applied := state.Applied[packageID]; !applied {
		t.Fatal("the resolution was interrupted before the package it revises was applied")
	}
	promised, found := state.Resolutions[packageID]
	if !found {
		t.Fatalf("nothing records the decision, so the vault silently holds the rejected version: %+v", state)
	}
	if promised.JournalEntry == "" {
		t.Fatal("the promised revision names no recovery entry, so a resumed pass could not tell it from one that ran")
	}
	if _, still := state.Conflicts[packageID]; still {
		t.Fatal("the package is both applied and held")
	}
	return promised
}

// requireResolutionFinished is what the vault must look like once the resumed
// pass has run: the decision carried out, under the identity it promised, with
// every durable trace of the resolution cleared.
func requireResolutionFinished(t *testing.T, report crashReport, promised *Plan, applied int) {
	t.Helper()
	requireState(t, report.Root, map[string]string{"Alpha.md": localAlphaBody}, "after the resolution was resumed")
	if got, want := crashCommitCount(t, report.Root), applied+1; got != want {
		t.Fatalf("the vault holds %d commits, want %d: the revision is one commit and is made once", got, want)
	}
	if !entryDirExists(t, report.Root, promised.JournalEntry) {
		t.Fatalf("the resumed revision did not write the entry %s it promised", promised.JournalEntry)
	}
	if _, err := journal.LoadEntry(noteops.JournalRoot(report.Root), promised.JournalEntry); err != nil {
		t.Fatalf("the resumed revision left no undo manifest: %v", err)
	}
	state, err := LoadState(report.Root)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.Resolutions) != 0 {
		t.Fatalf("the finished resolution is still promised: %v", state.Resolutions)
	}
	if state.Pending != nil {
		t.Fatalf("the resumed resolution left attempt %q pending", state.Pending.PackageID())
	}
	staged, err := resolutionDir(report.Root, report.PackageID)
	if err != nil {
		t.Fatalf("resolutionDir: %v", err)
	}
	if _, err := os.Lstat(staged); !os.IsNotExist(err) {
		t.Fatalf("the staged revision content survived the resumed resolution (%v)", err)
	}
	if dirty := crashDirty(t, report.Root); len(dirty) != 0 {
		t.Fatalf("the resumed resolution left the working tree dirty: %v", dirty)
	}
}

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

// TestPreviewRefusesKeepBothOnADivergentRename and its neighbours are the
// refusals for the conflict that has no second version at the contested path.
func TestPreviewRefusesAChoiceARenameCannotTake(t *testing.T) {
	cases := map[string]Choices{
		"keep-both":    {Default: KeepBoth},
		"manual merge": {Default: ManualMerge, Merged: map[string][]byte{"Alpha.md": []byte(mergedAlphaBody)}},
	}
	for name, choices := range cases {
		t.Run(name, func(t *testing.T) {
			r := enroll(t)
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
			_, err := r.apply(t, dir)
			requireIs(t, err, ErrConflict, "applying a divergent rename")

			_, err = Preview(r.root(), manifest.PackageID, choices)
			requireIs(t, err, ErrResolutionUnavailable, "answering a rename with a decision about content")
		})
	}
}

// TestALateEditAbandonsTheResolutionRatherThanWedgingTheVault is the failure
// mode the abandonment rule exists to prevent, and it is the worst one this
// package can have: not a lost note, but a vault that refuses every import
// forever.
//
// Every step of a revision is pinned to what applying the operation left on the
// path, so a note edited between the two commits refuses the whole transaction
// — permanently, since nothing puts the note back. And because the promise is
// finished before every import, drain, resolution and recovery, keeping it
// would mean one person's typing stops the vault synchronizing at all. So the
// decision is dropped: the edit is newer than the choice, the rejected version
// is still in both histories the application wrote, and the person is told on
// Recovery.Abandoned rather than left to find out.
func TestALateEditAbandonsTheResolutionRatherThanWedgingTheVault(t *testing.T) {
	vaultfixture.Isolate(t)
	report := interruptResolution(t, crashResolved)
	promised := requirePromisedRevision(t, report.Root, report.PackageID)

	// Somebody opens the note before the vault is next touched. What they are
	// looking at is the peer's version, since that is what the interrupted
	// resolution left there, and what they save is neither branch.
	late := alphaBody + "\nAnd a paragraph written before anything finished the resolution.\n"
	if err := os.WriteFile(filepath.Join(report.Root, "Alpha.md"), []byte(late), 0o644); err != nil {
		t.Fatalf("the late edit: %v", err)
	}

	recovery, err := Recover(report.Root, Options{Command: "bn sync recover --test"})
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	requireEqual(t, recovery.Abandoned, []string{report.PackageID}, "the abandoned resolutions")
	if len(recovery.Revised) != 0 {
		t.Fatalf("recovery revised %v over an edit that is newer than the decision", recovery.Revised)
	}
	// Which of the two abandonments it was, because a package name on a list
	// cannot tell "your edit was newer" from "your merge is damaged" and only one
	// of those is somebody's own doing.
	if why := recovery.AbandonedBecause[report.PackageID]; !errors.Is(why, ErrConflict) {
		t.Fatalf("the abandonment is reported as %v, not as the refusal that caused it", why)
	}
	if got := readVault(t, report.Root, "Alpha.md"); got != late {
		t.Fatalf("Alpha.md holds %q, want the later edit intact", got)
	}
	requireVaultUnwedged(t, report, promised)
}

// requireVaultUnwedged is the half of the claim that is about the vault rather
// than about the note: nothing durable is left pointing at the dropped
// decision, and the doors that run finishResolutions first all open again.
func requireVaultUnwedged(t *testing.T, report crashReport, promised *Plan) {
	t.Helper()
	state, err := LoadState(report.Root)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.Resolutions) != 0 {
		t.Fatalf("the abandoned decision is still promised, so every later import retries it: %v", state.Resolutions)
	}
	if _, applied := state.Applied[report.PackageID]; !applied {
		t.Fatal("the application the abandoned decision sat on top of was forgotten as well")
	}
	if entryDirExists(t, report.Root, promised.JournalEntry) {
		t.Fatalf("the refused revision left entry %s behind, which no manifest explains and no prune collects",
			promised.JournalEntry)
	}
	staged, err := resolutionDir(report.Root, report.PackageID)
	if err != nil {
		t.Fatalf("resolutionDir: %v", err)
	}
	if _, err := os.Lstat(staged); !os.IsNotExist(err) {
		t.Fatalf("the abandoned revision's staged content survived (%v)", err)
	}
	requireDoorsStillOpen(t, report)
}

// requireDoorsStillOpen is the unwedging itself: every entry point runs
// finishResolutions before anything else, which is exactly where a promise that
// can never be kept would refuse.
func requireDoorsStillOpen(t *testing.T, report crashReport) {
	t.Helper()
	again, err := Recover(report.Root, Options{Command: "bn sync recover --test"})
	if err != nil {
		t.Fatalf("a second recovery pass after abandoning: %v", err)
	}
	if len(again.Abandoned) != 0 || len(again.Revised) != 0 {
		t.Fatalf("a second recovery pass abandoned %v and revised %v", again.Abandoned, again.Revised)
	}
	if _, err := Drain(report.Root, Options{Command: "bn sync drain --test"}); err != nil {
		t.Fatalf("draining after abandoning a resolution: %v", err)
	}
}

// TestAnInterruptedRevisionIsTakenBackOutBeforeItIsRewritten is the roll-forward
// hazard stated as a test.
//
// internal/vault rolls a prepared transaction *forward* on the next lock
// acquisition and never back, so a revision killed between its commit and its
// undo manifest leaves content on the disk that nothing explains: no manifest,
// no content-history commit, and — for a step that deletes — no record
// authorizing the deletion, which is what the Git safety engine latches over.
// Re-driving cannot write it again either, because the path already holds it.
// So the interrupted attempt is taken back out from its own snapshots first,
// and the revision is then made properly under the identity it promised.
func TestAnInterruptedRevisionIsTakenBackOutBeforeItIsRewritten(t *testing.T) {
	vaultfixture.Isolate(t)
	report := interruptResolution(t, crashRevised)
	promised := requirePromisedRevision(t, report.Root, report.PackageID)
	applied := crashCommitCount(t, report.Root)

	// The decision is on the disk and nothing explains it: the entry exists and
	// has no manifest, so it is invisible to the recovery history and to Git.
	if got := readVault(t, report.Root, "Alpha.md"); got != localAlphaBody {
		t.Fatalf("Alpha.md holds %q, want the interrupted revision's content", got)
	}
	if _, err := journal.LoadEntry(noteops.JournalRoot(report.Root), promised.JournalEntry); err == nil {
		t.Fatal("the helper died after the undo manifest was written, so this proves nothing about the unwind")
	}

	recovery, err := Recover(report.Root, Options{Command: "bn sync recover --test"})
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	requireEqual(t, recovery.Revised, []string{report.PackageID}, "the resumed resolutions")
	requireResolutionFinished(t, report, promised, applied)

	// And the rewritten revision is a real operation rather than content that
	// happened to already be there: undoing it puts the rejected version back,
	// which is only true if the entry holds the snapshot the unwind restored.
	if _, err := journal.Restore(report.Root, noteops.JournalRoot(report.Root), promised.JournalEntry, journal.RestoreOptions{}); err != nil {
		t.Fatalf("undoing the rewritten revision: %v", err)
	}
	if got := readVault(t, report.Root, "Alpha.md"); got != alphaBody {
		t.Fatalf("undoing the rewritten revision left %q, want the rejected version back", got)
	}
}

// TestAResolutionRefusedBeforeItAppliesGivesTheHoldBack is the arm that decides
// what a durable promise means when the application returned no result at all.
//
// No result is not the same fact as no application — a failure after the commit
// leaves the change durable and the attempt pending, and the next pass rolls it
// forward — so the promise may only be dropped once this vault's own record
// says there is nothing for a revision to revise. Here there is nothing: the
// package is refused before a byte moves, so the decision is forgotten and the
// conflict goes back to being answerable.
func TestAResolutionRefusedBeforeItAppliesGivesTheHoldBack(t *testing.T) {
	r := enroll(t)
	manifest, dir := contest(t, r)
	plan := previewed(t, r, manifest.PackageID, Choices{Default: KeepCurrent})
	path, original := corruptOnePayload(t, dir)

	resolution, err := Resolve(r.root(), dir, plan, r.resolveOptions())
	if err == nil {
		t.Fatal("a package whose payload does not match its manifest was resolved anyway")
	}
	if resolution != nil {
		t.Fatalf("a refused resolution reported %+v, want nothing applied", resolution)
	}
	if got := r.pair.Local.Read(t, "Alpha.md"); got != localAlphaBody {
		t.Fatalf("Alpha.md holds %q, want this vault's own version untouched", got)
	}
	state := importState(t, r)
	if len(state.Resolutions) != 0 {
		t.Fatalf("a resolution that never applied anything is still promised: %v", state.Resolutions)
	}
	if _, held := state.Conflicts[manifest.PackageID]; !held {
		t.Fatalf("%s is neither applied nor held, so nothing can ever resolve it", manifest.PackageID)
	}
	if state.Pending != nil {
		t.Fatalf("the refused resolution left attempt %q pending", state.Pending.PackageID())
	}

	// Answerable means resolvable. Once the transfer has finished arriving, the
	// same conflict goes all the way through rather than staying behind a
	// decision nothing could carry out.
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("repair the payload: %v", err)
	}
	resolved(t, r, dir, previewed(t, r, manifest.PackageID, Choices{Default: KeepCurrent}))
	requireResolutionSettled(t, r, manifest.PackageID)
}

// corruptOnePayload rewrites one of a package's payload files with bytes of the
// same length, which is a package whose content disagrees with its manifest
// rather than one that is still arriving. It returns the path and what it
// replaced, so the test can put the package back.
func corruptOnePayload(t *testing.T, packageDir string) (string, []byte) {
	t.Helper()
	dir := filepath.Join(packageDir, vaultsync.PayloadsName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the payloads of %q: %v", packageDir, err)
	}
	if len(entries) == 0 {
		t.Fatalf("%q carries no payload to corrupt", packageDir)
	}
	path := filepath.Join(dir, entries[0].Name())
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), len(original)), 0o600); err != nil {
		t.Fatalf("corrupt payload: %v", err)
	}
	return path, original
}

// readVault reads one note out of a vault named by a crash report, where there
// is no fixture handle to ask.
func readVault(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

// TestAnInterruptedKeepBothSurvivesTheWatcherAdoptingItsDerivedCopy is the one
// thing the unwind must not take back, and the reason is asymmetric.
//
// Putting content back is a write, and a write needs no authorization. Removing
// a file the revision created is a deletion, and vaultgit commits a deletion
// only when an uncommitted journal record proves it was meant — the revision's
// record proves a creation, so it proves nothing here. While nothing tracks the
// derived copy that costs nothing. But `bn serve` in another process runs an
// autosave watcher that commits external changes, and it has the whole interval
// between the interruption and this pass to find one. Once the copy is in HEAD,
// removing it is an unexplained deletion, and the preflight that notices refuses
// *every* mutation this vault is asked for afterwards — starting with the
// re-drive a few lines later, and never clearing on its own. So the file is left
// where the watcher put it.
func TestAnInterruptedKeepBothSurvivesTheWatcherAdoptingItsDerivedCopy(t *testing.T) {
	vaultfixture.Isolate(t)
	report := interruptChoosing(t, crashRevised, "keep-both")
	promised := requirePromisedRevision(t, report.Root, report.PackageID)
	derived := requireDerivedCopy(t, report.Root)

	// Standing in for the autosave watcher in a `bn serve` this test does not
	// run: an external change found and committed while the resolver was gone.
	crashGit(t, report.Root, "add", "--", derived)
	crashGit(t, report.Root, "commit", "-q", "-m", "autosave: a copy another process found")

	if _, err := Recover(report.Root, Options{Command: "bn sync recover --test"}); err != nil {
		t.Fatalf("recovering a keep-both whose copy the watcher committed first: %v", err)
	}
	if _, err := os.Stat(filepath.Join(report.Root, derived)); err != nil {
		t.Fatalf("the unwind removed %q after the content history had adopted it, "+
			"which is the deletion nothing can authorize: %v", derived, err)
	}
	if kept, incoming := readVault(t, report.Root, derived), readVault(t, report.Root, "Alpha.md"); kept == incoming {
		t.Fatalf("keep-both left one version twice: %q", kept)
	}
	// Skipping the removal must not skip the rest of the unwind: the interrupted
	// revision's entry has no manifest, so ListEntries cannot see it and Prune
	// will never collect it, and leaving it is the orphan the unwind exists for.
	if entryDirExists(t, report.Root, promised.JournalEntry) {
		t.Fatalf("the manifest-less entry directory %s survived the unwind", promised.JournalEntry)
	}
	requireResolutionSettledAt(t, report.Root, report.PackageID)
	requireDoorsStillOpen(t, report)
}

// requireDerivedCopy finds the file keep-both wrote beside the contested note.
// Its name carries a digest, so the test cannot spell it and must not guess.
func requireDerivedCopy(t *testing.T, root string) string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read the vault root: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "(conflicted copy ") {
			return entry.Name()
		}
	}
	t.Fatal("the interrupted keep-both left no derived copy, so the unwind has nothing to delete " +
		"and this test proves nothing")
	return ""
}

// TestSplitNameKeepsADotfileWhole is the correction filepath.Ext cannot make.
//
// A leading dot is not an extension, it is the whole name, and treating it as
// one turns `.gitignore` into “ + `.gitignore` — so keep-both would write the
// copy to ` (conflicted copy <digest>).gitignore`, a name beginning with a space
// that no longer says which file it is a copy of.
func TestSplitNameKeepsADotfileWhole(t *testing.T) {
	for _, tc := range []struct{ rel, stem, ext string }{
		{".gitignore", ".gitignore", ""},
		{"Notes/.gitignore", "Notes/.gitignore", ""},
		{"Alpha.md", "Alpha", ".md"},
		{"Notes/Alpha.md", "Notes/Alpha", ".md"},
		{"Alpha.tar.gz", "Alpha.tar", ".gz"},
		{"Alpha", "Alpha", ""},
		{"Notes/Alpha", "Notes/Alpha", ""},
		{".config/Alpha.md", ".config/Alpha", ".md"},
		{"Notes.d/Alpha", "Notes.d/Alpha", ""},
	} {
		t.Run(tc.rel, func(t *testing.T) {
			stem, ext := splitName(tc.rel)
			if stem != tc.stem || ext != tc.ext {
				t.Fatalf("splitName(%q) = %q, %q; want %q, %q", tc.rel, stem, ext, tc.stem, tc.ext)
			}
		})
	}
}
