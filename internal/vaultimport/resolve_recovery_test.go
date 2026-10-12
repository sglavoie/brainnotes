package vaultimport

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultfixture"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

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
