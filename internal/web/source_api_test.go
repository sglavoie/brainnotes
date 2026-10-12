package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

const sourceTestToken = "source-csrf-token"

// sourceTestNote is stored the way somebody typed it: keys out of alphabetical
// order, a link into the vault, and no trailing newline. A route that repaired
// any of that would be editing a person's file while serving it.
const sourceTestNote = "---\ntitle: Alpha\nstatus: draft\n---\n\nAlpha body, see [[Beta]]."

type sourceReadPayload struct {
	OK       bool   `json:"ok"`
	Key      string `json:"key"`
	RelPath  string `json:"rel_path"`
	Revision string `json:"revision"`
	Content  string `json:"content"`
}

type sourceSavePayload struct {
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
	RelPath   string `json:"rel_path"`
	Revision  string `json:"revision"`
	Changed   bool   `json:"changed"`
	Created   bool   `json:"created"`
	Replayed  bool   `json:"replayed"`
	JournalID string `json:"journal_id"`
}

type sourceErrorPayload struct {
	OK       bool   `json:"ok"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Key      string `json:"key"`
	RelPath  string `json:"rel_path"`
	Revision string `json:"revision"`
	Content  string `json:"content"`
	Changed  bool   `json:"changed"`
	// Attachments is on the error payload for one arm only: a snapshot failure
	// committed its uploads as surely as a success did, and the browser stops
	// owing an upload id on the strength of this field.
	Attachments []noteops.SavedAttachment `json:"attachments"`
}

// newSourceTestServer builds a vault and a server over it.
//
// customize runs on the configuration after the fixture's own values and before
// the vault is opened, which is how a test asks for a narrower attachment limit
// or a different attachments directory without a second fixture.
func newSourceTestServer(t *testing.T, opts Options, customize ...func(*config.Config)) (*httptest.Server, *Server, string) {
	t.Helper()

	root := t.TempDir()
	writeTestFile(t, root, "Alpha.md", sourceTestNote)
	writeTestFile(t, root, "Beta.md", "---\ntitle: Beta\n---\n\nBeta body.\n")

	cfg := sourceTestConfig(root)
	for _, apply := range customize {
		apply(cfg)
	}
	v, err := vault.Open(cfg)
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	if opts.CSRFToken == "" {
		opts.CSRFToken = sourceTestToken
	}
	srv, err := NewServer(cfg, v, opts)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, srv, root
}

// sourceTestConfig is the vault configuration the source and attachment routes
// are exercised against.
//
// The ignore list and the templates directory are declared because the source
// routes honour them: they are two of the locations vault.Walk never visits, and
// a fixture that left them unset would exercise a laxer rule than a real vault's.
// The attachment settings are declared for the same reason — a loaded
// configuration always carries both, so a fixture that left them zero would have
// the upload routes fail on a store no real vault could produce.
func sourceTestConfig(root string) *config.Config {
	disabled := false
	return &config.Config{
		Vault:     root,
		Ignore:    []string{"Archive"},
		Templates: config.TemplatesConfig{Dir: "Templates"},
		Serve:     config.ServeConfig{Addr: "127.0.0.1:0", OpenBrowser: &disabled},
		Attachments: config.AttachmentsConfig{
			Dir:          config.DefaultAttachmentsDir,
			MaxFileBytes: config.MaxAttachmentFileBytes,
		},
	}
}

// restartTestServer builds a second server over a vault that already exists,
// which is how a test observes what a process does on startup with an earlier
// session's leftovers still on disk.
func restartTestServer(t *testing.T, root string, customize ...func(*Options)) *Server {
	t.Helper()

	cfg := sourceTestConfig(root)
	v, err := vault.Open(cfg)
	if err != nil {
		t.Fatalf("reopen vault: %v", err)
	}
	opts := Options{CSRFToken: sourceTestToken}
	for _, apply := range customize {
		apply(&opts)
	}
	srv, err := NewServer(cfg, v, opts)
	if err != nil {
		t.Fatalf("restart NewServer: %v", err)
	}
	return srv
}

func getSourceDocument(t *testing.T, ts *httptest.Server, key string) *http.Response {
	t.Helper()
	resp, err := ts.Client().Get(ts.URL + sourceAPIRoutePath + "?key=" + url.QueryEscape(key))
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	return resp
}

func postSourceRoute(t *testing.T, ts *httptest.Server, route string, form url.Values, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+route, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if token != "" {
		req.Header.Set(csrfHeaderName, token)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", route, err)
	}
	return resp
}

func decodeSourceSave(t *testing.T, resp *http.Response) sourceSavePayload {
	t.Helper()
	defer resp.Body.Close()

	var payload sourceSavePayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode save payload: %v", err)
	}
	if resp.StatusCode != http.StatusOK || !payload.OK {
		t.Fatalf("status = %d, payload = %+v, want a 200 success", resp.StatusCode, payload)
	}
	return payload
}

func decodeSourceError(t *testing.T, resp *http.Response, wantStatus int, wantCode noteops.ErrorCode) sourceErrorPayload {
	t.Helper()
	defer resp.Body.Close()

	var payload sourceErrorPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode error payload: %v", err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d (payload %+v)", resp.StatusCode, wantStatus, payload)
	}
	if payload.OK {
		t.Fatalf("an error payload reported ok: %+v", payload)
	}
	if noteops.ErrorCode(payload.Code) != wantCode {
		t.Fatalf("code = %q, want %q (payload %+v)", payload.Code, wantCode, payload)
	}
	return payload
}

func readSourceNote(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "Alpha.md"))
	if err != nil {
		t.Fatalf("read Alpha.md: %v", err)
	}
	return string(data)
}

func TestSourceReadServesTheStoredBytesAndTheirRevision(t *testing.T) {
	ts, _, _ := newSourceTestServer(t, Options{})

	resp := getSourceDocument(t, ts, "alpha")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var payload sourceReadPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Content != sourceTestNote {
		t.Fatalf("content was not served verbatim:\n%q", payload.Content)
	}
	if want := vault.HashBytes([]byte(sourceTestNote)); payload.Revision != want {
		t.Fatalf("revision = %q, want %q", payload.Revision, want)
	}
	if payload.RelPath != "Alpha.md" || payload.Key != "alpha" {
		t.Fatalf("payload identifies the note as %+v", payload)
	}
}

func TestSourceReadRefusesAMissingKeyAndTheWrongMethod(t *testing.T) {
	ts, _, _ := newSourceTestServer(t, Options{})

	decodeSourceError(t, getSourceDocument(t, ts, ""), http.StatusBadRequest, noteops.CodeInvalidRequest)
	decodeSourceError(t, getSourceDocument(t, ts, "nothing-here"), http.StatusNotFound, noteops.CodeNotFound)

	resp := postSourceRoute(t, ts, sourceAPIRoutePath, url.Values{}, sourceTestToken)
	decodeSourceError(t, resp, http.StatusMethodNotAllowed, noteops.CodeInvalidRequest)
	if allow := resp.Header.Get("Allow"); allow != http.MethodGet {
		t.Fatalf("Allow = %q, want GET", allow)
	}
}
