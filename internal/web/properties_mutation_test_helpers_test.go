package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

const propertyTestToken = "csrf-token"

const propertyTestConfigTOML = `[frontmatter.properties.status]
type = "string"
allowed = ["draft", "done"]

[frontmatter.properties.tags]
type = "list"
items = "string"

[frontmatter.properties.reviewed]
type = "date"
`

// propertyTestNotes is the shared two-note fixture: both notes carry every
// declared property so a mutation can be observed on each one independently.
func propertyTestNotes() map[string]string {
	return map[string]string{
		"Alpha.md": "---\nreviewed: 2026-01-02\nstatus: draft\ntags:\n    - alpha\n    - shared\ntitle: Alpha\n---\nAlpha body.\n",
		"Beta.md":  "---\nreviewed: 2026-01-03\nstatus: draft\ntags:\n    - beta\ntitle: Beta\n---\nBeta body.\n",
	}
}

// newPropertyServer builds a server whose configuration really lives on disk,
// which every property mutation needs: schema writes patch that file and note
// writes are pinned to its generation.
func newPropertyServer(t *testing.T, source string, files map[string]string) (*httptest.Server, *Server, string) {
	t.Helper()
	return newPropertyHookServer(t, source, files, nil)
}

// newPropertyHookServer is newPropertyServer with a hook that runs immediately
// before a property transaction commits. The hook is installed before the
// listener starts so a test can land an external edit inside the commit window
// without racing the server's own goroutine.
func newPropertyHookServer(t *testing.T, source string, files map[string]string, hook func(root string)) (*httptest.Server, *Server, string) {
	t.Helper()

	root := t.TempDir()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		writeTestFile(t, root, name, files[name])
	}
	if err := os.WriteFile(filepath.Join(root, config.FileName), []byte(source), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Decode(root, config.VaultOriginFlag, []byte(source))
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	disabled := false
	cfg.Serve.Addr = "127.0.0.1:0"
	cfg.Serve.OpenBrowser = &disabled
	v, err := vault.Open(cfg)
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	srv, err := NewServer(cfg, v, Options{CSRFToken: propertyTestToken})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if hook != nil {
		srv.beforePropertyCommit = func() { hook(root) }
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, srv, root
}

func newPropertyFixtureServer(t *testing.T) (*httptest.Server, *Server, string) {
	t.Helper()
	return newPropertyServer(t, propertyTestConfigTOML, propertyTestNotes())
}

// postProperty sends a JSON body with the CSRF header set. An empty token omits
// the header entirely, which is how the authorization tests reach the refusal.
func postProperty(t *testing.T, baseURL, route, token string, body any) (int, string) {
	t.Helper()

	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+route, bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set(csrfHeaderName, token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", route, err)
	}
	return resp.StatusCode, readResponseBody(t, resp)
}

func postPropertyMutation(t *testing.T, baseURL, route, token string, body any) (int, propertyMutationResponse) {
	t.Helper()

	status, raw := postProperty(t, baseURL, route, token, body)
	var payload propertyMutationResponse
	if status == http.StatusOK {
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("decode %s body %q: %v", route, raw, err)
		}
	}
	return status, payload
}

// previewProperty runs a preview that is expected to succeed and issue a
// receipt, so the apply-side tests read as one line of setup.
func previewProperty(t *testing.T, baseURL string, req propertyMutationRequest) propertyMutationResponse {
	t.Helper()

	status, payload := postPropertyMutation(t, baseURL, propertyPreviewAPIRoutePath, propertyTestToken, req)
	if status != http.StatusOK {
		t.Fatalf("preview status = %d", status)
	}
	if payload.PreviewID == "" {
		t.Fatalf("preview issued no receipt: %#v", payload)
	}
	return payload
}

func applyProperty(t *testing.T, baseURL, previewID string) (int, propertyMutationResponse) {
	t.Helper()
	return postPropertyMutation(t, baseURL, propertyApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: previewID})
}

func previewPropertySchema(t *testing.T, baseURL string, req propertySchemaRequest) propertyMutationResponse {
	t.Helper()

	status, payload := postPropertyMutation(t, baseURL, propertySchemaPreviewAPIRoutePath, propertyTestToken, req)
	if status != http.StatusOK {
		t.Fatalf("schema preview status = %d", status)
	}
	if payload.PreviewID == "" {
		t.Fatalf("schema preview issued no receipt: %#v", payload)
	}
	return payload
}

func applyPropertySchema(t *testing.T, baseURL, previewID string) (int, propertyMutationResponse) {
	t.Helper()
	return postPropertyMutation(t, baseURL, propertySchemaApplyAPIRoutePath, propertyTestToken,
		propertyApplyRequest{PreviewID: previewID})
}

func noteHash(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %q: %v", rel, err)
	}
	return vault.HashBytes(data)
}

func readVaultFile(t *testing.T, root, rel string) string {
	t.Helper()
	return readNoteFile(t, filepath.Join(root, rel))
}

func requireErrorCode(t *testing.T, raw, code string) {
	t.Helper()
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode error body %q: %v", raw, err)
	}
	if payload.Code != code {
		t.Fatalf("error code = %q, want %q (body %q)", payload.Code, code, raw)
	}
}

func propertyHistory(t *testing.T, baseURL string) historyResponse {
	t.Helper()

	resp, err := http.Get(baseURL + "/api/history") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /api/history: %v", err)
	}
	raw := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("history status = %d, body %q", resp.StatusCode, raw)
	}
	var payload historyResponse
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode history %q: %v", raw, err)
	}
	return payload
}

// postUndo drives the existing form-encoded undo endpoint, which property
// writes must be restorable through unchanged.
func postUndo(t *testing.T, baseURL, entryID string) (int, string) {
	t.Helper()

	form := url.Values{}
	form.Set("token", propertyTestToken)
	if entryID != "" {
		form.Set("id", entryID)
	}
	resp, err := http.Post(baseURL+"/api/undo", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("POST /api/undo: %v", err)
	}
	return resp.StatusCode, readResponseBody(t, resp)
}
