package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// TestSourceSaveRequiresTheCSRFToken is the management envelope applied to these
// routes. It is asserted here rather than assumed because all three writes are
// new doors into the vault.
func TestSourceSaveRequiresTheCSRFToken(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})

	form := url.Values{}
	form.Set("key", "alpha")
	form.Set("content", "---\ntitle: Alpha\n---\n\nno token.\n")
	form.Set("revision", vault.HashBytes([]byte(sourceTestNote)))
	for _, route := range []string{sourcePreviewAPIRoutePath, sourceSaveAPIRoutePath, sourceCreateAPIRoutePath} {
		decodeSourceError(t, postSourceRoute(t, ts, route, form, ""), http.StatusForbidden, noteops.CodeForbidden)
	}
	if got := readSourceNote(t, root); got != sourceTestNote {
		t.Fatalf("a save without a token wrote anyway:\n%q", got)
	}
}

// TestSourceRoutesAreUnavailableOnAReadOnlyReplica pins the deliberate choice
// recorded on registerSourceRoutes: none of the four is on the replica's
// allowlist, the read included. A replica cannot accept a save, and a route that
// hands out the revision one would need is an editing affordance on a published
// copy.
func TestSourceRoutesAreUnavailableOnAReadOnlyReplica(t *testing.T) {
	// A replica keeps its own state outside the vault it publishes, which
	// validateReplicaOptions requires before the mode resolves at all.
	ts, _, root := newSourceTestServer(t, Options{ReadOnly: true, StateDir: t.TempDir()})

	resp := getSourceDocument(t, ts, "alpha")
	payload := decodeSourceError(t, resp, http.StatusForbidden, noteops.CodeForbidden)
	if !strings.Contains(payload.Message, "read-only replica") {
		t.Fatalf("message = %q, want the allowlist refusal", payload.Message)
	}

	form := url.Values{}
	form.Set("key", "alpha")
	form.Set("content", "---\ntitle: Alpha\n---\n\nreplica write.\n")
	form.Set("revision", vault.HashBytes([]byte(sourceTestNote)))
	form.Set("rel_path", "Drafts/Delta.md")
	for _, route := range []string{sourcePreviewAPIRoutePath, sourceSaveAPIRoutePath, sourceCreateAPIRoutePath} {
		decodeSourceError(t, postSourceRoute(t, ts, route, form, sourceTestToken),
			http.StatusForbidden, noteops.CodeForbidden)
	}
	if got := readSourceNote(t, root); got != sourceTestNote {
		t.Fatalf("a replica wrote to the vault:\n%q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "Drafts")); err == nil {
		t.Fatal("a refused create left a folder behind")
	}
}

// TestSourceSaveWorksOnAHostedVaultWithinItsOwnBound covers the other half of
// the profile decision. Hosted has documentWrites, so the save must work — and
// the remote body bound is a megabyte, which form-encoding a note exceeds long
// before noteops' own content limit, so the route's widening has to be in force
// there rather than only locally.
func TestSourceSaveWorksOnAHostedVaultWithinItsOwnBound(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{Hosted: true})
	edited := "---\ntitle: Alpha\n---\n\n" + strings.Repeat("hosted body. ", 200_000)

	form := url.Values{}
	form.Set("key", "alpha")
	form.Set("content", edited)
	form.Set("revision", vault.HashBytes([]byte(sourceTestNote)))
	if len(form.Encode()) <= remoteMaxRequestBody {
		t.Fatalf("the request is %d bytes, which does not exceed the remote profile bound", len(form.Encode()))
	}
	decodeSourceSave(t, postSourceRoute(t, ts, sourceSaveAPIRoutePath, form, sourceTestToken))
	if got := readSourceNote(t, root); got != edited {
		t.Fatalf("the hosted save did not store the submitted bytes (%d bytes on disk)", len(got))
	}
}

// TestSourceRoutesStayBoundedByTheirOwnLimit is the other side of that
// widening: it moves the bound from the profile to the route rather than
// removing it, so a request over the route's own limit is still refused.
func TestSourceRoutesStayBoundedByTheirOwnLimit(t *testing.T) {
	for _, mode := range []Mode{ModeLocal, ModeHosted} {
		caps := capabilitiesFor(mode)
		for _, route := range []string{sourcePreviewAPIRoutePath, sourceSaveAPIRoutePath, sourceCreateAPIRoutePath} {
			if got := caps.requestBodyLimit(route); got < sourceRequestBodyLimit {
				t.Fatalf("%s mode bounds %s at %d, want at least the route's own %d",
					mode, route, got, sourceRequestBodyLimit)
			}
		}
		if got := caps.requestBodyLimit(sourceAPIRoutePath); got != caps.maxRequestBody {
			t.Fatalf("%s mode widened the read route to %d", mode, got)
		}
	}

	// Hosted is where the widening is the operative bound — its profile allowance
	// is a megabyte, so the route's own number is what a request is held to and
	// the guard answers an over-length one with an honest 413.
	ts, _, root := newSourceTestServer(t, Options{Hosted: true, CSRFToken: sourceTestToken})
	form := url.Values{}
	form.Set("key", "alpha")
	form.Set("content", strings.Repeat("x", sourceRequestBodyLimit+1))
	resp := postSourceRoute(t, ts, sourceSaveAPIRoutePath, form, sourceTestToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
	if got := readSourceNote(t, root); got != sourceTestNote {
		t.Fatalf("an over-limit save wrote anyway:\n%q", got)
	}
}

// TestSourceSaveRefusesABodyOverTheRouteBoundOnALocalVault is the local half of
// the same bound. The local profile allows more than these routes do, so the
// number that refuses is the one requireManagementPost applies, and its refusal
// is a malformed-request 400 rather than the guard's 413. Either way the save
// does not happen, which is the property worth pinning: the widening moved the
// bound to the route, it did not remove it.
func TestSourceSaveRefusesABodyOverTheRouteBoundOnALocalVault(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})

	form := url.Values{}
	form.Set("key", "alpha")
	form.Set("content", strings.Repeat("x", sourceRequestBodyLimit+1))
	decodeSourceError(t, postSourceRoute(t, ts, sourceSaveAPIRoutePath, form, sourceTestToken),
		http.StatusBadRequest, noteops.CodeInvalidRequest)
	if got := readSourceNote(t, root); got != sourceTestNote {
		t.Fatalf("an over-limit save wrote anyway:\n%q", got)
	}
}

// TestSourceSaveErrorTellsALandedSaveFromARefusedOne is the response shape for
// the snapshot-failure case.
//
// internal/mutationmatrix proves the real failure arrives here coded
// snapshot_failed with the bytes on disk; what this pins is what the body then
// says. A 500 with no revision would invite a resubmission planned against a
// revision this save already replaced, which is how an honest snapshot failure
// turns into a lost edit — so the landed revision and changed: true are part of
// the contract, and a refusal before the write must carry neither.
func TestSourceSaveErrorTellsALandedSaveFromARefusedOne(t *testing.T) {
	_, srv, _ := newSourceTestServer(t, Options{})
	req := noteops.SaveSourceRequest{RelPath: "Alpha.md", Content: sourceTestNote}
	landed := &noteops.SaveSourceResult{
		Destination: noteops.PathResolution{RelPath: "Alpha.md"},
		Revision:    vault.HashBytes([]byte(sourceTestNote)),
		Changed:     true,
	}

	recorder := httptest.NewRecorder()
	srv.writeSourceSaveError(recorder, req, landed,
		noteops.Errorf(noteops.CodeSnapshotFailed, "the save completed and is on disk"))
	payload := decodeSourceError(t, recorder.Result(), http.StatusInternalServerError, noteops.CodeSnapshotFailed)
	if payload.Revision != landed.Revision || !payload.Changed {
		t.Fatalf("payload = %+v, want the landed revision and changed: true", payload)
	}
	if payload.Key != "alpha" || payload.RelPath != "Alpha.md" {
		t.Fatalf("landed save must identify the note: %+v", payload)
	}

	recorder = httptest.NewRecorder()
	srv.writeSourceSaveError(recorder, req, nil,
		noteops.Errorf(noteops.CodeSafety, "this vault refuses further mutations"))
	payload = decodeSourceError(t, recorder.Result(), http.StatusInternalServerError, noteops.CodeSafety)
	if payload.Changed || payload.Revision != "" {
		t.Fatalf("payload = %+v, want no claim that anything landed", payload)
	}
}

// TestSourceSaveErrorCarriesTheAttachmentsALandedSaveCommitted is the other half
// of the snapshot-failure envelope, and the one the browser's staging area
// depends on.
//
// The uploads are in the vault — applySourceSave calls forgetCommittedAttachments
// whenever the result is non-nil, which a snapshot failure is — and the note on
// disk links them. A body that named none of them would leave the page still
// owing those ids, and the page gives an id it still owes back on the way out:
// the files the note references would be deleted from under it. A refusal that
// wrote nothing must carry none, for the mirror reason — the ids are still the
// page's to send with the attempt that follows.
func TestSourceSaveErrorCarriesTheAttachmentsALandedSaveCommitted(t *testing.T) {
	_, srv, _ := newSourceTestServer(t, Options{})
	req := noteops.SaveSourceRequest{RelPath: "Alpha.md", Content: sourceTestNote}
	committed := []noteops.SavedAttachment{
		{UploadID: "u-1", RelPath: "attachments/one.png"},
		{UploadID: "u-2", RelPath: "attachments/two.png"},
	}

	recorder := httptest.NewRecorder()
	srv.writeSourceSaveError(recorder, req, &noteops.SaveSourceResult{
		Destination: noteops.PathResolution{RelPath: "Alpha.md"},
		Revision:    vault.HashBytes([]byte(sourceTestNote)),
		Changed:     true,
		Attachments: committed,
	}, noteops.Errorf(noteops.CodeSnapshotFailed, "the save completed and is on disk"))
	payload := decodeSourceError(t, recorder.Result(), http.StatusInternalServerError, noteops.CodeSnapshotFailed)
	if !reflect.DeepEqual(payload.Attachments, committed) {
		t.Fatalf("attachments = %+v, want the two the save committed", payload.Attachments)
	}

	// A stale-revision refusal planned nothing and wrote nothing. It is the arm
	// that fills the payload from a *second* read of the note, so it is also the
	// one that could plausibly carry a field it has no business carrying.
	recorder = httptest.NewRecorder()
	srv.writeSourceSaveError(recorder, req, nil, &noteops.SourceRevisionError{
		RelPath:  "Alpha.md",
		Expected: "r-stale",
		Actual:   vault.HashBytes([]byte(sourceTestNote)),
		Err:      noteops.Errorf(noteops.CodeConflict, "source: Alpha.md changed since it was loaded"),
	})
	payload = decodeSourceError(t, recorder.Result(), http.StatusConflict, noteops.CodeConflict)
	if len(payload.Attachments) != 0 {
		t.Fatalf("attachments = %+v, want none on a save that wrote nothing", payload.Attachments)
	}
	if payload.Content != sourceTestNote {
		t.Fatalf("the conflict carried no bytes to merge against: %+v", payload)
	}
}
