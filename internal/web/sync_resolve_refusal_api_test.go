package web

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultimport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

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
