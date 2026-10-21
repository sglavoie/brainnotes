package mutationmatrix

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/brainnotes/internal/journal"
	"github.com/sglavoie/brainnotes/internal/noteops"
)

// uploadState is one staged upload as a save has to name it.
type uploadState struct {
	owner   string
	id      string
	relPath string
}

// sourceSaveWithUpload is what an attachment-carrying save is planned against:
// the note's revision and the upload it references.
type sourceSaveWithUpload struct {
	source sourceState
	upload uploadState
}

// uploadAttachment stages one file over the real route.
//
// The body is the file, raw, and the metadata rides in the query string — the
// shape a streamed upload has to take, and the reason the CSRF token is sent as
// a header: there is no form to put it in.
func uploadAttachment(t *testing.T, h *harness, owner, name string, payload []byte) uploadState {
	t.Helper()
	ts, err := sourceServer(t, h)
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	defer ts.Close()

	target := ts.URL + "/api/attachments/upload?owner=" + url.QueryEscape(owner) + "&name=" + url.QueryEscape(name)
	req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build upload request: %v", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-BN-CSRF", matrixCSRFToken)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("post upload: %v", err)
	}
	defer resp.Body.Close()

	var payloadResp struct {
		OK       bool   `json:"ok"`
		UploadID string `json:"upload_id"`
		RelPath  string `json:"rel_path"`
		SHA256   string `json:"sha256"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payloadResp); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if resp.StatusCode != http.StatusOK || !payloadResp.OK {
		t.Fatalf("POST /api/attachments/upload answered %d (%+v)", resp.StatusCode, payloadResp)
	}
	if payloadResp.RelPath != sourceUploadRel {
		t.Fatalf("staging reserved %q, want %q", payloadResp.RelPath, sourceUploadRel)
	}
	return uploadState{owner: owner, id: payloadResp.UploadID, relPath: payloadResp.RelPath}
}

// TestAttachmentUploadStaysOutOfTheHistory is the staging half of the
// attachment write class, and the reason an upload has no row of its own: it
// commits nothing, journals nothing and changes no note, which is a claim the
// table's "exactly one commit" assertion cannot express.
//
// An upload is a write to a private directory and nothing more. The save that
// references it is what puts the bytes in the vault, so an upload that committed
// — or that left an undoable entry behind — would mean uploading a file was a
// change to somebody's notes before they ever saved.
func TestAttachmentUploadStaysOutOfTheHistory(t *testing.T) {
	h := newHarness(t, nil)
	head := h.head()
	entries := undoEntryCount(t, h)

	upload := uploadAttachment(t, h, sourceUploadOwner, sourceUploadName, sourceUploadPayload)

	if got := h.head(); got != head {
		t.Fatalf("an upload committed %q", h.subject("HEAD"))
	}
	if got := undoEntryCount(t, h); got != entries {
		t.Fatalf("an upload recorded %d undo entries", got-entries)
	}
	// The bytes are on this disk, and they are nowhere a note could reach.
	if _, err := os.Lstat(filepath.Join(h.root, filepath.FromSlash(sourceUploadRel))); !os.IsNotExist(err) {
		t.Fatalf("an upload put bytes at its destination before any save: %v", err)
	}
	if staged := stagedUploadFiles(t, h); len(staged) == 0 {
		t.Fatal("the upload left nothing in staging")
	}
	if upload.id == "" {
		t.Fatal("the upload answered with no id")
	}
}

// TestAttachmentSaveClearsStagingAndLandsTheExactBytes follows one file the
// whole way: private staging, into the vault with the note that names it, and
// out of staging again.
func TestAttachmentSaveClearsStagingAndLandsTheExactBytes(t *testing.T) {
	h := newHarness(t, nil)
	base := getSource(t, h, sourceSaveKey)
	upload := uploadAttachment(t, h, sourceUploadOwner, sourceUploadName, sourceUploadPayload)

	if err := postSourceSave(t, h, sourceSaveKey, sourceSaveContentWithAttachment, base.revision, upload); err != nil {
		t.Fatalf("save with an attachment: %v", err)
	}
	stored, err := os.ReadFile(filepath.Join(h.root, filepath.FromSlash(sourceUploadRel)))
	if err != nil {
		t.Fatalf("read the committed attachment: %v", err)
	}
	if !bytes.Equal(stored, sourceUploadPayload) {
		t.Fatalf("the committed attachment holds %d bytes, want the %d uploaded", len(stored), len(sourceUploadPayload))
	}
	// Staging is not a second copy of the vault. Once the bytes are committed the
	// staged one is redundant, and keeping it would double what every upload costs.
	if staged := stagedUploadFiles(t, h); len(staged) != 0 {
		t.Fatalf("staging still holds %v after the save committed the upload", staged)
	}
}

// undoEntryCount is how many operations this vault could undo right now.
func undoEntryCount(t *testing.T, h *harness) int {
	t.Helper()
	entries, _, err := journal.ListEntries(noteops.JournalRoot(h.root))
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	return len(entries)
}

// stagedUploadFiles lists what is in private staging, by name only.
func stagedUploadFiles(t *testing.T, h *harness) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(h.root, filepath.FromSlash(sourceUploadDir)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read %s: %v", sourceUploadDir, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.Type()&fs.ModeDir == 0 {
			names = append(names, entry.Name())
		}
	}
	return names
}
