package web

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultimport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// decide is one path's decision, as the page sends it.
func decide(path string, choice vaultimport.Choice) syncPathChoiceRequest {
	return syncPathChoiceRequest{Path: path, Choice: string(choice)}
}

// merge is a manual merge carrying the assembled text.
func merge(path, merged string) syncPathChoiceRequest {
	return syncPathChoiceRequest{Path: path, Choice: string(vaultimport.ManualMerge), Merged: &merged}
}

// resolve redeems a receipt, returning the status and the raw body.
func (w *syncWorld) resolve(t *testing.T, previewID string) (int, string) {
	t.Helper()

	return w.post(t, syncResolveAPIRoutePath,
		propertyApplyRequest{Token: syncTestToken, PreviewID: previewID})
}

// previewOK plans a resolution and fails unless it was accepted.
func (w *syncWorld) previewOK(t *testing.T, packageID string, paths ...syncPathChoiceRequest) syncPlanView {
	t.Helper()

	code, plan, body := w.preview(t, syncResolveRequest{
		Token: syncTestToken, PackageID: packageID, Paths: paths,
	})
	if code != http.StatusOK {
		t.Fatalf("POST %s = %d: %s", syncPreviewAPIRoutePath, code, body)
	}
	return plan
}

// previewRefused plans a resolution that must be refused and returns the code a
// client acts on.
func (w *syncWorld) previewRefused(t *testing.T, packageID string, paths ...syncPathChoiceRequest) string {
	t.Helper()

	code, _, body := w.preview(t, syncResolveRequest{
		Token: syncTestToken, PackageID: packageID, Paths: paths,
	})
	if code == http.StatusOK {
		t.Fatalf("POST %s was accepted: %s", syncPreviewAPIRoutePath, body)
	}
	return errorCodeOf(t, body)
}

// resolveOK redeems a receipt and fails unless the resolution succeeded.
func (w *syncWorld) resolveOK(t *testing.T, previewID string) syncResolvedView {
	t.Helper()

	code, body := w.resolve(t, previewID)
	if code != http.StatusOK {
		t.Fatalf("POST %s = %d: %s", syncResolveAPIRoutePath, code, body)
	}
	var view syncResolvedView
	decodeSyncJSON(t, body, &view)
	return view
}

// TestSyncPreviewShowsThePathsADecisionDerives is why the plan is shown at all:
// "keep both" writes a second file whose name nobody chose, and a person
// authorizing the decision has to see it before it exists.
func TestSyncPreviewShowsThePathsADecisionDerives(t *testing.T) {
	w := newSyncWorld(t)
	packageID := w.holdTextConflict(t, "text")
	w.drain(t)

	plan := w.previewOK(t, packageID, decide("Alpha.md", vaultimport.KeepBoth))
	assertPlanIsAbout(t, plan, packageID, w.operationOf(t, "text"), "Alpha.md")
	decided := plan.Paths[0]
	if decided.Choice != string(vaultimport.KeepBoth) ||
		decided.Kind != string(vaultimport.ConflictConcurrentEdit) {
		t.Fatalf("plan path = %+v", decided)
	}
	derived := derivedCopyOf(t, decided, "Alpha.md")
	if _, err := os.Stat(w.pair.Local.Path(derived)); !os.IsNotExist(err) {
		t.Fatalf("the preview wrote %s", derived)
	}
}

// assertPlanIsAbout fails unless the plan carries a receipt and is about the
// package, operation and paths the decision named.
func assertPlanIsAbout(t *testing.T, plan syncPlanView, packageID, operationID string, paths ...string) {
	t.Helper()

	if plan.PreviewID == "" || plan.ExpiresAt == "" {
		t.Fatalf("the plan carries no receipt: %+v", plan)
	}
	if plan.PackageID != packageID || plan.OperationID != operationID {
		t.Fatalf("the plan is about %s/%s, want %s/%s",
			plan.PackageID, plan.OperationID, packageID, operationID)
	}
	if len(plan.Paths) != len(paths) {
		t.Fatalf("the plan decides %d paths, want %d: %+v", len(plan.Paths), len(paths), plan.Paths)
	}
	for i, want := range paths {
		if plan.Paths[i].Path != want {
			t.Fatalf("plan path %d = %q, want %q", i, plan.Paths[i].Path, want)
		}
	}
}

// derivedCopyOf is the path a decision writes that nobody typed, which is the one
// thing about a plan a person cannot predict from their own choice.
func derivedCopyOf(t *testing.T, decided syncPlanPathView, contested string) string {
	t.Helper()

	derived := ""
	for _, step := range decided.Steps {
		if step.Path != contested {
			derived = step.Path
		}
	}
	if derived == "" {
		t.Fatalf("%s planned no derived copy: %+v", decided.Choice, decided.Steps)
	}
	base := strings.TrimSuffix(contested, ".md")
	if !strings.HasPrefix(derived, base+" (conflicted copy ") || !strings.HasSuffix(derived, ").md") {
		t.Fatalf("the derived path is %q, which nobody would recognize as their note", derived)
	}
	return derived
}

// TestSyncPreviewSaysNothingWillBeRevisedForAnAcceptance earns its own assertion:
// taking the other replica's version is the one decision the applied package
// already satisfies, so an empty step list is the correct answer rather than a
// plan that failed to describe itself.
func TestSyncPreviewSaysNothingWillBeRevisedForAnAcceptance(t *testing.T) {
	w := newSyncWorld(t)
	packageID := w.holdTextConflict(t, "text")
	w.drain(t)

	plan := w.previewOK(t, packageID, decide("Alpha.md", vaultimport.AcceptIncoming))
	if len(plan.Paths) != 1 {
		t.Fatalf("plan paths = %+v", plan.Paths)
	}
	if plan.Paths[0].Steps == nil {
		t.Fatal("steps decoded as null; the browser renders the list unconditionally")
	}
	if len(plan.Paths[0].Steps) != 0 {
		t.Fatalf("accepting the peer's version planned a revision: %+v", plan.Paths[0].Steps)
	}
}

// TestSyncPreviewNamesTheChoiceTheWayTheControlDoes keeps the wording in one
// place. The page renders the plan a person reads immediately before authorizing
// it, and the engine's slug is jargon exactly there; carrying the label from
// syncChoiceLabels means the plan and the radio that chose it cannot drift into
// two vocabularies for one decision.
func TestSyncPreviewNamesTheChoiceTheWayTheControlDoes(t *testing.T) {
	w := newSyncWorld(t)
	packageID := w.holdTextConflict(t, "text")
	w.drain(t)

	for _, choice := range []vaultimport.Choice{vaultimport.KeepCurrent, vaultimport.KeepBoth} {
		plan := w.previewOK(t, packageID, decide("Alpha.md", choice))
		decided := plan.Paths[0]
		if want := syncChoiceLabels[choice]; decided.Label != want {
			t.Fatalf("%s is labelled %q, want %q", choice, decided.Label, want)
		}
		if decided.Label == decided.Choice {
			t.Fatalf("%s reached the page as its own slug", choice)
		}
	}
}

// TestSyncResolveKeepsThisVaultsVersionInTwoCommits is the resolution contract in
// full, and the middle commit is the load-bearing part: the vault holds the peer's
// version between the two writes, which is what puts the rejected version in the
// content history where an undo can reach it.
func TestSyncResolveKeepsThisVaultsVersionInTwoCommits(t *testing.T) {
	w := newSyncWorld(t)
	packageID := w.holdTextConflict(t, "text")
	w.drain(t)
	before := w.commit(t, "the local edit that conflicts")

	plan := w.previewOK(t, packageID, decide("Alpha.md", vaultimport.KeepCurrent))
	resolved := w.resolveOK(t, plan.PreviewID)

	if !resolved.OK || resolved.PackageID != packageID {
		t.Fatalf("resolved = %+v", resolved)
	}
	if resolved.JournalID == "" {
		t.Fatal("the resolution recorded no undo entry")
	}
	if !resolved.Acknowledged {
		t.Fatalf("the peer was not acknowledged: %s", resolved.Summary)
	}
	if !strings.Contains(resolved.Summary, "your decision was recorded") {
		t.Fatalf("summary = %q", resolved.Summary)
	}

	if got := w.pair.Local.Read(t, "Alpha.md"); got != syncLocalAlpha {
		t.Fatalf("the note holds %q, want this vault's version", got)
	}
	if count := w.pair.Local.Git(t, "rev-list", "--count", before+"..HEAD"); count != "2" {
		t.Fatalf("the resolution made %s commits, want 2", count)
	}
	// Git's stdout is trimmed by the fixture, so the comparison is against the
	// peer's version without its trailing newline rather than against a different
	// body.
	if got := w.pair.Local.Git(t, "show", "HEAD~1:Alpha.md"); got != strings.TrimRight(syncRemoteAlpha, "\n") {
		t.Fatalf("HEAD~1 holds %q, want the peer's version the decision rejected", got)
	}
}

// TestSyncResolveSettlesEveryConflictFlavour is the session's completion clause at
// the handler layer: each of the five disagreements a person meets is decided
// through these two routes and the vault holds what was chosen afterwards.
func TestSyncResolveSettlesEveryConflictFlavour(t *testing.T) {
	w := newSyncWorld(t)
	text := w.holdTextConflict(t, "text")
	attachment := w.holdAttachmentConflict(t, "attachment")
	rename := w.holdRenameConflict(t, "rename")
	deletion := w.holdDeletionConflict(t, "deletion")
	settings := w.holdSettingsConflict(t, "settings")
	w.drain(t)
	w.commit(t, "the local edits that conflict")

	merged := "---\nstatus: review\ntitle: Alpha\n---\n\nBoth bodies, reconciled by hand.\n"
	mergedConfig := w.pair.Local.Read(t, "brainnotes.toml") +
		"\n[frontmatter.properties.org]\ntype = \"string\"\n"
	for _, settle := range []syncSettlement{
		{
			name: "text", packageID: text, decision: merge("Alpha.md", merged),
			holds: map[string]string{"Alpha.md": merged},
		},
		{
			name: "attachment", packageID: attachment,
			decision: decide(syncAttachmentPath, vaultimport.AcceptIncoming),
			holds:    map[string]string{syncAttachmentPath: syncRemoteAttachment},
		},
		{
			name: "rename", packageID: rename,
			decision: decide("Notes/Gamma.md", vaultimport.AcceptIncoming),
			// Both names, deliberately: taking the peer's rename writes its name and
			// leaves this vault's own arrangement alone, which is what the choice's
			// wording says and is why it discards nothing.
			exists: []string{"Notes/Delta.md", "Archive/Gamma.md"},
		},
		{
			name: "deletion", packageID: deletion,
			decision: decide("Beta.md", vaultimport.KeepCurrent),
			holds:    map[string]string{"Beta.md": syncLocalBeta},
		},
		{
			name: "settings", packageID: settings,
			decision: merge("brainnotes.toml", mergedConfig),
			mentions: map[string][]string{
				"brainnotes.toml": {"properties.area", "properties.org"},
			},
		},
	} {
		t.Run(settle.name, func(t *testing.T) {
			w.settle(t, settle)
		})
	}

	w.assertNothingLeftToDecide(t, 5)
}

// syncSettlement is one conflict decided, with what the vault must hold afterwards
// stated rather than checked by a closure: the five decisions differ in what they
// leave behind, not in how that is asserted.
type syncSettlement struct {
	name      string
	packageID string
	decision  syncPathChoiceRequest
	// holds is a path and the exact content the decision leaves there.
	holds map[string]string
	// exists is a path the decision leaves in place, whatever it holds.
	exists []string
	// mentions is a path and the fragments its content must still carry, which is
	// how a merged configuration is asserted without pinning its whole text.
	mentions map[string][]string
}

// settle decides one conflict through both routes and checks what it left.
func (w *syncWorld) settle(t *testing.T, settle syncSettlement) {
	t.Helper()

	plan := w.previewOK(t, settle.packageID, settle.decision)
	resolved := w.resolveOK(t, plan.PreviewID)
	if !resolved.Acknowledged {
		t.Fatalf("%s was resolved without acknowledging the peer: %s", settle.name, resolved.Summary)
	}
	for path, want := range settle.holds {
		if got := w.pair.Local.Read(t, path); got != want {
			t.Fatalf("%s: %s holds %q, want %q", settle.name, path, got, want)
		}
	}
	for _, path := range settle.exists {
		if _, err := os.Stat(w.pair.Local.Path(path)); err != nil {
			t.Fatalf("%s: %s is not there: %v", settle.name, path, err)
		}
	}
	for path, fragments := range settle.mentions {
		content := w.pair.Local.Read(t, path)
		for _, fragment := range fragments {
			if !strings.Contains(content, fragment) {
				t.Fatalf("%s: %s lost %q: %s", settle.name, path, fragment, content)
			}
		}
	}
	w.assertNotHeld(t, settle)
}

// assertNotHeld is the channel half of a settlement.
//
// The package directory stays where the transport left it — retirement is the
// sender's, on the strength of the receipt — so what must be gone is the hold:
// nothing is waiting for a decision about this package any more.
func (w *syncWorld) assertNotHeld(t *testing.T, settle syncSettlement) {
	t.Helper()

	for _, entry := range w.inspect(t) {
		if entry.PackageID == settle.packageID && entry.Reason == vaultimport.HoldConflict {
			t.Fatalf("%s is still waiting for a decision after being resolved: %s",
				settle.name, entry.Detail)
		}
	}
}

// assertNothingLeftToDecide is what every surface must say once the decisions are
// made: no conflicts, no review link, and the applications counted.
func (w *syncWorld) assertNothingLeftToDecide(t *testing.T, applied int) {
	t.Helper()

	status := w.status(t)
	if status.Peer.Conflicts != 0 {
		t.Fatalf("conflicts = %d after every decision was made", status.Peer.Conflicts)
	}
	if status.Peer.Applied != applied {
		t.Fatalf("applied = %d, want the %d packages the decisions applied", status.Peer.Applied, applied)
	}
	if status.ReviewPath != "" {
		t.Fatalf("the status still offers %q with nothing left to decide", status.ReviewPath)
	}
	if view := w.conflicts(t); len(view.Operations) != 0 {
		t.Fatalf("the review route still lists %d conflicts", len(view.Operations))
	}
}

// TestSyncResolveRejectsAStaleDecisionWithoutDiscardingTheDraft is the durability
// boundary this route exists to hold: a decision planned against a version this
// vault has since moved past is refused with nothing written, and everything the
// person needs in order to decide again — the hold, both versions, a merge they
// can compose once more — is still there.
func TestSyncResolveRejectsAStaleDecisionWithoutDiscardingTheDraft(t *testing.T) {
	w := newSyncWorld(t)
	packageID := w.holdTextConflict(t, "text")
	w.drain(t)
	head := w.commit(t, "the local edit that conflicts")

	composed := "---\nstatus: review\ntitle: Alpha\n---\n\nA merge composed before the interruption.\n"
	plan := w.previewOK(t, packageID, merge("Alpha.md", composed))

	// The late-arriving change: somebody edits the contested note in this vault
	// after the plan was built and before it was authorized.
	late := syncLocalAlpha + "\nOne more sentence, typed while the dialog was open.\n"
	w.pair.Local.Write(t, "Alpha.md", late)

	code, body := w.resolve(t, plan.PreviewID)
	if code != http.StatusConflict {
		t.Fatalf("POST %s = %d, want %d: %s", syncResolveAPIRoutePath, code, http.StatusConflict, body)
	}
	if got := errorCodeOf(t, body); got != string(noteops.CodePreviewStale) {
		t.Fatalf("error code = %q, want %q: %s", got, noteops.CodePreviewStale, body)
	}
	if strings.Contains(body, w.root()) {
		t.Fatalf("the refusal names this vault's location: %s", body)
	}

	if got := w.pair.Local.Read(t, "Alpha.md"); got != late {
		t.Fatalf("the note holds %q, want the late edit the refusal protected", got)
	}
	if w.pair.Local.Head(t) != head {
		t.Fatal("a refused resolution committed something")
	}
	path := pathNamed(t, operationNamed(t, w.conflicts(t), packageID), "Alpha.md")
	if !path.LocalChanged {
		t.Fatal("the review route does not say this vault moved on, which is why the decision failed")
	}
	if path.Remote.Text != syncRemoteAlpha || path.Local.Text != late {
		t.Fatalf("a version is missing on the second reading: local %q remote %q",
			path.Local.Text, path.Remote.Text)
	}
	if !path.Mergeable {
		t.Fatal("the merge cannot be composed again, so the draft has nowhere to go")
	}
}

// TestSyncResolveSpendsAReceiptOnce is the replay refusal every preview-gated
// write here shares, and it matters more for this one: the receipt carries the
// plan, so a redeemable receipt would be a decision applicable twice.
func TestSyncResolveSpendsAReceiptOnce(t *testing.T) {
	w := newSyncWorld(t)
	packageID := w.holdTextConflict(t, "text")
	w.drain(t)
	w.commit(t, "the local edit that conflicts")

	plan := w.previewOK(t, packageID, decide("Alpha.md", vaultimport.KeepCurrent))
	w.resolveOK(t, plan.PreviewID)
	head := w.pair.Local.Head(t)

	code, body := w.resolve(t, plan.PreviewID)
	if code != http.StatusConflict {
		t.Fatalf("POST %s = %d, want %d: %s", syncResolveAPIRoutePath, code, http.StatusConflict, body)
	}
	if got := errorCodeOf(t, body); got != string(noteops.CodePreviewRequired) {
		t.Fatalf("error code = %q, want %q", got, noteops.CodePreviewRequired)
	}
	if w.pair.Local.Head(t) != head {
		t.Fatal("the replay committed something")
	}
}

// TestSyncResolveWillNotRedeemAnotherFamilysReceipt is the kind separation: the
// store is shared with the property and settings writes, so a receipt issued there
// must not be spendable as a conflict resolution — which would reach Resolve with
// no plan at all.
func TestSyncResolveWillNotRedeemAnotherFamilysReceipt(t *testing.T) {
	w := newSyncWorld(t)

	receipt, err := w.srv.propertyPreviews.store(propertyPreviewRecord{Kind: propertyPreviewSetting})
	if err != nil {
		t.Fatalf("store a settings receipt: %v", err)
	}
	code, body := w.resolve(t, receipt.ID)
	if code != http.StatusConflict {
		t.Fatalf("POST %s = %d, want %d: %s", syncResolveAPIRoutePath, code, http.StatusConflict, body)
	}
	if got := errorCodeOf(t, body); got != string(noteops.CodePreviewRequired) {
		t.Fatalf("error code = %q, want %q", got, noteops.CodePreviewRequired)
	}
}

// TestSyncResolveRequiresAPlan pins the gate itself: this route decides nothing
// from its own request body.
func TestSyncResolveRequiresAPlan(t *testing.T) {
	w := newSyncWorld(t)

	code, body := w.resolve(t, "")
	if code != http.StatusConflict {
		t.Fatalf("POST %s = %d, want %d: %s", syncResolveAPIRoutePath, code, http.StatusConflict, body)
	}
	if got := errorCodeOf(t, body); got != string(noteops.CodePreviewRequired) {
		t.Fatalf("error code = %q, want %q", got, noteops.CodePreviewRequired)
	}
}

// TestSyncPreviewRefusesADecisionThatIsNotOne collects the request-level refusals,
// and the first is the one this layer exists for: the engine has a request-wide
// default, and a page showing every contested path individually must not reach for
// it, because a path nobody looked at would inherit a destructive answer.
func TestSyncPreviewRefusesADecisionThatIsNotOne(t *testing.T) {
	w := newSyncWorld(t)
	alpha := w.believes(t, "Alpha.md")
	gamma := w.believes(t, "Notes/Gamma.md")
	w.pair.Local.Write(t, "Alpha.md", syncLocalAlpha)
	w.pair.Local.Write(t, "Notes/Gamma.md", syncLocalGamma)
	packageID := w.deliver(t, "two", func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(syncRemoteAlpha), alpha)
		b.Write("Notes/Gamma.md", []byte("---\ntitle: Gamma\n---\n\nRewritten on the peer.\n"), gamma)
	})
	w.drain(t)

	contested := operationNamed(t, w.conflicts(t), packageID)
	if len(contested.Paths) != 2 {
		t.Fatalf("the package contests %d paths, want 2: %+v", len(contested.Paths), contested.Paths)
	}

	for _, refusal := range []struct {
		name  string
		paths []syncPathChoiceRequest
	}{
		{"one path left undecided", []syncPathChoiceRequest{
			decide("Alpha.md", vaultimport.KeepCurrent),
		}},
		{"one path decided twice", []syncPathChoiceRequest{
			decide("Alpha.md", vaultimport.KeepCurrent),
			decide("Alpha.md", vaultimport.AcceptIncoming),
			decide("Notes/Gamma.md", vaultimport.KeepCurrent),
		}},
		{"a choice the engine does not name", []syncPathChoiceRequest{
			decide("Alpha.md", vaultimport.Choice("keep-the-nicer-one")),
			decide("Notes/Gamma.md", vaultimport.KeepCurrent),
		}},
		{"a merge with no text", []syncPathChoiceRequest{
			{Path: "Alpha.md", Choice: string(vaultimport.ManualMerge)},
			decide("Notes/Gamma.md", vaultimport.KeepCurrent),
		}},
		{"a merge larger than this page can carry", []syncPathChoiceRequest{
			merge("Alpha.md", strings.Repeat("x", syncMergeLimit+1)),
			decide("Notes/Gamma.md", vaultimport.KeepCurrent),
		}},
		{"a decision naming no path", []syncPathChoiceRequest{
			decide("   ", vaultimport.KeepCurrent),
		}},
	} {
		t.Run(refusal.name, func(t *testing.T) {
			got := w.previewRefused(t, packageID, refusal.paths...)
			if got != string(noteops.CodeInvalidRequest) {
				t.Fatalf("error code = %q, want %q", got, noteops.CodeInvalidRequest)
			}
		})
	}
}

// TestSyncPreviewRefusesAChoiceTheConflictCannotTake is the other half of the
// review route's refused list: the page is told which choices are unavailable, and
// asking for one anyway is refused before the vault is touched.
func TestSyncPreviewRefusesAChoiceTheConflictCannotTake(t *testing.T) {
	w := newSyncWorld(t)
	packageID := w.holdSettingsConflict(t, "settings")
	w.drain(t)

	got := w.previewRefused(t, packageID, decide("brainnotes.toml", vaultimport.KeepBoth))
	if got != string(noteops.CodeInvalidRequest) {
		t.Fatalf("error code = %q, want %q", got, noteops.CodeInvalidRequest)
	}
}

// TestSyncPreviewAnswersAboutPackagesItIsNotHolding separates the two refusals a
// page can provoke by being out of date: a package that is not in the channel at
// all, and one that is there for a reason no decision settles.
func TestSyncPreviewAnswersAboutPackagesItIsNotHolding(t *testing.T) {
	w := newSyncWorld(t)
	held := w.holdTextConflict(t, "text")
	waiting := w.dependent(t, "dependent", "Notes/Epsilon.md", "Waiting behind the conflict.\n",
		w.operationOf(t, "text"))
	w.drain(t)

	unknown := "pkg-00000000000000000000000000000000"
	got := w.previewRefused(t, unknown, decide("Alpha.md", vaultimport.KeepCurrent))
	if got != string(noteops.CodeNotFound) {
		t.Fatalf("an unknown package answered %q, want %q", got, noteops.CodeNotFound)
	}
	got = w.previewRefused(t, waiting, decide("Notes/Epsilon.md", vaultimport.KeepCurrent))
	if got != string(noteops.CodeConflict) {
		t.Fatalf("a package held behind a conflict answered %q, want %q", got, noteops.CodeConflict)
	}
	if _, err := os.Stat(w.pair.Local.Path("Notes/Epsilon.md")); !os.IsNotExist(err) {
		t.Fatal("a refused preview applied the waiting package")
	}
	if operationNamed(t, w.conflicts(t), held).PackageID != held {
		t.Fatal("the conflict this vault is holding is gone after two refused previews")
	}
}

// TestSyncMergeValidationRefusesBytesThatAreNotText drives validMerge directly,
// because the check cannot be provoked through the route: a JSON string is decoded
// with every invalid byte and lone surrogate already replaced by U+FFFD, which is
// valid UTF-8. The check is defence for any other caller of this type, and a test
// that could only be written over HTTP would have to claim it is unreachable.
func TestSyncMergeValidationRefusesBytesThatAreNotText(t *testing.T) {
	invalid := "half a rune: \xc3"
	decided := syncPathChoiceRequest{
		Path:   "Alpha.md",
		Choice: string(vaultimport.ManualMerge),
		Merged: &invalid,
	}
	err := decided.validMerge(vaultimport.ManualMerge)
	if err == nil {
		t.Fatal("a merge that is not text was accepted")
	}
	if got := noteops.CodeOf(err); got != noteops.CodeInvalidRequest {
		t.Fatalf("code = %q, want %q", got, noteops.CodeInvalidRequest)
	}
	if !strings.Contains(err.Error(), "not valid text") {
		t.Fatalf("error = %q", err)
	}
	for _, choice := range []vaultimport.Choice{
		vaultimport.KeepCurrent, vaultimport.AcceptIncoming, vaultimport.KeepBoth,
	} {
		if err := (syncPathChoiceRequest{Path: "Alpha.md"}).validMerge(choice); err != nil {
			t.Fatalf("%s required a merge: %v", choice, err)
		}
	}
}

// acknowledgedApplication is an application whose receipt exists, which is the
// only evidence this vault can have that the peer will stop offering the package.
func acknowledgedApplication() *vaultimport.Result {
	return &vaultimport.Result{Receipt: &vaultsync.Receipt{}}
}

// TestSyncResolvedSummaryClaimsOnlyTheCommitsThereWere drives the success sentence
// over the three states a finished resolution has.
//
// Both halves are claims a person acts on rather than wording. "Your decision was
// recorded" sends somebody to the history to find an entry, and an accepted
// incoming change has none there: the application was the whole of the decision.
// Saying nothing about the peer says the peer knows, which an unacknowledged
// resolution has not established.
func TestSyncResolvedSummaryClaimsOnlyTheCommitsThereWere(t *testing.T) {
	for _, tc := range []struct {
		name       string
		resolution *vaultimport.Resolution
		want       string
	}{
		{
			name:       "a decision recorded on top of the application",
			resolution: &vaultimport.Resolution{JournalEntry: "entry-1", Applied: acknowledgedApplication()},
			want:       "The change was applied and your decision was recorded.",
		},
		{
			name:       "an acceptance with nothing left to revise",
			resolution: &vaultimport.Resolution{Applied: acknowledgedApplication()},
			want: "The change was applied, and it needed nothing revised on top of it: " +
				"what it carries is what you chose.",
		},
		{
			name:       "finished here and unfinished between the replicas",
			resolution: &vaultimport.Resolution{JournalEntry: "entry-1", Applied: &vaultimport.Result{}},
			want: "The change was applied and your decision was recorded. " +
				"The other replica has not been told yet and will keep offering it until it is.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view := syncResolvedViewOf(tc.resolution, "/vaults/under-test")
			if view.Summary != tc.want {
				t.Fatalf("summary = %q, want %q", view.Summary, tc.want)
			}
		})
	}
}

// syncPartialAnswerCase is one failed resolution and the answer it is owed.
type syncPartialAnswerCase struct {
	name       string
	resolution *vaultimport.Resolution
	plan       *vaultimport.Plan
	err        error
	wantCode   noteops.ErrorCode
	says       []string
	silentOn   []string
}

// runSyncPartialAnswerCases holds syncPartialResolutionError to the four halves of
// its contract on every case: the state it names, the engine's own sentence kept
// verbatim, the classification, and the failure still being reachable — the last
// because a caller that logs the cause reads the engine's error and not this one.
func runSyncPartialAnswerCases(t *testing.T, root string, cases []syncPartialAnswerCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := syncPartialResolutionError(tc.resolution, tc.plan, tc.err, root)
			if err == nil {
				t.Fatal("a failed resolution answered with no error")
			}
			if got := noteops.CodeOf(err); got != tc.wantCode {
				t.Fatalf("code = %q, want %q: %v", got, tc.wantCode, err)
			}
			assertAnswerSaysAndIsSilent(t, err, tc.says, tc.silentOn)
			if !errors.Is(err, tc.err) {
				t.Fatalf("the engine's failure is not reachable from the answer: %v", err)
			}
		})
	}
}

// TestSyncPartialResolutionErrorTellsTheUnrecordedStatesApart is the reason the
// plan is an argument at all.
//
// An empty Resolution.JournalEntry is three states, not one, and each is a
// different instruction to the person reading the answer: wait for a commit that is
// coming, stop waiting for one that is not, or nothing was ever owed. Only the plan
// and the failure together say which, so a surface holding the error alone can give
// two of these three answers wrong while looking entirely reasonable.
func TestSyncPartialResolutionErrorTellsTheUnrecordedStatesApart(t *testing.T) {
	const root = "/vaults/under-test"
	runSyncPartialAnswerCases(t, root, []syncPartialAnswerCase{
		{
			name:       "the peer's version is here and the decision is not recorded",
			resolution: &vaultimport.Resolution{Applied: acknowledgedApplication()},
			plan:       revisingTestPlan(),
			err:        errors.New("the revision could not be committed"),
			wantCode:   noteops.CodeInternal,
			says: []string{
				"the other replica's version was applied and your decision has not been recorded yet",
				"will be finished automatically on the next synchronization pass",
				"What failed: the revision could not be committed",
			},
			silentOn: []string{"undoable"},
		},
		{
			name:       "the decision is not recorded and no pass will record it",
			resolution: &vaultimport.Resolution{Applied: acknowledgedApplication()},
			plan:       revisingTestPlan(),
			// One of the three sentinels internal/vaultimport abandons a promise on,
			// which is the whole difference from the case above: the state file no
			// longer holds the promise, so there is nothing left to finish and this
			// vault is sitting on the version the person rejected.
			err:      fmt.Errorf("%w: Alpha.md moved under the revision", vaultimport.ErrConflict),
			wantCode: noteops.CodeInternal,
			says: []string{
				"your decision could not be recorded on top of it, and it will not be tried again",
				"this vault is holding the version you decided against",
			},
			silentOn: []string{"will be finished automatically"},
		},
		{
			name:       "an acceptance that owed no second commit",
			resolution: &vaultimport.Resolution{Applied: acknowledgedApplication()},
			// A plan that revises nothing is an accept-incoming decision, carried out
			// by the application alone. Reading its empty JournalEntry as an
			// outstanding promise would send somebody to wait for a commit that was
			// never owed, and hide the failure that actually happened.
			plan:     &vaultimport.Plan{PackageID: "pkg-1"},
			err:      errors.New("the acknowledgment could not be published"),
			wantCode: noteops.CodeInternal,
			says: []string{
				"the change was applied and there was nothing to record on top of it",
				"what it carries is what you chose",
				"It is undoable.",
			},
			silentOn: []string{"has not been recorded yet", "will not be tried again"},
		},
	})
}

// TestSyncPartialResolutionErrorSaysWhatIsDurable is the rest of the failure
// answer's contract: a resolution whose decision *was* recorded and something
// after it failed, an earlier promise the recovery pass dropped on the way in, and
// the plain failure that applied nothing at all. Each has to name what is durable,
// keep the engine's own sentence, and be classified rather than called internal.
func TestSyncPartialResolutionErrorSaysWhatIsDurable(t *testing.T) {
	const root = "/vaults/under-test"
	dropped := map[string]error{"pkg-earlier": errors.New("the application it revises never landed")}
	runSyncPartialAnswerCases(t, root, []syncPartialAnswerCase{
		{
			name:       "both commits landed and the peer was never told",
			resolution: &vaultimport.Resolution{JournalEntry: "entry-1", Applied: &vaultimport.Result{}},
			plan:       revisingTestPlan(),
			err:        errors.New("the acknowledgment could not be published"),
			wantCode:   noteops.CodeInternal,
			says: []string{
				"the change was applied and your decision was recorded, and both are undoable",
				"The other replica has not been told, so it will keep offering the change",
			},
			silentOn: []string{"What failed came afterwards"},
		},
		{
			name:       "everything landed and the tail failed",
			resolution: &vaultimport.Resolution{JournalEntry: "entry-1", Applied: acknowledgedApplication()},
			plan:       revisingTestPlan(),
			err: &vault.TxnRecoveryError{
				Path:         root + "/Alpha.md",
				RecoveryPath: root + "/.bnotes/tmp/alpha",
				Err:          errors.New("the destination could not be put back"),
			},
			// A transaction that could not put content back is a safety answer and
			// not a retry: the sentence naming where the content is sitting is the
			// only thing that recovers it.
			wantCode: noteops.CodeSafety,
			says:     []string{"What failed came afterwards", "<vault>/Alpha.md"},
			silentOn: []string{root},
		},
		{
			name: "an earlier decision was dropped on the way in",
			resolution: &vaultimport.Resolution{
				JournalEntry:         "entry-1",
				Applied:              acknowledgedApplication(),
				AbandonedResolutions: dropped,
			},
			plan:     revisingTestPlan(),
			err:      errors.New("the acknowledgment could not be published"),
			wantCode: noteops.CodeInternal,
			says: []string{
				"Dropped on the way in, from an earlier attempt: " +
					"pkg-earlier (the application it revises never landed)",
			},
		},
		{
			name:       "nothing landed at all",
			resolution: nil,
			err:        errStaleTestPreview(),
			wantCode:   noteops.CodePreviewStale,
			says:       []string{"sync: "},
			// With no Resolution there is nothing durable to report, and a message
			// saying a change was applied would be the worst available answer.
			silentOn: []string{"was applied", "undoable"},
		},
	})
}

// assertAnswerSaysAndIsSilent holds one answer to both halves of its contract: the
// sentences it owes a person, and the ones that would be false of this outcome.
//
// It is out here rather than inside the table's subtest because what it costs there
// is nesting — a table, a subtest closure and two fragment loops in one function —
// and the two halves are the same assertion whichever failure is being described.
func assertAnswerSaysAndIsSilent(t *testing.T, err error, says, silentOn []string) {
	t.Helper()

	for _, fragment := range says {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("the answer does not say %q: %v", fragment, err)
		}
	}
	for _, fragment := range silentOn {
		if strings.Contains(err.Error(), fragment) {
			t.Fatalf("the answer says %q, which is not true of this outcome: %v", fragment, err)
		}
	}
}

// revisingTestPlan is a plan that has a second commit to make — one step, which is
// all Plan.Revises() asks — so an empty JournalEntry beside it is a decision that
// has not been recorded rather than one there was never anything to record.
func revisingTestPlan() *vaultimport.Plan {
	return &vaultimport.Plan{
		PackageID: "pkg-1",
		Paths: []vaultimport.PathPlan{{
			Path:   "Alpha.md",
			Kind:   vaultimport.ConflictConcurrentEdit,
			Choice: vaultimport.KeepCurrent,
			Steps:  []vaultimport.Step{{Path: "Alpha.md", Action: vaultsync.ActionWrite, SHA256: "sha-local"}},
		}},
	}
}

// errStaleTestPreview is a staleness refusal as the engine raises one, which is
// the failure that comes back with no resolution at all.
func errStaleTestPreview() error {
	return fmt.Errorf("%w: pkg-1 was decided against a version this vault has moved past",
		vaultimport.ErrStalePreview)
}
