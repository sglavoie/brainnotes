package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

// attachmentTestPayload is a small binary file: a PNG signature, a NUL, a byte
// no text encoding round-trips, and a bare CR. A route that decoded it, trimmed
// it, or re-encoded it anywhere between the upload and the vault would change
// these bytes, and every assertion below compares them exactly.
var attachmentTestPayload = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0xff, '\r', 0xfe}

type attachmentUploadPayload struct {
	OK       bool   `json:"ok"`
	UploadID string `json:"upload_id"`
	Filename string `json:"filename"`
	RelPath  string `json:"rel_path"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

// uploadAttachmentRequest posts raw bytes to the upload route the way the editor
// does: the body is the file, the metadata is in the query string, and the CSRF
// token rides in the header because there is no form to put it in.
func uploadAttachmentRequest(t *testing.T, ts *httptest.Server, owner, name string, body []byte, token string) *http.Response {
	t.Helper()

	target := ts.URL + attachmentUploadAPIRoutePath +
		"?owner=" + url.QueryEscape(owner) + "&name=" + url.QueryEscape(name)
	req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new upload request: %v", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if token != "" {
		req.Header.Set(csrfHeaderName, token)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("post upload: %v", err)
	}
	return resp
}

func uploadAttachment(t *testing.T, ts *httptest.Server, owner, name string, body []byte) attachmentUploadPayload {
	t.Helper()

	resp := uploadAttachmentRequest(t, ts, owner, name, body, sourceTestToken)
	defer resp.Body.Close()

	var payload attachmentUploadPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode upload payload: %v", err)
	}
	if resp.StatusCode != http.StatusOK || !payload.OK {
		t.Fatalf("status = %d, payload = %+v, want a 200 success", resp.StatusCode, payload)
	}
	return payload
}

// stagedUploadEntries lists what the staging directory currently holds, blobs
// and records together, so a test can say "nothing was left behind" without
// knowing how this staging area is laid out.
func stagedUploadEntries(t *testing.T, root string) []string {
	t.Helper()

	dir := filepath.Join(root, ".bnotes", "uploads")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read staging directory: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}
	return names
}

// backdateStaging ages every file in the staging directory past any TTL a sweep
// will apply, which is how a test says "this was left by an earlier session"
// without waiting a day.
func backdateStaging(t *testing.T, root string) {
	t.Helper()

	dir := filepath.Join(root, ".bnotes", "uploads")
	old := time.Now().Add(-48 * time.Hour)
	err := filepath.WalkDir(dir, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, old, old)
	})
	if err != nil {
		t.Fatalf("backdate staging: %v", err)
	}
}

// plantUncollectableUpload leaves a stale record whose blob is a non-empty
// directory, which is what an artifact the sweep cannot remove looks like —
// a leftover from a server that ran as another user, or a file another process
// is holding. The record is what the sweep selects; the directory is what
// refuses to be unlinked.
func plantUncollectableUpload(t *testing.T, root string) string {
	t.Helper()

	const id = "0123456789abcdef0123456789abcdef"
	dir := filepath.Join(root, ".bnotes", "uploads")
	if err := os.MkdirAll(filepath.Join(dir, id+".bin"), 0o755); err != nil {
		t.Fatalf("plant blob directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".bin", "occupant"), []byte("in the way"), 0o600); err != nil {
		t.Fatalf("plant blob occupant: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(`{"id":"`+id+`"}`), 0o600); err != nil {
		t.Fatalf("plant record: %v", err)
	}
	backdateStaging(t, root)
	return id
}

// A sweep cleans up after *other* requests, so its failure says nothing about
// the upload in hand. Refusing that upload would be bad enough on its own; since
// the upload route is where the sweep runs, it would also mean the first
// artifact nobody can remove refuses every upload the vault ever receives again.
func TestAnUncollectableStagingArtifactDoesNotRefuseAnUpload(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})

	// Twice, with the artifact planted again in between: one upload succeeding is
	// the fix, and the second is the claim that the route does not latch shut
	// behind the first failure it meets.
	for attempt := range 2 {
		id := plantUncollectableUpload(t, root)
		payload := uploadAttachment(t, ts, "draft-1", fmt.Sprintf("photo-%d.png", attempt), attachmentTestPayload)
		staged := filepath.Join(root, ".bnotes", "uploads", payload.UploadID+".bin")
		data, err := os.ReadFile(staged)
		if err != nil {
			t.Fatalf("attempt %d: read staged blob: %v", attempt, err)
		}
		if !bytes.Equal(data, attachmentTestPayload) {
			t.Fatalf("attempt %d: staged blob holds %q", attempt, data)
		}
		// The artifact is still there — nothing pretended to collect it — and
		// that is the point: it is a housekeeping problem, not an upload failure.
		if _, err := os.Stat(filepath.Join(root, ".bnotes", "uploads", id+".bin")); err != nil {
			t.Fatalf("attempt %d: the uncollectable artifact should be left alone: %v", attempt, err)
		}
	}
}

// Without a sweep at startup the only collector is the upload route, and the
// person whose browser died mid-upload is exactly the person who may never
// upload again. Their staging would then sit there indefinitely, invisible to
// every bn command.
func TestAServerStartCollectsStagingTheLastSessionAbandoned(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})
	uploadAttachment(t, ts, "draft-1", "photo.png", attachmentTestPayload)
	if len(stagedUploadEntries(t, root)) == 0 {
		t.Fatal("the upload should be staged before the restart")
	}
	backdateStaging(t, root)

	restartTestServer(t, root)

	if left := stagedUploadEntries(t, root); len(left) != 0 {
		t.Fatalf("staging still holds %v after a restart", left)
	}
}

// A read-only server serves a vault without changing it, and a sweep is a
// change. The abandoned staging stays until somebody serves the vault in a
// profile that may write.
func TestAReadOnlyServerStartDoesNotSweepStaging(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})
	uploadAttachment(t, ts, "draft-1", "photo.png", attachmentTestPayload)
	backdateStaging(t, root)
	before := stagedUploadEntries(t, root)

	restartTestServer(t, root, func(opts *Options) {
		opts.ReadOnly = true
		opts.StateDir = t.TempDir()
	})

	if after := stagedUploadEntries(t, root); len(after) != len(before) {
		t.Fatalf("staging went from %v to %v; a read-only server must not write", before, after)
	}
}

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
