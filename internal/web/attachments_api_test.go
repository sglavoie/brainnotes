package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
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
