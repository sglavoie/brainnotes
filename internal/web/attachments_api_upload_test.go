package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

func TestAttachmentUploadStagesTheExactBytesAndNamesADestination(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})

	upload := uploadAttachment(t, ts, "draft-1", "photo.png", attachmentTestPayload)

	if upload.RelPath != "attachments/photo.png" {
		t.Fatalf("rel_path = %q, want attachments/photo.png", upload.RelPath)
	}
	if upload.Size != int64(len(attachmentTestPayload)) {
		t.Fatalf("size = %d, want %d", upload.Size, len(attachmentTestPayload))
	}
	if upload.UploadID == "" || upload.SHA256 == "" {
		t.Fatalf("upload = %+v, want an id and a digest", upload)
	}
	// The destination is reserved, not written: the note that references it has
	// not been saved, so nothing may be in the vault yet.
	if _, err := os.Lstat(filepath.Join(root, "attachments", "photo.png")); !os.IsNotExist(err) {
		t.Fatalf("lstat destination: err = %v, want the file to be absent until a save commits it", err)
	}
	blob := filepath.Join(root, ".bnotes", "uploads", upload.UploadID+".bin")
	staged, err := os.ReadFile(blob)
	if err != nil {
		t.Fatalf("read staged blob: %v", err)
	}
	if !bytes.Equal(staged, attachmentTestPayload) {
		t.Fatalf("staged bytes = %v, want %v", staged, attachmentTestPayload)
	}
}

// TestTwoUploadsOfOneFilenameGetTwoDestinations pins the reason the claim is
// taken at staging time: two drafts uploading `photo.png` must not both be told
// the same destination, or the second save would be refused over a name the
// first one took.
func TestTwoUploadsOfOneFilenameGetTwoDestinations(t *testing.T) {
	ts, _, _ := newSourceTestServer(t, Options{})

	first := uploadAttachment(t, ts, "draft-1", "photo.png", attachmentTestPayload)
	second := uploadAttachment(t, ts, "draft-2", "photo.png", append(attachmentTestPayload, 'x'))

	if first.RelPath == second.RelPath {
		t.Fatalf("both uploads claimed %q, want two destinations", first.RelPath)
	}
	if first.UploadID == second.UploadID {
		t.Fatalf("both uploads got id %q, want two ids", first.UploadID)
	}
}

// TestAttachmentUploadRefusesABodyOverTheVaultLimit is the store's bound rather
// than the transport's: the configured limit is below the transport ceiling, so
// the body arrives and is refused while it is being written.
func TestAttachmentUploadRefusesABodyOverTheVaultLimit(t *testing.T) {
	const limit = 64
	ts, _, root := newSourceTestServer(t, Options{}, func(cfg *config.Config) {
		cfg.Attachments.MaxFileBytes = limit
	})

	resp := uploadAttachmentRequest(t, ts, "draft-1", "big.bin",
		bytes.Repeat([]byte{0x7f}, limit+1), sourceTestToken)
	decodeSourceError(t, resp, http.StatusBadRequest, noteops.CodeInvalidRequest)

	if left := stagedUploadEntries(t, root); len(left) != 0 {
		t.Fatalf("staging holds %v after a refused upload, want nothing", left)
	}
}

// TestTheUploadRouteIsBoundedInEveryProfile is the transport half of the same
// bound, asserted on the table rather than by sending 25 MiB: a remote profile
// is widened to exactly the ceiling — so a hosted deployment can upload a
// full-size attachment at all — and no profile is widened past it.
func TestTheUploadRouteIsBoundedInEveryProfile(t *testing.T) {
	if got := routeBodyLimit(attachmentUploadAPIRoutePath); got != config.MaxAttachmentFileBytes {
		t.Fatalf("routeBodyLimit = %d, want %d", got, config.MaxAttachmentFileBytes)
	}
	for _, mode := range []Mode{ModeLocal, ModeReadOnly, ModeHosted} {
		caps := capabilitiesFor(mode)
		limit := caps.requestBodyLimit(attachmentUploadAPIRoutePath)
		if limit < config.MaxAttachmentFileBytes {
			t.Errorf("%s: limit = %d, want at least the %d ceiling", mode, limit, config.MaxAttachmentFileBytes)
		}
		if limit > config.MaxAttachmentFileBytes && limit != caps.maxRequestBody {
			t.Errorf("%s: limit = %d, want the profile's own %d when it is the larger",
				mode, limit, caps.maxRequestBody)
		}
	}
}

// TestABodyOverTheTransportBoundIsRefusedAsTooLarge is the classification the
// bound above needs to be worth having: a vault that left its limit at the
// ceiling trips the transport reader rather than the store's own check, and that
// must still read as "your file is too large" rather than as a server fault.
func TestABodyOverTheTransportBoundIsRefusedAsTooLarge(t *testing.T) {
	err := attachmentError("attachments: receive upload", &http.MaxBytesError{Limit: config.MaxAttachmentFileBytes})
	if got := noteops.CodeOf(err); got != noteops.CodeInvalidRequest {
		t.Fatalf("code = %q, want %q", got, noteops.CodeInvalidRequest)
	}
	if got := noteops.HTTPStatusForError(err); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", got)
	}
}

func TestAttachmentUploadRefusesARequestWithoutTheCSRFToken(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})

	resp := uploadAttachmentRequest(t, ts, "draft-1", "photo.png", attachmentTestPayload, "")
	decodeSourceError(t, resp, http.StatusForbidden, noteops.CodeForbidden)

	if left := stagedUploadEntries(t, root); len(left) != 0 {
		t.Fatalf("staging holds %v after a refused upload, want nothing", left)
	}
}

func TestAttachmentUploadRefusesAGet(t *testing.T) {
	ts, _, _ := newSourceTestServer(t, Options{})

	resp, err := ts.Client().Get(ts.URL + attachmentUploadAPIRoutePath + "?owner=draft-1&name=photo.png")
	if err != nil {
		t.Fatalf("get upload route: %v", err)
	}
	decodeSourceError(t, resp, http.StatusMethodNotAllowed, noteops.CodeInvalidRequest)
}

// TestAttachmentRoutesAreRefusedInReadOnlyMode covers both routes at once: the
// replica's POST allowlist names neither, so the refusal happens in the guard
// and staging is never created.
func TestAttachmentRoutesAreRefusedInReadOnlyMode(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{ReadOnly: true, StateDir: t.TempDir()})

	resp := uploadAttachmentRequest(t, ts, "draft-1", "photo.png", attachmentTestPayload, sourceTestToken)
	decodeSourceError(t, resp, http.StatusForbidden, noteops.CodeForbidden)

	cancel := postSourceRoute(t, ts, attachmentCancelAPIRoutePath,
		url.Values{"owner": {"draft-1"}, "upload_id": {"whatever"}, "token": {sourceTestToken}}, sourceTestToken)
	decodeSourceError(t, cancel, http.StatusForbidden, noteops.CodeForbidden)

	if left := stagedUploadEntries(t, root); len(left) != 0 {
		t.Fatalf("staging holds %v in read-only mode, want nothing", left)
	}
}

// TestStagedUploadsAreNotServed is the containment claim. Staging lives under a
// dot-directory, which /media refuses at every depth, so neither the blob nor the
// record nor the destination claim is reachable — and neither is the destination
// the upload reserved, because nothing is there yet.
func TestStagedUploadsAreNotServed(t *testing.T) {
	ts, _, _ := newSourceTestServer(t, Options{})

	upload := uploadAttachment(t, ts, "draft-1", "photo.png", attachmentTestPayload)

	for _, rel := range []string{
		".bnotes/uploads/" + upload.UploadID + ".bin",
		".bnotes/uploads/" + upload.UploadID + ".json",
		".bnotes/uploads",
		"./.bnotes/uploads/" + upload.UploadID + ".bin",
		upload.RelPath,
	} {
		t.Run(rel, func(t *testing.T) {
			resp, err := ts.Client().Get(ts.URL + mediaRoutePrefix + rel)
			if err != nil {
				t.Fatalf("get media: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %d for %q, want 404", resp.StatusCode, rel)
			}
		})
	}
}

// TestStagedUploadsAreNotServedThroughASymlink closes the alias: a link inside
// the vault pointing at the staging blob resolves to a private path, and the
// answer is decided after the link is followed.
func TestStagedUploadsAreNotServedThroughASymlink(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})

	upload := uploadAttachment(t, ts, "draft-1", "photo.png", attachmentTestPayload)
	link := filepath.Join(root, "leak.png")
	if err := os.Symlink(filepath.Join(root, ".bnotes", "uploads", upload.UploadID+".bin"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	resp, err := ts.Client().Get(ts.URL + mediaRoutePrefix + "leak.png")
	if err != nil {
		t.Fatalf("get media: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a link into staging", resp.StatusCode)
	}
}

func TestAttachmentCancelDropsTheUploadAndIsIdempotent(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})

	upload := uploadAttachment(t, ts, "draft-1", "photo.png", attachmentTestPayload)
	form := url.Values{"owner": {"draft-1"}, "upload_id": {upload.UploadID}, "token": {sourceTestToken}}

	for attempt := 1; attempt <= 2; attempt++ {
		resp := postSourceRoute(t, ts, attachmentCancelAPIRoutePath, form, sourceTestToken)
		func() {
			defer resp.Body.Close()
			var payload attachmentCancelResponse
			if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
				t.Fatalf("decode cancel payload: %v", err)
			}
			if resp.StatusCode != http.StatusOK || !payload.OK {
				t.Fatalf("cancel attempt %d: status = %d, payload = %+v, want a 200 success",
					attempt, resp.StatusCode, payload)
			}
		}()
	}

	if _, err := os.Lstat(filepath.Join(root, ".bnotes", "uploads", upload.UploadID+".bin")); !os.IsNotExist(err) {
		t.Fatalf("lstat cancelled blob: err = %v, want it gone", err)
	}
}

// TestAttachmentCancelFromAnotherOwnerLeavesTheUploadAlone is the ownership
// claim on the cancel side: an id that leaked is still only usable by the draft
// it was staged for, and a cancel from anybody else must not remove the bytes —
// while still answering the way a cancel of an unknown upload does, so the two
// cannot be told apart.
func TestAttachmentCancelFromAnotherOwnerLeavesTheUploadAlone(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})

	upload := uploadAttachment(t, ts, "draft-1", "photo.png", attachmentTestPayload)
	resp := postSourceRoute(t, ts, attachmentCancelAPIRoutePath,
		url.Values{"owner": {"draft-2"}, "upload_id": {upload.UploadID}, "token": {sourceTestToken}},
		sourceTestToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want the same 200 an unknown upload gets", resp.StatusCode)
	}

	blob, err := os.ReadFile(filepath.Join(root, ".bnotes", "uploads", upload.UploadID+".bin"))
	if err != nil {
		t.Fatalf("read staged blob: %v", err)
	}
	if !bytes.Equal(blob, attachmentTestPayload) {
		t.Fatalf("staged bytes = %v, want them untouched", blob)
	}
}
