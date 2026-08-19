package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

import "github.com/sglavoie/sglavoie_brainnotes/internal/config"

func TestServer_BulkPreviewAPI_CSRFRequired(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\nstatus: draft\ntitle: Source\n---\nBody\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	resp := postBulkPreviewRequest(t, ts.URL, "frontmatter_set",
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

func TestServer_BulkPreviewAPI_RejectsInvalidActionAndFields(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\nstatus: draft\ntitle: Source\n---\nBody\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	tests := []struct {
		name    string
		action  string
		fields  map[string]string
		message string
	}{
		{
			name:    "unsupported action",
			action:  "archive",
			fields:  nil,
			message: "unsupported action",
		},
		{
			name:    "move folder",
			action:  "move",
			fields:  nil,
			message: "move: missing new folder",
		},
		{
			name:   "set value",
			action: "frontmatter_set",
			fields: map[string]string{
				"property": "status",
			},
			message: "frontmatter: missing value",
		},
		{
			name:    "unset property",
			action:  "frontmatter_unset",
			fields:  nil,
			message: "frontmatter: missing property",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := postBulkPreviewRequest(t, ts.URL, tt.action, []string{"source"}, tt.fields, "csrf-token", "")
			body := readResponseBody(t, resp)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusBadRequest, body)
			}
			payload := decodeManagementError(t, body)
			if payload.OK || payload.Code != "invalid_request" || !strings.Contains(payload.Message, tt.message) {
				t.Fatalf("payload = %#v, want invalid_request containing %q", payload, tt.message)
			}
		})
	}
}

func TestServer_BulkPreviewAPI_MissingKeysAndStaleSelectedKeys(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\nstatus: draft\ntitle: Source\n---\nBody\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	missingKeysResp := postBulkPreviewRequest(t, ts.URL, "trash", nil, nil, "csrf-token", "")
	missingKeysBody := readResponseBody(t, missingKeysResp)
	if missingKeysResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing keys status: got %d, want %d; body=%q", missingKeysResp.StatusCode, http.StatusBadRequest, missingKeysBody)
	}
	missingKeysPayload := decodeManagementError(t, missingKeysBody)
	if missingKeysPayload.OK || missingKeysPayload.Code != "invalid_request" ||
		!strings.Contains(missingKeysPayload.Message, "no notes selected") {
		t.Fatalf("missing keys payload = %#v, want no notes selected", missingKeysPayload)
	}

	staleResp := postBulkPreviewRequest(t, ts.URL, "trash", []string{"missing"}, nil, "csrf-token", "")
	staleBody := readResponseBody(t, staleResp)
	if staleResp.StatusCode != http.StatusNotFound {
		t.Fatalf("stale key status: got %d, want %d; body=%q", staleResp.StatusCode, http.StatusNotFound, staleBody)
	}
	if !strings.Contains(staleBody, `"preview_id"`) || !strings.Contains(staleBody, `"expires_at"`) {
		t.Fatalf("stale key body missing preview fields: %q", staleBody)
	}
	stalePayload := decodeBulkPreviewResult(t, staleBody)
	if stalePayload.OK || stalePayload.Action != "trash" || stalePayload.Failed != 1 || len(stalePayload.Failures) != 1 {
		t.Fatalf("stale payload = %#v, want one failed trash preview", stalePayload)
	}
	if stalePayload.Failures[0].Key != "missing" || stalePayload.Failures[0].Code != "not_found" {
		t.Fatalf("stale failures = %#v, want missing not_found", stalePayload.Failures)
	}
}

func TestServer_BulkPreviewAPI_FrontmatterSetNoWritesJournalOrInvalidation(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "Source.md", "---\nstatus: draft\ntitle: Source\n---\nBody marker.\n")

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

	resp := postBulkPreviewRequest(t, ts.URL, "frontmatter_set",
		[]string{" source ", "missing", "source"},
		map[string]string{
			"property": "status",
			"value":    "done",
		},
		"csrf-token",
		"",
	)
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, `"preview_id"`) || !strings.Contains(body, `"expires_at"`) {
		t.Fatalf("preview body missing preview fields: %q", body)
	}
	payload := decodeBulkPreviewResult(t, body)
	if !payload.OK || payload.Action != "frontmatter_set" || payload.Changed != 1 || payload.Failed != 1 {
		t.Fatalf("payload = %#v, want one changed preview and one stale-key failure", payload)
	}
	if len(payload.Results) != 1 {
		t.Fatalf("results = %#v, want one source result", payload.Results)
	}
	result := payload.Results[0]
	if result.Key != "source" || result.Path != "/n/source" || result.RelPath != "Source.md" ||
		result.Source.Key != "source" || result.Source.RelPath != "Source.md" || result.Destination != nil || !result.Changed {
		t.Fatalf("result = %#v, want source frontmatter preview row", result)
	}
	if len(result.Affected) != 1 || result.Affected[0].Role != "source" || !result.Affected[0].Exists {
		t.Fatalf("affected = %#v, want existing source path", result.Affected)
	}
	if len(result.Frontmatter) != 1 {
		t.Fatalf("frontmatter = %#v, want one status change", result.Frontmatter)
	}
	change := result.Frontmatter[0]
	if change.Key != "status" || !change.Changed ||
		!change.Before.Present || change.Before.Value != "draft" ||
		!change.After.Present || change.After.Value != "done" {
		t.Fatalf("frontmatter change = %#v, want draft -> done", change)
	}
	if len(payload.Failures) != 1 || payload.Failures[0].Key != "missing" || payload.Failures[0].Code != "not_found" {
		t.Fatalf("failures = %#v, want missing not_found", payload.Failures)
	}

	notePath := filepath.Join(root, "Source.md")
	if got := readNoteFile(t, notePath); !strings.Contains(got, "status: draft") || strings.Contains(got, "status: done") {
		t.Fatalf("preview wrote note:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(root, config.DefaultJournalDir)); !os.IsNotExist(err) {
		t.Fatalf("preview created journal directory or stat failed: %v", err)
	}

	if err := os.WriteFile(notePath, []byte("---\nstatus: external\ntitle: Source\n---\nBody marker.\n"), 0o644); err != nil {
		t.Fatalf("write external note change: %v", err)
	}
	cachedResp, err := http.Get(ts.URL + "/n/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET cached /n/source: %v", err)
	}
	cachedBody := readResponseBody(t, cachedResp)
	if cachedResp.StatusCode != http.StatusOK ||
		!strings.Contains(cachedBody, `<span class="frontmatter-text">draft</span>`) ||
		strings.Contains(cachedBody, `<span class="frontmatter-text">external</span>`) {
		t.Fatalf("state was invalidated after preview: status=%d body=%q", cachedResp.StatusCode, cachedBody)
	}
}

func TestServer_BulkPreviewAPI_MoveReportsRewriteCountsAndPartialFailures(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Inbox/Source.md": []byte("---\ntitle: Source\n---\nBody.\n"),
		"Ref.md":          []byte("---\ntitle: Ref\n---\nSee [[Inbox/Source]].\n"),
		"Meta.md":         []byte("---\nparent: \"[[Inbox/Source]]\"\n---\nBody.\n"),
	}, func(cfg *config.Config) {
		cfg.Frontmatter.Properties = map[string]config.PropertyDefinition{
			"parent": {Type: config.KindWikilink},
		}
	}, Options{
		CSRFToken: "csrf-token",
	})

	resp := postBulkPreviewRequest(t, ts.URL, "move",
		[]string{" inbox/source ", "missing", "inbox/source"},
		map[string]string{"folder": "Archive"},
		"csrf-token",
		"",
	)
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeBulkPreviewResult(t, body)
	if !payload.OK || payload.Action != "move" || payload.Changed != 1 || payload.Unchanged != 0 || payload.Failed != 1 {
		t.Fatalf("payload = %#v, want one changed move preview and one missing failure", payload)
	}
	if strings.TrimSpace(payload.PreviewID) == "" || strings.TrimSpace(payload.ExpiresAt) == "" {
		t.Fatalf("preview receipt missing: %#v", payload)
	}
	if len(payload.Results) != 1 {
		t.Fatalf("results = %#v, want one move result", payload.Results)
	}
	result := payload.Results[0]
	if result.Key != "inbox/source" || result.Path != "/n/archive/source" || result.RelPath != "Archive/Source.md" || !result.Changed {
		t.Fatalf("result = %#v, want archive destination payload", result)
	}
	if result.Source.Key != "inbox/source" || result.Source.Path != "/n/inbox/source" || result.Source.RelPath != "Inbox/Source.md" {
		t.Fatalf("source = %#v, want original source path", result.Source)
	}
	if result.Destination == nil ||
		result.Destination.Key != "archive/source" ||
		result.Destination.Path != "/n/archive/source" ||
		result.Destination.RelPath != "Archive/Source.md" {
		t.Fatalf("destination = %#v, want archive destination", result.Destination)
	}
	if result.Move == nil ||
		result.Move.RewriteCount != 2 ||
		result.Move.RewriteFileCount != 2 ||
		result.Move.BodyRewriteCount != 1 ||
		result.Move.FrontmatterRewriteCount != 1 {
		t.Fatalf("move preview = %#v, want one body and one frontmatter rewrite", result.Move)
	}
	if len(result.Affected) != 4 {
		t.Fatalf("affected = %#v, want source, destination, and two rewrite paths", result.Affected)
	}
	if len(payload.Failures) != 1 || payload.Failures[0].Key != "missing" || payload.Failures[0].Code != "not_found" {
		t.Fatalf("failures = %#v, want missing not_found", payload.Failures)
	}
	if got := readNoteFile(t, filepath.Join(root, "Inbox", "Source.md")); !strings.Contains(got, "Body.") {
		t.Fatalf("source changed after preview:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(root, "Archive")); !os.IsNotExist(err) {
		t.Fatalf("preview created archive path or stat failed: %v", err)
	}
	if got := readNoteFile(t, filepath.Join(root, "Ref.md")); !strings.Contains(got, "[[Inbox/Source]]") {
		t.Fatalf("body rewrite applied during preview:\n%s", got)
	}
	if got := readNoteFile(t, filepath.Join(root, "Meta.md")); !strings.Contains(got, "[[Inbox/Source]]") {
		t.Fatalf("frontmatter rewrite applied during preview:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(root, config.DefaultJournalDir)); !os.IsNotExist(err) {
		t.Fatalf("preview created journal directory or stat failed: %v", err)
	}
}

func TestServer_BulkPreviewAPI_FrontmatterUnsetDistinguishesPresentEmptyStringAndAbsent(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Empty.md":  []byte("---\nstatus: \"\"\ntitle: Empty\n---\nBody.\n"),
		"Absent.md": []byte("---\ntitle: Absent\n---\nBody.\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	resp := postBulkPreviewRequest(t, ts.URL, "frontmatter_unset",
		[]string{"empty", "absent"},
		map[string]string{"property": "status"},
		"csrf-token",
		"",
	)
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeBulkPreviewResult(t, body)
	if !payload.OK || payload.Action != "frontmatter_unset" || payload.Changed != 1 || payload.Unchanged != 1 || payload.Failed != 0 {
		t.Fatalf("payload = %#v, want one changed and one unchanged unset preview", payload)
	}
	if len(payload.Results) != 2 {
		t.Fatalf("results = %#v, want two preview rows", payload.Results)
	}

	var emptyResult, absentResult *bulkPreviewResultPayload
	for i := range payload.Results {
		switch payload.Results[i].Key {
		case "empty":
			emptyResult = &payload.Results[i]
		case "absent":
			absentResult = &payload.Results[i]
		}
	}
	if emptyResult == nil || absentResult == nil {
		t.Fatalf("results missing expected keys: %#v", payload.Results)
	}
	if !emptyResult.Changed || len(emptyResult.Frontmatter) != 1 {
		t.Fatalf("empty result = %#v, want one changed frontmatter row", emptyResult)
	}
	emptyChange := emptyResult.Frontmatter[0]
	if emptyChange.Key != "status" ||
		!emptyChange.Before.Present || emptyChange.Before.Value != "" ||
		emptyChange.After.Present || !emptyChange.Changed {
		t.Fatalf("empty change = %#v, want present empty string removed", emptyChange)
	}
	if absentResult.Changed || len(absentResult.Frontmatter) != 1 {
		t.Fatalf("absent result = %#v, want one unchanged frontmatter row", absentResult)
	}
	absentChange := absentResult.Frontmatter[0]
	if absentChange.Key != "status" ||
		absentChange.Before.Present || absentChange.After.Present || absentChange.Changed {
		t.Fatalf("absent change = %#v, want absent property unchanged", absentChange)
	}
	if got := readNoteFile(t, filepath.Join(root, "Empty.md")); !strings.Contains(got, `status: ""`) {
		t.Fatalf("preview wrote Empty.md:\n%s", got)
	}
	if got := readNoteFile(t, filepath.Join(root, "Absent.md")); strings.Contains(got, "status:") {
		t.Fatalf("preview wrote Absent.md:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(root, config.DefaultJournalDir)); !os.IsNotExist(err) {
		t.Fatalf("preview created journal directory or stat failed: %v", err)
	}
}
