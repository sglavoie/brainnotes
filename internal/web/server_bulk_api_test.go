package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

import "github.com/sglavoie/sglavoie_brainnotes/internal/config"

func TestServer_BulkAPI_FrontmatterSetPartialMissingKeysJournalsAndInvalidatesState(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\nstatus: draft\ntitle: Source\n---\nBody marker.\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	warmResp, err := http.Get(ts.URL + "/n/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /n/source: %v", err)
	}
	warmBody := readResponseBody(t, warmResp)
	if warmResp.StatusCode != http.StatusOK || !strings.Contains(warmBody, "draft") {
		t.Fatalf("warm response unexpected: status=%d body=%q", warmResp.StatusCode, warmBody)
	}

	keys := []string{" source ", "missing", "source"}
	fields := map[string]string{
		"property": "status",
		"value":    "done",
	}
	previewResp := postBulkPreviewRequest(t, ts.URL, "frontmatter_set", keys, fields, "csrf-token", "")
	previewBody := readResponseBody(t, previewResp)
	if previewResp.StatusCode != http.StatusOK {
		t.Fatalf("preview status: got %d, want %d; body=%q", previewResp.StatusCode, http.StatusOK, previewBody)
	}
	previewPayload := decodeBulkPreviewResult(t, previewBody)
	if !previewPayload.OK || strings.TrimSpace(previewPayload.PreviewID) == "" || strings.TrimSpace(previewPayload.ExpiresAt) == "" {
		t.Fatalf("preview payload = %#v, want preview id and expiry", previewPayload)
	}

	applyFields := bulkFieldsWithPreviewID(fields, previewPayload.PreviewID)
	resp := postBulkRequest(t, ts.URL, "frontmatter_set", keys, applyFields, "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeBulkManagementResult(t, body)
	if !payload.OK || payload.Changed != 1 || payload.Failed != 1 || strings.TrimSpace(payload.JournalID) == "" {
		t.Fatalf("payload = %#v, want one change, one stale-key failure, and a journal", payload)
	}
	if len(payload.Results) != 1 || payload.Results[0].Key != "source" || !payload.Results[0].Changed {
		t.Fatalf("results = %#v, want one changed source result", payload.Results)
	}
	if len(payload.Failures) != 1 || payload.Failures[0].Key != "missing" || payload.Failures[0].Code != "not_found" {
		t.Fatalf("failures = %#v, want missing not_found", payload.Failures)
	}
	if got := readNoteFile(t, filepath.Join(root, "Source.md")); !strings.Contains(got, "status: done") {
		t.Fatalf("note was not updated:\n%s", got)
	}

	manifestPath := filepath.Join(root, config.DefaultJournalDir, payload.JournalID, "manifest.json")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("journal manifest missing: %v", err)
	}
	var manifest struct {
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("decode manifest: %v; body=%q", err, string(manifestData))
	}
	if len(manifest.Files) != 1 || manifest.Files[0].Path != "Source.md" {
		t.Fatalf("journal files = %#v, want only Source.md", manifest.Files)
	}

	refreshedResp, err := http.Get(ts.URL + "/n/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET refreshed /n/source: %v", err)
	}
	refreshedBody := readResponseBody(t, refreshedResp)
	if refreshedResp.StatusCode != http.StatusOK ||
		!strings.Contains(refreshedBody, `<span class="frontmatter-text">done</span>`) ||
		strings.Contains(refreshedBody, `<span class="frontmatter-text">draft</span>`) {
		t.Fatalf("state was not refreshed after bulk set: status=%d body=%q", refreshedResp.StatusCode, refreshedBody)
	}

	reuseResp := postBulkRequest(t, ts.URL, "frontmatter_set", keys, applyFields, "csrf-token", "")
	reuseBody := readResponseBody(t, reuseResp)
	if reuseResp.StatusCode != http.StatusConflict {
		t.Fatalf("reuse status: got %d, want %d; body=%q", reuseResp.StatusCode, http.StatusConflict, reuseBody)
	}
	reusePayload := decodeManagementError(t, reuseBody)
	if reusePayload.OK || reusePayload.Code != "preview_required" {
		t.Fatalf("reuse payload = %#v, want preview_required", reusePayload)
	}
}

func TestServer_BulkAPI_CSRFRequired(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\nstatus: draft\ntitle: Source\n---\nBody\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	resp := postBulkRequest(t, ts.URL, "frontmatter_set",
		[]string{"source"},
		map[string]string{
			"property": "status",
			"value":    "done",
		},
		"",
		"",
	)
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusForbidden, body)
	}
	payload := decodeManagementError(t, body)
	if payload.OK || payload.Code != "forbidden" {
		t.Fatalf("payload = %#v, want forbidden", payload)
	}
	if got := readNoteFile(t, filepath.Join(root, "Source.md")); !strings.Contains(got, "status: draft") {
		t.Fatalf("note changed without csrf:\n%s", got)
	}
}

func TestServer_BulkAPI_RequiresPreviewID(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\nstatus: draft\ntitle: Source\n---\nBody\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	resp := postBulkRequest(t, ts.URL, "frontmatter_set",
		[]string{"source"},
		map[string]string{
			"property": "status",
			"value":    "done",
		},
		"csrf-token",
		"",
	)
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusConflict, body)
	}
	payload := decodeManagementError(t, body)
	if payload.OK || payload.Code != "preview_required" {
		t.Fatalf("payload = %#v, want preview_required", payload)
	}
	if got := readNoteFile(t, filepath.Join(root, "Source.md")); !strings.Contains(got, "status: draft") {
		t.Fatalf("note changed without preview:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(root, config.DefaultJournalDir)); !os.IsNotExist(err) {
		t.Fatalf("missing preview created journal directory or stat failed: %v", err)
	}
}

func TestServer_BulkAPI_RejectsExpiredPreviewID(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "Source.md", "---\nstatus: draft\ntitle: Source\n---\nBody\n")

	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")
	srv, err := NewServer(cfg, v, Options{CSRFToken: "csrf-token"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	srv.previewStore.now = func() time.Time { return now }
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	keys := []string{"source"}
	fields := map[string]string{
		"property": "status",
		"value":    "done",
	}
	previewResp := postBulkPreviewRequest(t, ts.URL, "frontmatter_set", keys, fields, "csrf-token", "")
	previewBody := readResponseBody(t, previewResp)
	if previewResp.StatusCode != http.StatusOK {
		t.Fatalf("preview status: got %d, want %d; body=%q", previewResp.StatusCode, http.StatusOK, previewBody)
	}
	previewPayload := decodeBulkPreviewResult(t, previewBody)
	if strings.TrimSpace(previewPayload.PreviewID) == "" {
		t.Fatalf("preview payload = %#v, want preview id", previewPayload)
	}

	now = now.Add(bulkPreviewTTL + time.Second)
	resp := postBulkRequest(t, ts.URL, "frontmatter_set", keys,
		bulkFieldsWithPreviewID(fields, previewPayload.PreviewID),
		"csrf-token",
		"",
	)
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusConflict, body)
	}
	payload := decodeManagementError(t, body)
	if payload.OK || payload.Code != "preview_required" {
		t.Fatalf("payload = %#v, want preview_required", payload)
	}
	if got := readNoteFile(t, filepath.Join(root, "Source.md")); !strings.Contains(got, "status: draft") {
		t.Fatalf("note changed with expired preview:\n%s", got)
	}
}

func TestServer_BulkAPI_RejectsMismatchedPreviewRequests(t *testing.T) {
	tests := []struct {
		name        string
		applyKeys   []string
		applyFields map[string]string
	}{
		{
			name:      "field mismatch",
			applyKeys: []string{"source"},
			applyFields: map[string]string{
				"property": "status",
				"value":    "blocked",
			},
		},
		{
			name:      "selection mismatch",
			applyKeys: []string{"other"},
			applyFields: map[string]string{
				"property": "status",
				"value":    "done",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
				"Source.md": []byte("---\nstatus: draft\ntitle: Source\n---\nBody\n"),
				"Other.md":  []byte("---\nstatus: draft\ntitle: Other\n---\nBody\n"),
			}, nil, Options{
				CSRFToken: "csrf-token",
			})
			previewFields := map[string]string{
				"property": "status",
				"value":    "done",
			}
			previewResp := postBulkPreviewRequest(t, ts.URL, "frontmatter_set", []string{"source"}, previewFields, "csrf-token", "")
			previewBody := readResponseBody(t, previewResp)
			if previewResp.StatusCode != http.StatusOK {
				t.Fatalf("preview status: got %d, want %d; body=%q", previewResp.StatusCode, http.StatusOK, previewBody)
			}
			previewPayload := decodeBulkPreviewResult(t, previewBody)
			if strings.TrimSpace(previewPayload.PreviewID) == "" {
				t.Fatalf("preview payload = %#v, want preview id", previewPayload)
			}

			resp := postBulkRequest(t, ts.URL, "frontmatter_set", tt.applyKeys,
				bulkFieldsWithPreviewID(tt.applyFields, previewPayload.PreviewID),
				"csrf-token",
				"",
			)
			body := readResponseBody(t, resp)
			if resp.StatusCode != http.StatusConflict {
				t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusConflict, body)
			}
			payload := decodeManagementError(t, body)
			if payload.OK || payload.Code != "preview_required" {
				t.Fatalf("payload = %#v, want preview_required", payload)
			}
			if got := readNoteFile(t, filepath.Join(root, "Source.md")); !strings.Contains(got, "status: draft") {
				t.Fatalf("source changed after mismatched preview:\n%s", got)
			}
			if got := readNoteFile(t, filepath.Join(root, "Other.md")); !strings.Contains(got, "status: draft") {
				t.Fatalf("other changed after mismatched preview:\n%s", got)
			}
		})
	}
}

func TestServer_BulkAPI_RejectsStalePreviewFileStateWithoutInvalidatingState(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "Source.md", "---\nstatus: draft\ntitle: Source\n---\nBody\n")

	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")
	srv, err := NewServer(cfg, v, Options{CSRFToken: "csrf-token"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv.state.ttl = time.Hour
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	warmResp, err := http.Get(ts.URL + "/n/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /n/source: %v", err)
	}
	warmBody := readResponseBody(t, warmResp)
	if warmResp.StatusCode != http.StatusOK || !strings.Contains(warmBody, "draft") {
		t.Fatalf("warm response unexpected: status=%d body=%q", warmResp.StatusCode, warmBody)
	}

	keys := []string{"source"}
	fields := map[string]string{
		"property": "status",
		"value":    "done",
	}
	previewResp := postBulkPreviewRequest(t, ts.URL, "frontmatter_set", keys, fields, "csrf-token", "")
	previewBody := readResponseBody(t, previewResp)
	if previewResp.StatusCode != http.StatusOK {
		t.Fatalf("preview status: got %d, want %d; body=%q", previewResp.StatusCode, http.StatusOK, previewBody)
	}
	previewPayload := decodeBulkPreviewResult(t, previewBody)
	if strings.TrimSpace(previewPayload.PreviewID) == "" {
		t.Fatalf("preview payload = %#v, want preview id", previewPayload)
	}

	notePath := filepath.Join(root, "Source.md")
	if err := os.WriteFile(notePath, []byte("---\nstatus: external\ntitle: Source\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write external note change: %v", err)
	}
	resp := postBulkRequest(t, ts.URL, "frontmatter_set", keys,
		bulkFieldsWithPreviewID(fields, previewPayload.PreviewID),
		"csrf-token",
		"",
	)
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusConflict, body)
	}
	payload := decodeManagementError(t, body)
	if payload.OK || payload.Code != "preview_stale" {
		t.Fatalf("payload = %#v, want preview_stale", payload)
	}
	if got := readNoteFile(t, notePath); !strings.Contains(got, "status: external") || strings.Contains(got, "status: done") {
		t.Fatalf("stale preview wrote note:\n%s", got)
	}

	cachedResp, err := http.Get(ts.URL + "/n/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET cached /n/source: %v", err)
	}
	cachedBody := readResponseBody(t, cachedResp)
	if cachedResp.StatusCode != http.StatusOK ||
		!strings.Contains(cachedBody, `<span class="frontmatter-text">draft</span>`) ||
		strings.Contains(cachedBody, `<span class="frontmatter-text">external</span>`) {
		t.Fatalf("state was invalidated after rejected apply: status=%d body=%q", cachedResp.StatusCode, cachedBody)
	}
}
