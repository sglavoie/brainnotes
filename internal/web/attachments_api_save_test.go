package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

// saveWithAttachments posts a whole-file save that also names uploads to commit.
func saveWithAttachments(t *testing.T, ts *httptest.Server, key, content, revision, saveID, owner string, ids ...string) *http.Response {
	t.Helper()

	form := url.Values{
		"key":      {key},
		"content":  {content},
		"revision": {revision},
		"owner":    {owner},
		"token":    {sourceTestToken},
	}
	if saveID != "" {
		form.Set("save_id", saveID)
	}
	for _, id := range ids {
		form.Add("attachment", id)
	}
	return postSourceRoute(t, ts, sourceSaveAPIRoutePath, form, sourceTestToken)
}

// attachmentSavePayload is sourceSavePayload plus the field only a save
// carrying attachments has an answer for. It is declared here rather than added
// to sourceSavePayload so the tests that predate attachments keep asserting
// against exactly the fields they are about.
type attachmentSavePayload struct {
	sourceSavePayload
	Attachments []noteops.SavedAttachment `json:"attachments"`
}

func decodeSourceSaveWithAttachments(t *testing.T, resp *http.Response) attachmentSavePayload {
	t.Helper()
	defer resp.Body.Close()

	var payload attachmentSavePayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode save payload: %v", err)
	}
	if resp.StatusCode != http.StatusOK || !payload.OK {
		t.Fatalf("status = %d, payload = %+v, want a 200 success", resp.StatusCode, payload)
	}
	return payload
}

func currentSourceRevision(t *testing.T, ts *httptest.Server, key string) sourceReadPayload {
	t.Helper()

	resp := getSourceDocument(t, ts, key)
	defer resp.Body.Close()
	var payload sourceReadPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode source payload: %v", err)
	}
	if resp.StatusCode != http.StatusOK || !payload.OK {
		t.Fatalf("status = %d, payload = %+v, want a 200 success", resp.StatusCode, payload)
	}
	return payload
}

// TestSaveCommitsTheUploadedBytesAndClearsStaging is the round trip: raw bytes
// in through the upload route, out into the vault through the save that
// references them, byte for byte, with staging emptied afterwards.
func TestSaveCommitsTheUploadedBytesAndClearsStaging(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})

	upload := uploadAttachment(t, ts, "draft-1", "photo.png", attachmentTestPayload)
	current := currentSourceRevision(t, ts, "Alpha")
	body := current.Content + "\n\n![photo](" + upload.RelPath + ")\n"

	saved := decodeSourceSave(t, saveWithAttachments(t, ts, "Alpha", body, current.Revision, "save-1", "draft-1", upload.UploadID))
	if !saved.Changed || saved.JournalID == "" {
		t.Fatalf("save = %+v, want a journaled change", saved)
	}

	landed, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(upload.RelPath)))
	if err != nil {
		t.Fatalf("read committed attachment: %v", err)
	}
	if !bytes.Equal(landed, attachmentTestPayload) {
		t.Fatalf("committed bytes = %v, want %v", landed, attachmentTestPayload)
	}
	if left := stagedUploadEntries(t, root); len(left) != 0 {
		t.Fatalf("staging holds %v after the save committed, want nothing", left)
	}
	if got := readNoteFile(t, filepath.Join(root, "Alpha.md")); got != body {
		t.Fatalf("note = %q, want %q", got, body)
	}
}

// TestAReplayedSaveReportsTheAttachmentsThatLanded covers the retry: the same
// save_id submitted twice answers from the ledger, names the same attachment,
// and does not try to write the file a second time.
func TestAReplayedSaveReportsTheAttachmentsThatLanded(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})

	upload := uploadAttachment(t, ts, "draft-1", "photo.png", attachmentTestPayload)
	current := currentSourceRevision(t, ts, "Alpha")
	body := current.Content + "\n\n![photo](" + upload.RelPath + ")\n"

	first := decodeSourceSaveWithAttachments(t,
		saveWithAttachments(t, ts, "Alpha", body, current.Revision, "save-1", "draft-1", upload.UploadID))
	second := decodeSourceSaveWithAttachments(t,
		saveWithAttachments(t, ts, "Alpha", body, current.Revision, "save-1", "draft-1", upload.UploadID))

	if !second.Replayed {
		t.Fatalf("second save = %+v, want it replayed", second)
	}
	if len(second.Attachments) != 1 || second.Attachments[0].RelPath != upload.RelPath {
		t.Fatalf("replayed attachments = %+v, want %q", second.Attachments, upload.RelPath)
	}
	if first.Revision != second.Revision {
		t.Fatalf("revisions = %q and %q, want one answer", first.Revision, second.Revision)
	}
	landed, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(upload.RelPath)))
	if err != nil {
		t.Fatalf("read committed attachment: %v", err)
	}
	if !bytes.Equal(landed, attachmentTestPayload) {
		t.Fatalf("committed bytes = %v, want %v", landed, attachmentTestPayload)
	}
}

// TestASaveNamingAnotherDraftsUploadIsRefused is the ownership claim on the
// commit side, and the sharper one: the note names a file, so a save that cannot
// have that upload must be refused rather than stored without it.
func TestASaveNamingAnotherDraftsUploadIsRefused(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})

	upload := uploadAttachment(t, ts, "draft-1", "photo.png", attachmentTestPayload)
	current := currentSourceRevision(t, ts, "Alpha")
	body := current.Content + "\n\n![photo](" + upload.RelPath + ")\n"

	resp := saveWithAttachments(t, ts, "Alpha", body, current.Revision, "save-1", "draft-2", upload.UploadID)
	decodeSourceError(t, resp, http.StatusNotFound, noteops.CodeNotFound)

	if got := readNoteFile(t, filepath.Join(root, "Alpha.md")); got != sourceTestNote {
		t.Fatalf("note = %q, want it unchanged", got)
	}
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(upload.RelPath))); !os.IsNotExist(err) {
		t.Fatalf("lstat destination: err = %v, want nothing committed", err)
	}
}

// TestASaveNamingASweptUploadIsRefused is the same refusal for the other way an
// upload stops being there: cancelled, swept, or never staged all read the same.
func TestASaveNamingASweptUploadIsRefused(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})

	upload := uploadAttachment(t, ts, "draft-1", "photo.png", attachmentTestPayload)
	cancel := postSourceRoute(t, ts, attachmentCancelAPIRoutePath,
		url.Values{"owner": {"draft-1"}, "upload_id": {upload.UploadID}, "token": {sourceTestToken}},
		sourceTestToken)
	cancel.Body.Close()

	current := currentSourceRevision(t, ts, "Alpha")
	body := current.Content + "\n\n![photo](" + upload.RelPath + ")\n"
	resp := saveWithAttachments(t, ts, "Alpha", body, current.Revision, "save-1", "draft-1", upload.UploadID)
	decodeSourceError(t, resp, http.StatusNotFound, noteops.CodeNotFound)

	if got := readNoteFile(t, filepath.Join(root, "Alpha.md")); got != sourceTestNote {
		t.Fatalf("note = %q, want it unchanged", got)
	}
}

// TestASaveOnAStaleRevisionLeavesTheUploadStaged is the interrupted case seen
// from the client's side: the save is refused, nothing lands, and the upload is
// still there to be committed by the retry the person is about to make.
func TestASaveOnAStaleRevisionLeavesTheUploadStaged(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})

	upload := uploadAttachment(t, ts, "draft-1", "photo.png", attachmentTestPayload)
	current := currentSourceRevision(t, ts, "Alpha")
	body := current.Content + "\n\n![photo](" + upload.RelPath + ")\n"

	resp := saveWithAttachments(t, ts, "Alpha", body, strings.Repeat("a", 64), "save-1", "draft-1", upload.UploadID)
	decodeSourceError(t, resp, http.StatusConflict, noteops.CodeConflict)

	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(upload.RelPath))); !os.IsNotExist(err) {
		t.Fatalf("lstat destination: err = %v, want nothing committed", err)
	}
	blob, err := os.ReadFile(filepath.Join(root, ".bnotes", "uploads", upload.UploadID+".bin"))
	if err != nil {
		t.Fatalf("read staged blob: %v", err)
	}
	if !bytes.Equal(blob, attachmentTestPayload) {
		t.Fatalf("staged bytes = %v, want them intact for the retry", blob)
	}

	// The retry against the revision the note actually holds succeeds with the
	// same upload id, which is what "still there to be committed" has to mean.
	saved := decodeSourceSave(t, saveWithAttachments(t, ts, "Alpha", body, current.Revision, "save-2", "draft-1", upload.UploadID))
	if !saved.Changed {
		t.Fatalf("retry = %+v, want it to have written", saved)
	}
	landed, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(upload.RelPath)))
	if err != nil {
		t.Fatalf("read committed attachment: %v", err)
	}
	if !bytes.Equal(landed, attachmentTestPayload) {
		t.Fatalf("committed bytes = %v, want %v", landed, attachmentTestPayload)
	}
}

// TestACommittedAttachmentIsServedAsItsOwnBytes closes the loop the note's link
// makes a promise about: once the save commits, /media serves the attachment,
// and it serves exactly what was uploaded.
func TestACommittedAttachmentIsServedAsItsOwnBytes(t *testing.T) {
	ts, _, _ := newSourceTestServer(t, Options{})

	upload := uploadAttachment(t, ts, "draft-1", "photo.png", attachmentTestPayload)
	current := currentSourceRevision(t, ts, "Alpha")
	body := current.Content + "\n\n![photo](" + upload.RelPath + ")\n"
	decodeSourceSave(t, saveWithAttachments(t, ts, "Alpha", body, current.Revision, "save-1", "draft-1", upload.UploadID))

	resp, err := ts.Client().Get(ts.URL + mediaRoutePrefix + upload.RelPath)
	if err != nil {
		t.Fatalf("get media: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	served, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read media body: %v", err)
	}
	if !bytes.Equal(served, attachmentTestPayload) {
		t.Fatalf("served bytes = %v, want %v", served, attachmentTestPayload)
	}
}

// TestAnActiveAttachmentIsNeverExecutedInThisOrigin pins the other half of
// serving: an upload route that accepts arbitrary bytes must not become a way to
// put script on this origin, so an HTML attachment comes back sandboxed and as a
// download.
func TestAnActiveAttachmentIsNeverExecutedInThisOrigin(t *testing.T) {
	ts, _, _ := newSourceTestServer(t, Options{})

	payload := []byte("<html><script>fetch('/api/source?key=Alpha')</script></html>")
	upload := uploadAttachment(t, ts, "draft-1", "page.html", payload)
	current := currentSourceRevision(t, ts, "Alpha")
	body := current.Content + "\n\n[page](" + upload.RelPath + ")\n"
	decodeSourceSave(t, saveWithAttachments(t, ts, "Alpha", body, current.Revision, "save-1", "draft-1", upload.UploadID))

	resp, err := ts.Client().Get(ts.URL + mediaRoutePrefix + upload.RelPath)
	if err != nil {
		t.Fatalf("get media: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Security-Policy"); got != "sandbox" {
		t.Fatalf("Content-Security-Policy = %q, want sandbox", got)
	}
	if got := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") {
		t.Fatalf("Content-Disposition = %q, want an attachment disposition", got)
	}
}
