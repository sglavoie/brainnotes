package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/vaultsyncd"
)

// TestSyncStatusCountsWhatIsWaitingForADecision is the status line's whole job on
// a vault that needs somebody: the headline, the count in the sentence, and the
// link to where the decision is made.
func TestSyncStatusCountsWhatIsWaitingForADecision(t *testing.T) {
	w := newSyncWorld(t)
	w.holdTextConflict(t, "text")
	w.holdSettingsConflict(t, "settings")
	w.drain(t)

	status := w.status(t)
	if !status.OK || status.Mode != "local" || !status.Enrolled {
		t.Fatalf("status = %+v, want an enrolled local vault", status)
	}
	if status.Headline != string(vaultsyncd.StateConflict) {
		t.Fatalf("headline = %q, want %q", status.Headline, vaultsyncd.StateConflict)
	}
	if status.Peer.Conflicts != 2 {
		t.Fatalf("conflicts = %d, want 2", status.Peer.Conflicts)
	}
	if status.Summary != "2 changes are waiting for a decision." {
		t.Fatalf("summary = %q", status.Summary)
	}
	if !status.NeedsPerson {
		t.Fatal("a vault holding two conflicts reported that it needs nobody")
	}
	if status.ReviewPath != syncRoutePath {
		t.Fatalf("review path = %q, want %q", status.ReviewPath, syncRoutePath)
	}
}

// TestSyncStatusNeverCallsDeliveryApplication is the distinction the session's
// completion turns on. Five packages are on this disk and none of them is this
// vault's content yet, so every "arrived" count is five and every "done" count is
// zero — a status that added them together would report the conflicts as applied.
func TestSyncStatusNeverCallsDeliveryApplication(t *testing.T) {
	w := newSyncWorld(t)
	w.holdTextConflict(t, "text")
	w.holdAttachmentConflict(t, "attachment")
	w.holdRenameConflict(t, "rename")
	w.holdDeletionConflict(t, "deletion")
	w.holdSettingsConflict(t, "settings")
	w.drain(t)

	status := w.status(t)
	if status.Peer.Delivered != 5 || status.Peer.AwaitingApplication != 5 {
		t.Fatalf("delivered = %d, awaiting = %d, want 5 and 5", status.Peer.Delivered, status.Peer.AwaitingApplication)
	}
	if status.Peer.Applied != 0 {
		t.Fatalf("applied = %d, want 0: nothing has been applied", status.Peer.Applied)
	}
	if status.Transport.Acknowledged != 0 {
		t.Fatalf("acknowledged = %d, want 0: this vault has acknowledged nothing", status.Transport.Acknowledged)
	}
	if status.Peer.LastDeliveryAt == "" {
		t.Fatal("five packages arrived and the status names no delivery time")
	}
}

// TestSyncStatusOffersNoReviewWhenThereIsNothingToDecide is the other half of
// ReviewPath: a link that were always present would offer an empty page.
func TestSyncStatusOffersNoReviewWhenThereIsNothingToDecide(t *testing.T) {
	w := newSyncWorld(t)

	status := w.status(t)
	if status.ReviewPath != "" {
		t.Fatalf("review path = %q on a vault with no conflicts", status.ReviewPath)
	}
	if status.Peer.Conflicts != 0 || status.Peer.Delivered != 0 {
		t.Fatalf("peer = %+v, want an empty channel", status.Peer)
	}
	if status.Replica != nil {
		t.Fatalf("a writable vault reported replica freshness: %+v", status.Replica)
	}
	if status.Summary == "" {
		t.Fatal("the status line has no sentence to show")
	}
	if status.States == nil {
		t.Fatal("states decoded as null; the browser iterates it unconditionally")
	}
}

// TestSyncStatusNeverServesTheVaultLocation checks the whole response rather than
// one field. The projection exists partly to keep Status.Root out of a page, and
// a new field carrying an absolute path would pass every field-by-field test.
func TestSyncStatusNeverServesTheVaultLocation(t *testing.T) {
	w := newSyncWorld(t)
	w.holdTextConflict(t, "text")
	w.drain(t)

	code, body := w.get(t, syncStatusAPIRoutePath)
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", syncStatusAPIRoutePath, code, body)
	}
	if strings.Contains(body, w.root()) {
		t.Fatalf("the status response carries this vault's location: %s", body)
	}
}

// TestSyncWorkerErrorsNameTheVaultWithoutItsLocation drives the projection
// directly, because a failing worker's recorded diagnostic is the one place a
// vault path reaches this route and provoking a real failure would prove less:
// what matters is that the substitution happens wherever the text comes from.
func TestSyncWorkerErrorsNameTheVaultWithoutItsLocation(t *testing.T) {
	root := "/home/someone/vault"
	workers := []vaultsyncd.WorkerStatus{{
		Name:      vaultsyncd.WorkerExport,
		Failures:  3,
		LastError: "vaultexport: " + root + "/.bnotes/sync/export.json could not be written",
	}}

	views := syncWorkerViewsOf(workers, root)
	if len(views) != 1 {
		t.Fatalf("views = %d, want 1", len(views))
	}
	if strings.Contains(views[0].LastError, root) {
		t.Fatalf("last error still names the vault location: %q", views[0].LastError)
	}
	if !strings.Contains(views[0].LastError, "<vault>/.bnotes/sync/export.json") {
		t.Fatalf("last error lost the path it was about: %q", views[0].LastError)
	}
	if views[0].Failures != 3 || views[0].Name != string(vaultsyncd.WorkerExport) {
		t.Fatalf("view = %+v, want the export worker's three failures", views[0])
	}
}

// TestSyncStatusRefusesWriteMethods pins the read-only route class: the status
// route answers a question and must not be reachable as a write.
func TestSyncStatusRefusesWriteMethods(t *testing.T) {
	w := newSyncWorld(t)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req, err := http.NewRequest(method, w.ts.URL+syncStatusAPIRoutePath, nil)
		if err != nil {
			t.Fatalf("new %s request: %v", method, err)
		}
		req.Header.Set(csrfHeaderName, syncTestToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, syncStatusAPIRoutePath, err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s = %d, want %d: %s", method, resp.StatusCode, http.StatusMethodNotAllowed, body)
		}
		// Both read methods, because the route answers both: HEAD missing from the
		// header is a client told the route does not take a request it does take.
		if got, want := resp.Header.Get("Allow"), "GET, HEAD"; got != want {
			t.Fatalf("%s Allow = %q, want %q", method, got, want)
		}
	}
}

// TestSyncStatusAnswersAReplicaAboutItselfAndRefusesTheRest is the read-only
// mode's whole synchronization surface.
//
// The replica here is an enrolled vault with five real conflicts on disk, which
// is the case that matters: the route must answer about the copy rather than
// about the synchronization state it can see and cannot act on, and the review
// surface must be refused outright rather than rendered and then failed.
func TestSyncStatusAnswersAReplicaAboutItselfAndRefusesTheRest(t *testing.T) {
	writable := newSyncWorld(t)
	writable.holdTextConflict(t, "text")
	writable.holdSettingsConflict(t, "settings")
	writable.drain(t)

	state := t.TempDir()
	writable.serve(t, func(opts *Options) {
		opts.ReadOnly = true
		opts.StateDir = state
	})

	status := writable.status(t)
	if status.Mode != "read-only" {
		t.Fatalf("mode = %q, want read-only", status.Mode)
	}
	if status.Replica == nil {
		t.Fatal("a replica reported no freshness at all")
	}
	if status.Replica.Recorded {
		t.Fatalf("replica = %+v, want no recorded copy", status.Replica)
	}
	if !strings.Contains(status.Summary, "no verified copy has been recorded") {
		t.Fatalf("summary = %q", status.Summary)
	}
	if status.Peer.Conflicts != 0 || status.Enrolled {
		t.Fatalf("the replica answered from the vault's own sync state: %+v", status)
	}
	if status.ReviewPath != "" {
		t.Fatalf("a replica offered %q, which it refuses", status.ReviewPath)
	}

	assertReplicaRefusesTheDecisionSurface(t, writable)
}

// assertReplicaRefusesTheDecisionSurface is the containment half: the review page
// and every route a decision travels through are refused outright by the guard,
// rather than rendered and then failed at the write.
func assertReplicaRefusesTheDecisionSurface(t *testing.T, replica *syncWorld) {
	t.Helper()

	for _, route := range []string{syncRoutePath, syncConflictsAPIRoutePath} {
		code, body := replica.get(t, route)
		if code != http.StatusForbidden {
			t.Fatalf("GET %s = %d, want %d: %s", route, code, http.StatusForbidden, body)
		}
		if !strings.Contains(body, "read-only replica") {
			t.Fatalf("GET %s refusal does not name the replica: %s", route, body)
		}
	}
	for _, route := range []string{syncPreviewAPIRoutePath, syncResolveAPIRoutePath} {
		code, body := replica.post(t, route, syncResolveRequest{Token: syncTestToken})
		if code != http.StatusForbidden {
			t.Fatalf("POST %s = %d, want %d: %s", route, code, http.StatusForbidden, body)
		}
	}
}
