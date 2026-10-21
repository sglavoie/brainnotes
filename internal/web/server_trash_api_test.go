package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/query"
)

func TestServer_TrashAPI_SuccessMovesNoteJournalsAndInvalidatesState(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	noteResp, err := http.Get(ts.URL + "/n/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /n/source: %v", err)
	}
	readResponseBody(t, noteResp)
	if noteResp.StatusCode != http.StatusOK {
		t.Fatalf("initial note status: got %d, want %d", noteResp.StatusCode, http.StatusOK)
	}

	resp := postTrashRequest(t, ts.URL, "source", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("content-type: got %q, want application/json...", got)
	}
	payload := decodeManagementResult(t, body)
	if !payload.OK {
		t.Fatalf("payload ok = false, body=%q", body)
	}
	if payload.Message != "Moved note to Trash." {
		t.Fatalf("message = %q, want trash success", payload.Message)
	}
	if payload.Key != "source" || payload.Path != "/n/source" || payload.RelPath != "Trash/Source.md" {
		t.Fatalf("payload path fields unexpected: %#v", payload)
	}
	if strings.TrimSpace(payload.JournalID) == "" {
		t.Fatalf("journal id missing in payload: %#v", payload)
	}
	if _, err := os.Stat(filepath.Join(root, "Source.md")); !os.IsNotExist(err) {
		t.Fatalf("source stat err = %v, want missing", err)
	}
	moved, err := os.ReadFile(filepath.Join(root, "Trash", "Source.md"))
	if err != nil {
		t.Fatalf("read trashed note: %v", err)
	}
	if string(moved) != "---\ntitle: Source\n---\nBody\n" {
		t.Fatalf("trashed note body = %q", string(moved))
	}
	if _, err := os.Stat(filepath.Join(root, config.DefaultJournalDir, payload.JournalID, "manifest.json")); err != nil {
		t.Fatalf("journal manifest missing: %v", err)
	}

	afterResp, err := http.Get(ts.URL + "/n/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET trashed note: %v", err)
	}
	readResponseBody(t, afterResp)
	if afterResp.StatusCode != http.StatusNotFound {
		t.Fatalf("post-trash note status: got %d, want %d", afterResp.StatusCode, http.StatusNotFound)
	}
}

func TestServer_TrashAPI_CSRFRequired(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	resp := postTrashRequest(t, ts.URL, "source", "", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusForbidden, body)
	}
	payload := decodeManagementError(t, body)
	if payload.OK || payload.Code != "forbidden" {
		t.Fatalf("payload = %#v, want forbidden error", payload)
	}
	if _, err := os.Stat(filepath.Join(root, "Source.md")); err != nil {
		t.Fatalf("source should remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Trash", "Source.md")); !os.IsNotExist(err) {
		t.Fatalf("trash destination stat err = %v, want missing", err)
	}
}

func TestServer_TrashAPI_InvalidAndUnknownKeys(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	resp := postTrashRequest(t, ts.URL, "../source", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid key status: got %d, want %d; body=%q", resp.StatusCode, http.StatusBadRequest, body)
	}
	payload := decodeManagementError(t, body)
	if payload.OK || payload.Code != "invalid_key" {
		t.Fatalf("invalid key payload = %#v", payload)
	}

	resp = postTrashRequest(t, ts.URL, "missing", "csrf-token", "")
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown key status: got %d, want %d; body=%q", resp.StatusCode, http.StatusNotFound, body)
	}
	payload = decodeManagementError(t, body)
	if payload.OK || payload.Code != "not_found" {
		t.Fatalf("unknown key payload = %#v", payload)
	}
}

func TestServer_TrashAPI_GraphKnownMissingFileReturnsConflict(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	warmResp, err := http.Get(ts.URL + "/n/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /n/source: %v", err)
	}
	readResponseBody(t, warmResp)
	if warmResp.StatusCode != http.StatusOK {
		t.Fatalf("warm status: got %d, want %d", warmResp.StatusCode, http.StatusOK)
	}
	if err := os.Remove(filepath.Join(root, "Source.md")); err != nil {
		t.Fatalf("remove source: %v", err)
	}

	resp := postTrashRequest(t, ts.URL, "source", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusConflict, body)
	}
	payload := decodeManagementError(t, body)
	if payload.OK || payload.Code != "conflict" {
		t.Fatalf("payload = %#v, want conflict error", payload)
	}
}

func TestServer_TrashAPI_RejectsEscapingGraphPath(t *testing.T) {
	root := t.TempDir()
	outsideRoot := t.TempDir()
	outsideNote := filepath.Join(outsideRoot, "Outside.md")
	if err := os.WriteFile(outsideNote, []byte("---\ntitle: Outside\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write outside note: %v", err)
	}

	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")
	srv, err := NewServer(cfg, v, Options{
		CSRFToken: "csrf-token",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	now := time.Unix(0, 0)
	srv.state.mu.Lock()
	srv.state.now = func() time.Time { return now }
	srv.state.snapshot = vaultSnapshot{
		graph:    &query.Graph{PathsByTitle: map[string]string{"source": outsideNote}},
		loadedAt: now,
	}
	srv.state.mu.Unlock()

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp := postTrashRequest(t, ts.URL, "source", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusForbidden, body)
	}
	payload := decodeManagementError(t, body)
	if payload.OK || payload.Code != "forbidden" {
		t.Fatalf("payload = %#v, want forbidden error", payload)
	}
	if _, err := os.Stat(outsideNote); err != nil {
		t.Fatalf("outside note should remain untouched: %v", err)
	}
}
