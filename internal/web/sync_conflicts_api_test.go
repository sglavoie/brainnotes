package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultimport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// TestSyncConflictsPresentsBothVersionsOfATextConflict is the ordinary case and
// the one the page is mostly about: two rewrites of one note, both versions
// carried, the baseline available, and all four choices on offer.
func TestSyncConflictsPresentsBothVersionsOfATextConflict(t *testing.T) {
	w := newSyncWorld(t)
	packageID := w.holdTextConflict(t, "text")
	w.drain(t)

	operation := operationNamed(t, w.conflicts(t), packageID)
	if operation.OperationID != w.operationOf(t, "text") {
		t.Fatalf("operation = %q, want %q", operation.OperationID, w.operationOf(t, "text"))
	}
	if operation.Problem != "" {
		t.Fatalf("a readable hold reported a problem: %q", operation.Problem)
	}
	path := pathNamed(t, operation, "Alpha.md")
	if path.Kind != string(vaultimport.ConflictConcurrentEdit) {
		t.Fatalf("kind = %q", path.Kind)
	}
	assertVersionsShown(t, path, syncLocalAlpha, syncRemoteAlpha)
	if !path.Baseline.Shown || path.Baseline.Text == path.Local.Text {
		t.Fatalf("the baseline is not the version both sides started from: %+v", path.Baseline)
	}
	if !path.Mergeable {
		t.Fatal("two text versions were reported as unmergeable")
	}
	if len(path.Refused) != 0 {
		t.Fatalf("a plain concurrent edit refused a choice: %+v", path.Refused)
	}
	assertChoicesOffered(t, path, vaultimport.KeepCurrent, vaultimport.AcceptIncoming,
		vaultimport.KeepBoth, vaultimport.ManualMerge)
	// The other half of the neutral-wording rule: two versions on screen get the
	// sentence written for this kind, so a fallback widened by accident would fail
	// here rather than quietly describe every conflict in the vaguest terms it has.
	keep, _ := choiceNamed(t, path, string(vaultimport.KeepCurrent))
	if keep.Effect != syncChoiceEffects[vaultimport.ConflictConcurrentEdit][vaultimport.KeepCurrent].effect {
		t.Fatalf("a readable concurrent edit is worded generically: %q", keep.Effect)
	}
}

// TestSyncConflictsNamesEveryDiscardingChoice is the destructive-clarity contract
// in its machine-readable half: a choice that drops a version says so, and one
// that keeps both does not.
//
// The page draws Discards in the danger colour, so a choice that stopped setting
// it would silently become the one that reads as safe.
func TestSyncConflictsNamesEveryDiscardingChoice(t *testing.T) {
	w := newSyncWorld(t)
	text := w.holdTextConflict(t, "text")
	deletion := w.holdDeletionConflict(t, "deletion")
	settings := w.holdSettingsConflict(t, "settings")
	w.drain(t)

	view := w.conflicts(t)
	expected := map[string]map[string]string{
		"Alpha.md":        {"keep-current": "remote", "accept-incoming": "local", "keep-both": "", "manual-merge": ""},
		"Beta.md":         {"keep-current": "remote", "accept-incoming": "local", "manual-merge": ""},
		"brainnotes.toml": {"keep-current": "remote", "accept-incoming": "local", "manual-merge": ""},
	}
	for packageID, rel := range map[string]string{
		text: "Alpha.md", deletion: "Beta.md", settings: "brainnotes.toml",
	} {
		path := pathNamed(t, operationNamed(t, view, packageID), rel)
		want := expected[rel]
		if len(path.Available) != len(want) {
			t.Fatalf("%s offers %d choices, want %d: %+v", rel, len(path.Available), len(want), path.Available)
		}
		for _, choice := range path.Available {
			discards, known := want[choice.Choice]
			if !known {
				t.Fatalf("%s offers %s, which it should not", rel, choice.Choice)
			}
			if choice.Discards != discards {
				t.Fatalf("%s %s discards %q, want %q", rel, choice.Choice, choice.Discards, discards)
			}
		}
	}
}

// TestSyncConflictsRefusesTheChoicesEachKindCannotTake keeps the refusals on
// screen rather than absent: a choice missing with no reason reads as a page that
// forgot to offer it.
func TestSyncConflictsRefusesTheChoicesEachKindCannotTake(t *testing.T) {
	w := newSyncWorld(t)
	rename := w.holdRenameConflict(t, "rename")
	deletion := w.holdDeletionConflict(t, "deletion")
	settings := w.holdSettingsConflict(t, "settings")
	w.drain(t)

	view := w.conflicts(t)
	for _, want := range []struct {
		packageID string
		path      string
		refused   []string
	}{
		{rename, "Notes/Gamma.md", []string{"keep-both", "manual-merge"}},
		{deletion, "Beta.md", []string{"keep-both"}},
		{settings, "brainnotes.toml", []string{"keep-both"}},
	} {
		path := pathNamed(t, operationNamed(t, view, want.packageID), want.path)
		if len(path.Refused) != len(want.refused) {
			t.Fatalf("%s refused %+v, want %v", want.path, path.Refused, want.refused)
		}
		for i, choice := range want.refused {
			if path.Refused[i].Choice != choice {
				t.Fatalf("%s refusal %d is %s, want %s", want.path, i, path.Refused[i].Choice, choice)
			}
			if path.Refused[i].Reason == "" {
				t.Fatalf("%s refuses %s without saying why", want.path, choice)
			}
			if path.Refused[i].Label == "" {
				t.Fatalf("%s refuses %s with no label to draw", want.path, choice)
			}
		}
		if path.Remedy != "" {
			t.Fatalf("%s has choices and also a remedy: %q", want.path, path.Remedy)
		}
	}
}

// TestSyncConflictsDescribesADivergentRename pins the one kind whose contested
// path exists on neither side: the whole disagreement is where the note went, and
// only the baseline has content to show.
func TestSyncConflictsDescribesADivergentRename(t *testing.T) {
	w := newSyncWorld(t)
	packageID := w.holdRenameConflict(t, "rename")
	w.drain(t)

	path := pathNamed(t, operationNamed(t, w.conflicts(t), packageID), "Notes/Gamma.md")
	if path.Kind != string(vaultimport.ConflictDivergentRename) {
		t.Fatalf("kind = %q", path.Kind)
	}
	if path.RenamedTo != "Notes/Delta.md" {
		t.Fatalf("renamed to %q, want Notes/Delta.md", path.RenamedTo)
	}
	if path.Local.Exists || path.Remote.Exists {
		t.Fatalf("a divergent rename reported live content: local %+v remote %+v", path.Local, path.Remote)
	}
	if !path.Baseline.Shown || !strings.Contains(path.Baseline.Text, "Gamma body") {
		t.Fatalf("the baseline does not carry the note that moved: %+v", path.Baseline)
	}
	if path.Mergeable {
		t.Fatal("a rename was reported as mergeable")
	}
	if path.Summary == "" {
		t.Fatal("a divergent rename has no summary sentence")
	}
}

// TestSyncConflictsWillNotOfferAMergeItCannotSeed is the binary half of
// "appropriate binary choices". The engine would accept a manual merge here — it
// has no notion of an attachment — so the refusal is this layer's, and Mergeable
// is how the page knows not to open an editor over a PNG.
func TestSyncConflictsWillNotOfferAMergeItCannotSeed(t *testing.T) {
	w := newSyncWorld(t)
	packageID := w.holdAttachmentConflict(t, "attachment")
	w.drain(t)

	path := pathNamed(t, operationNamed(t, w.conflicts(t), packageID), syncAttachmentPath)
	for name, version := range map[string]syncVersionView{
		"this vault's": path.Local, "the peer's": path.Remote,
	} {
		if !version.Exists || !version.Binary || version.Shown || version.Text != "" {
			t.Fatalf("%s version of an attachment = %+v", name, version)
		}
		if version.Size == 0 || version.SHA256 == "" {
			t.Fatalf("%s version of an attachment carries neither size nor digest: %+v", name, version)
		}
	}
	if path.Mergeable {
		t.Fatal("an attachment was reported as mergeable")
	}
	if _, available := choiceNamed(t, path, string(vaultimport.ManualMerge)); !available {
		t.Fatal("the engine's own answer changed: a merge is now refused for an attachment")
	}
	assertEffectsAreContentNeutral(t, path)
	// Neutral wording is not a reason to stop naming the version that goes.
	assertNamesTheVersionItDiscards(t, path)
}

// TestSyncConflictsWordsATombstoneByItsKindAndAMergeNeutrally is the rest of the
// neutral-wording rule, on the one shape where its two halves disagree: an
// attachment the other replica deleted and this vault changed.
//
// Neither version can be shown, so a rule applied once per path would word every
// choice neutrally — and the neutral sentence for taking the other replica's
// version says "what is here is replaced", which puts a replacement where this
// decision makes a removal. The merge is the opposite case: its kind-specific
// sentence promises text about bytes nobody was shown, while the neutral one is
// true of a deletion as well.
func TestSyncConflictsWordsATombstoneByItsKindAndAMergeNeutrally(t *testing.T) {
	w := newSyncWorld(t)
	packaged := w.believes(t, syncAttachmentPath)
	w.pair.Local.Write(t, syncAttachmentPath, syncLocalAttachment)
	packageID := w.deliver(t, "deleted-attachment", func(b *vaultsync.Builder) {
		b.Delete(syncAttachmentPath, packaged)
	})
	w.drain(t)

	path := pathNamed(t, operationNamed(t, w.conflicts(t), packageID), syncAttachmentPath)
	if path.Kind != string(vaultimport.ConflictDeleteEdit) {
		t.Fatalf("kind = %q, want the other replica's deletion of a file changed here", path.Kind)
	}
	if !path.Local.Exists || !path.Local.Binary || path.Remote.Exists {
		t.Fatalf("the fixture is not an unreadable version against a tombstone: local %+v remote %+v",
			path.Local, path.Remote)
	}
	take := assertChoiceWorded(t, path, vaultimport.AcceptIncoming,
		syncChoiceEffects[vaultimport.ConflictDeleteEdit][vaultimport.AcceptIncoming].effect,
		"a decision that deletes the file is worded as a replacement")
	if take.Discards != syncDiscardsLocal {
		t.Fatalf("deleting bytes nobody here has seen discards nothing: %+v", take)
	}
	assertChoiceWorded(t, path, vaultimport.ManualMerge,
		syncGenericEffects[vaultimport.ManualMerge].effect,
		"a merge of bytes nobody was shown is worded as text")
	if path.Mergeable {
		t.Fatal("an unreadable version was reported as mergeable, so the editor would open over it")
	}
}

// TestSyncConflictsWillNotShowAVersionOverTheResponseBound is the other reason a
// version is withheld. It is deliberately separate from the binary case: the
// remedies differ, so the page says which one it is.
func TestSyncConflictsWillNotShowAVersionOverTheResponseBound(t *testing.T) {
	w := newSyncWorld(t)
	oversized := "---\ntitle: Alpha\n---\n\n" + strings.Repeat("a line of the peer's long note\n", 20000)
	packaged := w.believes(t, "Alpha.md")
	w.pair.Local.Write(t, "Alpha.md", syncLocalAlpha)
	packageID := w.deliver(t, "oversized", func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(oversized), packaged)
	})
	w.drain(t)

	path := pathNamed(t, operationNamed(t, w.conflicts(t), packageID), "Alpha.md")
	if len(oversized) <= syncVersionTextLimit {
		t.Fatalf("the fixture is %d bytes, which is under the bound", len(oversized))
	}
	if !path.Remote.TooLarge || path.Remote.Shown || path.Remote.Binary {
		t.Fatalf("the peer's oversized version = %+v", path.Remote)
	}
	if path.Remote.Size != len(oversized) {
		t.Fatalf("size = %d, want %d", path.Remote.Size, len(oversized))
	}
	if !path.Local.Shown {
		t.Fatalf("the version that fits was withheld with the one that does not: %+v", path.Local)
	}
	if path.Mergeable {
		t.Fatal("a version the page never carried was reported as mergeable")
	}
	// One version shown and one withheld is still a decision about bytes nobody has
	// read, so the wording drops to neutral here too — the half that *is* on screen
	// does not make "the text it has here" a fair description of the comparison.
	assertEffectsAreContentNeutral(t, path)
}

// TestSyncConflictsSaysWhenThisVaultMovedOnSinceTheHold is the late-arriving-change
// contract: an edit made here after the conflict was detected does not change what
// is contested, and a person deciding between two versions has to be told that the
// one on the left is not the one the hold recorded.
func TestSyncConflictsSaysWhenThisVaultMovedOnSinceTheHold(t *testing.T) {
	w := newSyncWorld(t)
	packageID := w.holdTextConflict(t, "text")
	w.drain(t)

	before := pathNamed(t, operationNamed(t, w.conflicts(t), packageID), "Alpha.md")
	if before.LocalChanged {
		t.Fatalf("an untouched vault reported a late local change: %+v", before)
	}

	later := syncLocalAlpha + "\nA sentence added while the conflict waited.\n"
	w.pair.Local.Write(t, "Alpha.md", later)

	after := pathNamed(t, operationNamed(t, w.conflicts(t), packageID), "Alpha.md")
	if !after.LocalChanged {
		t.Fatal("this vault's version changed after the hold and the page did not say so")
	}
	if after.Local.Text != later {
		t.Fatalf("this vault's version = %q, want the text now on disk", after.Local.Text)
	}
	if after.Remote.Text != syncRemoteAlpha {
		t.Fatalf("a local edit moved the peer's version: %q", after.Remote.Text)
	}
}

// TestSyncConflictsGivesARemedyWhenNoChoiceIsLeft is the dead-end case: a name
// occupied here by something that is not a note refuses all four choices, so the
// path must carry the one thing that does resolve it.
func TestSyncConflictsGivesARemedyWhenNoChoiceIsLeft(t *testing.T) {
	w := newSyncWorld(t)
	occupied := filepath.Join(w.root(), "Notes", "Delta.md")
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatalf("occupy the name: %v", err)
	}
	if err := os.WriteFile(filepath.Join(occupied, "Inside.md"), []byte("inside\n"), 0o600); err != nil {
		t.Fatalf("write inside the occupied name: %v", err)
	}
	w.pair.Local.Commit(t, "a directory took a note's name")
	packageID := w.deliver(t, "occupied", func(b *vaultsync.Builder) {
		b.Write("Notes/Delta.md", []byte("the peer's note\n"), vaultsync.Absent())
	})
	w.drain(t)

	path := pathNamed(t, operationNamed(t, w.conflicts(t), packageID), "Notes/Delta.md")
	if path.Kind != string(vaultimport.ConflictFilenameCollision) {
		t.Fatalf("kind = %q, want %q", path.Kind, vaultimport.ConflictFilenameCollision)
	}
	if len(path.Available) != 0 {
		t.Fatalf("a name this vault cannot clear offered a choice: %+v", path.Available)
	}
	if len(path.Refused) != 4 {
		t.Fatalf("refused %d choices, want all four: %+v", len(path.Refused), path.Refused)
	}
	if path.Remedy == "" {
		t.Fatal("a path with no choice and no remedy is a dead end")
	}
	if !strings.Contains(path.Remedy, "next pass") {
		t.Fatalf("the remedy does not say the change is still waiting: %q", path.Remedy)
	}
}

// assertWaitingOnAPredecessor fails unless every blocked package is held for a
// causal predecessor and says enough to be recognized. A dependent listed with no
// origin and no position is a row a person cannot match to anything.
func assertWaitingOnAPredecessor(t *testing.T, blocked []syncBlockedView) {
	t.Helper()

	for _, waiting := range blocked {
		if waiting.Reason != string(vaultimport.HoldPredecessor) {
			t.Fatalf("%s is held for %q, want %q",
				waiting.PackageID, waiting.Reason, vaultimport.HoldPredecessor)
		}
		if waiting.Detail == "" || waiting.Sequence == 0 || waiting.OriginDevice == "" {
			t.Fatalf("a blocked package is reported with nothing to identify it: %+v", waiting)
		}
	}
}

// TestSyncConflictsShowsWhatIsWaitingBehindAConflict is the dependency inversion:
// the engine says which package each one waits on, and the page has to turn that
// round so a conflict can say what resolving it would release.
func TestSyncConflictsShowsWhatIsWaitingBehindAConflict(t *testing.T) {
	w := newSyncWorld(t)
	packageID := w.holdTextConflict(t, "text")
	next := w.dependent(t, "next", "Notes/New1.md", "one\n", w.operationOf(t, "text"))
	later := w.dependent(t, "later", "Notes/New2.md", "two\n", w.operationOf(t, "next"))
	w.drain(t)

	operation := operationNamed(t, w.conflicts(t), packageID)
	if len(operation.Blocked) != 2 {
		t.Fatalf("blocked = %+v, want the two packages behind this one", operation.Blocked)
	}
	direct, transitive := operation.Blocked[0], operation.Blocked[1]
	if direct.PackageID != next || direct.WaitingOn != packageID {
		t.Fatalf("the direct dependent = %+v, want %s waiting on %s", direct, next, packageID)
	}
	if transitive.PackageID != later || transitive.WaitingOn != next {
		t.Fatalf("the transitive dependent = %+v, want %s waiting on %s", transitive, later, next)
	}
	assertWaitingOnAPredecessor(t, operation.Blocked)
	if len(w.conflicts(t).Operations) != 1 {
		t.Fatal("a package waiting behind a conflict was itself listed as a conflict")
	}
}

// TestSyncConflictsTerminatesOnAWaitingCycle is the tolerance the engine has and
// this page had to inherit without inheriting the spin. Two packages each waiting
// on an operation the other carries stall forever; the review route still answers,
// and neither appears behind an unrelated conflict, because nothing waits on it.
func TestSyncConflictsTerminatesOnAWaitingCycle(t *testing.T) {
	w := newSyncWorld(t)
	packageID := w.holdTextConflict(t, "text")
	first, err := vaultsync.NewOperationID()
	if err != nil {
		t.Fatalf("NewOperationID: %v", err)
	}
	second, err := vaultsync.NewOperationID()
	if err != nil {
		t.Fatalf("NewOperationID: %v", err)
	}
	w.deliverOperation(t, "cycle-first", first, func(b *vaultsync.Builder) {
		b.Write("Notes/First.md", []byte("first\n"), vaultsync.Absent(), second)
	})
	w.deliverOperation(t, "cycle-second", second, func(b *vaultsync.Builder) {
		b.Write("Notes/Second.md", []byte("second\n"), vaultsync.Absent(), first)
	})
	w.drain(t)

	pending := w.inspect(t)
	if len(pending) != 3 {
		t.Fatalf("the channel holds %d packages, want 3", len(pending))
	}
	stalled := 0
	for _, entry := range pending {
		if entry.Reason == vaultimport.HoldPredecessor && entry.Blocker != "" {
			stalled++
		}
	}
	if stalled != 2 {
		t.Fatalf("%d packages are waiting on each other, want 2", stalled)
	}

	operation := operationNamed(t, w.conflicts(t), packageID)
	if len(operation.Blocked) != 0 {
		t.Fatalf("a cycle nothing depends on was reported behind this conflict: %+v", operation.Blocked)
	}
}

// TestSyncConflictsDropsAHoldThatIsAlreadyGone is the race the route is written
// for: Inspect takes no lock, so a drain or a CLI resolution can settle a package
// between the scan and the read of its offers. It is driven directly because the
// window cannot be held open from outside.
func TestSyncConflictsDropsAHoldThatIsAlreadyGone(t *testing.T) {
	w := newSyncWorld(t)

	vanished := vaultimport.Pending{
		PackageID: "pkg-00000000000000000000000000000000",
		Reason:    vaultimport.HoldConflict,
		Conflict: &vaultimport.ConflictRecord{
			PackageID: "pkg-00000000000000000000000000000000",
			Paths:     []vaultimport.PathConflict{{Path: "Alpha.md", Kind: vaultimport.ConflictConcurrentEdit}},
		},
	}
	if _, holding := w.srv.syncOperationView(vanished, nil); holding {
		t.Fatal("a hold the vault no longer records was reported as a conflict to decide")
	}
}

// TestSyncConflictsIsRefusedAsAWrite is the same read-only route class the status
// route is in.
func TestSyncConflictsIsRefusedAsAWrite(t *testing.T) {
	w := newSyncWorld(t)

	code, body := w.post(t, syncConflictsAPIRoutePath, syncResolveRequest{Token: syncTestToken})
	if code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d, want %d: %s", code, http.StatusMethodNotAllowed, body)
	}
}

// TestSyncConflictsAnswersAnEmptyChannelAsAnArray is a shape contract the browser
// depends on: it iterates the operations without a null check.
func TestSyncConflictsAnswersAnEmptyChannelAsAnArray(t *testing.T) {
	w := newSyncWorld(t)

	view := w.conflicts(t)
	if !view.OK {
		t.Fatal("an empty channel answered with ok false")
	}
	if view.Operations == nil {
		t.Fatal("operations decoded as null on a vault with no conflicts")
	}
	if len(view.Operations) != 0 {
		t.Fatalf("operations = %+v on a vault with no conflicts", view.Operations)
	}
}
