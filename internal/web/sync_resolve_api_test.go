package web

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultimport"
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
