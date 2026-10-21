package vaultimport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/journal"
	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/vaultfixture"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

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
