package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/vault"
)

const captureTestToken = "capture-csrf-token"

var captureBulletPattern = regexp.MustCompile(`^- \d{4}-\d{2}-\d{2} \d{2}:\d{2} `)

type captureSuccessPayload struct {
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
	Detail    string `json:"detail"`
	Key       string `json:"key"`
	Path      string `json:"path"`
	RelPath   string `json:"rel_path"`
	JournalID string `json:"journal_id"`
}

type captureErrorPayload struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// newCaptureTestServer builds a running server over files with quick capture
// configured. The snapshot clock is frozen so the one-second cache TTL can never
// expire on its own: a snapshot reload then proves invalidate() ran.
func newCaptureTestServer(t *testing.T, files map[string]string, configure func(cfg *config.Config)) (*httptest.Server, *Server, string) {
	t.Helper()

	root := t.TempDir()
	for rel, body := range files {
		writeTestFile(t, root, rel, body)
	}

	disabled := false
	cfg := &config.Config{
		Vault:   root,
		Serve:   config.ServeConfig{Addr: "127.0.0.1:0", OpenBrowser: &disabled},
		Capture: config.CaptureConfig{InboxNote: "Inbox.md", Heading: "## Captures"},
	}
	if configure != nil {
		configure(cfg)
	}
	v, err := vault.Open(cfg)
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	srv, err := NewServer(cfg, v, Options{CSRFToken: captureTestToken})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	frozen := time.Now()
	srv.state.now = func() time.Time { return frozen }

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, srv, root
}

func postCapture(t *testing.T, ts *httptest.Server, form url.Values, headerToken string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/capture", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("post capture: %v", err)
	}
	return resp
}

func decodeCaptureSuccess(t *testing.T, resp *http.Response) captureSuccessPayload {
	t.Helper()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var payload captureSuccessPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode success payload: %v", err)
	}
	return payload
}

func decodeCaptureError(t *testing.T, resp *http.Response, wantStatus int) captureErrorPayload {
	t.Helper()
	defer resp.Body.Close()

	if resp.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d", resp.StatusCode, wantStatus)
	}
	var payload captureErrorPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode error payload: %v", err)
	}
	if payload.OK {
		t.Fatalf("error payload should not report ok: %+v", payload)
	}
	return payload
}

func TestCaptureAPIRejectsMissingCSRFToken(t *testing.T) {
	files := map[string]string{"Inbox.md": "---\ntitle: Inbox\n---\n## Captures\n"}
	ts, _, root := newCaptureTestServer(t, files, nil)

	form := url.Values{}
	form.Set("text", "no token")
	payload := decodeCaptureError(t, postCapture(t, ts, form, ""), http.StatusForbidden)
	if payload.Code != "forbidden" {
		t.Fatalf("code = %q, want forbidden", payload.Code)
	}

	if got := readNoteFile(t, filepath.Join(root, "Inbox.md")); strings.Contains(got, "no token") {
		t.Fatalf("rejected capture still wrote the note:\n%s", got)
	}
}

func TestCaptureAPIRejectsWrongCSRFToken(t *testing.T) {
	files := map[string]string{"Inbox.md": "---\ntitle: Inbox\n---\n## Captures\n"}
	ts, _, _ := newCaptureTestServer(t, files, nil)

	form := url.Values{}
	form.Set("text", "wrong token")
	payload := decodeCaptureError(t, postCapture(t, ts, form, "not-the-token"), http.StatusForbidden)
	if payload.Code != "forbidden" {
		t.Fatalf("code = %q, want forbidden", payload.Code)
	}
}

func TestCaptureAPIAcceptsFormTokenFallback(t *testing.T) {
	files := map[string]string{"Inbox.md": "---\ntitle: Inbox\n---\n## Captures\n"}
	ts, _, _ := newCaptureTestServer(t, files, nil)

	form := url.Values{}
	form.Set("text", "form token")
	form.Set("token", captureTestToken)
	payload := decodeCaptureSuccess(t, postCapture(t, ts, form, ""))
	if !strings.HasSuffix(payload.Detail, "form token") {
		t.Fatalf("detail = %q, want the captured text", payload.Detail)
	}
}

func TestCaptureAPIRejectsGET(t *testing.T) {
	files := map[string]string{"Inbox.md": "---\ntitle: Inbox\n---\n## Captures\n"}
	ts, _, _ := newCaptureTestServer(t, files, nil)

	resp, err := ts.Client().Get(ts.URL + "/api/capture")
	if err != nil {
		t.Fatalf("get capture: %v", err)
	}
	if got := resp.Header.Get("Allow"); got != http.MethodPost {
		t.Fatalf("Allow = %q, want POST", got)
	}
	payload := decodeCaptureError(t, resp, http.StatusMethodNotAllowed)
	if payload.Message != "method not allowed" {
		t.Fatalf("message = %q, want %q", payload.Message, "method not allowed")
	}
}

func TestCaptureAPIRejectsEmptyText(t *testing.T) {
	files := map[string]string{"Inbox.md": "---\ntitle: Inbox\n---\n## Captures\n"}
	original := files["Inbox.md"]
	ts, _, root := newCaptureTestServer(t, files, nil)

	for _, text := range []string{"", "   \t  "} {
		form := url.Values{}
		form.Set("text", text)
		payload := decodeCaptureError(t, postCapture(t, ts, form, captureTestToken), http.StatusBadRequest)
		if payload.Code != "invalid_request" {
			t.Fatalf("code = %q, want invalid_request", payload.Code)
		}
	}

	if got := readNoteFile(t, filepath.Join(root, "Inbox.md")); got != original {
		t.Fatalf("note changed:\ngot  %q\nwant %q", got, original)
	}
}

func TestCaptureAPIRejectsUnconfiguredCapture(t *testing.T) {
	ts, _, _ := newCaptureTestServer(t, nil, func(cfg *config.Config) {
		cfg.Capture = config.CaptureConfig{}
	})

	form := url.Values{}
	form.Set("text", "nowhere to go")
	payload := decodeCaptureError(t, postCapture(t, ts, form, captureTestToken), http.StatusBadRequest)
	if payload.Code != "invalid_request" {
		t.Fatalf("code = %q, want invalid_request", payload.Code)
	}
}

func TestCaptureAPIAppendsUnderHeading(t *testing.T) {
	files := map[string]string{
		"Inbox.md": "---\ntitle: Inbox\n---\nIntro.\n\n## Captures\n- 2026-01-01 08:00 older\n\n## Later\nTail.\n",
	}
	ts, _, root := newCaptureTestServer(t, files, nil)

	form := url.Values{}
	form.Set("text", "call [[Bob]] back")
	payload := decodeCaptureSuccess(t, postCapture(t, ts, form, captureTestToken))

	if !payload.OK {
		t.Fatalf("payload.OK = false, want true: %+v", payload)
	}
	if !captureBulletPattern.MatchString(payload.Detail) || !strings.HasSuffix(payload.Detail, "call [[Bob]] back") {
		t.Fatalf("detail = %q, want a timestamped bullet ending in the captured text", payload.Detail)
	}
	if payload.RelPath != "Inbox.md" {
		t.Fatalf("rel_path = %q, want Inbox.md", payload.RelPath)
	}
	if payload.Key != "inbox" {
		t.Fatalf("key = %q, want inbox", payload.Key)
	}
	if payload.Path != mustManagementNotePath("Inbox.md") {
		t.Fatalf("path = %q, want the inbox note route", payload.Path)
	}
	if payload.JournalID == "" {
		t.Fatal("journal_id is empty, want the undo entry id")
	}
	if !strings.Contains(payload.Message, "Inbox.md") {
		t.Fatalf("message = %q, want it to name the inbox note", payload.Message)
	}

	got := readNoteFile(t, filepath.Join(root, "Inbox.md"))
	want := "---\ntitle: Inbox\n---\nIntro.\n\n## Captures\n- 2026-01-01 08:00 older\n" + payload.Detail + "\n\n## Later\nTail.\n"
	if got != want {
		t.Fatalf("note contents:\ngot  %q\nwant %q", got, want)
	}
}

func TestCaptureAPIBootstrapsMissingInbox(t *testing.T) {
	ts, _, root := newCaptureTestServer(t, map[string]string{
		"Note.md": "---\ntitle: Note\n---\nBody\n",
	}, nil)

	inbox := filepath.Join(root, "Inbox.md")
	if _, err := os.Stat(inbox); !os.IsNotExist(err) {
		t.Fatalf("inbox should not exist yet: %v", err)
	}

	form := url.Values{}
	form.Set("text", "first thought")
	payload := decodeCaptureSuccess(t, postCapture(t, ts, form, captureTestToken))
	if !strings.Contains(payload.Message, "Created") {
		t.Fatalf("message = %q, want it to report the created note", payload.Message)
	}

	got := readNoteFile(t, inbox)
	if !strings.Contains(got, "title: Inbox") {
		t.Fatalf("bootstrapped note is missing its frontmatter title:\n%s", got)
	}
	if !strings.Contains(got, "## Captures\n"+payload.Detail+"\n") {
		t.Fatalf("bootstrapped note is missing the heading and bullet:\n%s", got)
	}
}

func TestCaptureAPIInvalidatesSnapshot(t *testing.T) {
	ts, srv, _ := newCaptureTestServer(t, map[string]string{
		"Note.md": "---\ntitle: Note\n---\nBody\n",
	}, nil)

	primed, err := srv.state.currentSnapshot()
	if err != nil {
		t.Fatalf("prime snapshot: %v", err)
	}
	cached, err := srv.state.currentSnapshot()
	if err != nil {
		t.Fatalf("cached snapshot: %v", err)
	}
	if cached.serial != primed.serial {
		t.Fatalf("frozen clock should serve the cached snapshot: %d != %d", cached.serial, primed.serial)
	}

	form := url.Values{}
	form.Set("text", "invalidate me")
	decodeCaptureSuccess(t, postCapture(t, ts, form, captureTestToken))

	after, err := srv.state.currentSnapshot()
	if err != nil {
		t.Fatalf("post-capture snapshot: %v", err)
	}
	if after.serial == primed.serial {
		t.Fatalf("snapshot serial %d unchanged, want a reload after invalidate", after.serial)
	}
}

func TestIndexRendersWithoutInboxNote(t *testing.T) {
	ts, _, root := newCaptureTestServer(t, map[string]string{
		"Note.md": "---\ntitle: Note\n---\nBody\n",
	}, nil)

	if _, err := os.Stat(filepath.Join(root, "Inbox.md")); !os.IsNotExist(err) {
		t.Fatalf("inbox should not exist yet: %v", err)
	}
	resp, err := ts.Client().Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("get index: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestCaptureDestination(t *testing.T) {
	relPath, heading, configured := captureDestination(" Inbox.md ", " ## Captures ")
	if relPath != "Inbox.md" || heading != "## Captures" || !configured {
		t.Fatalf("captureDestination = (%q, %q, %v), want trimmed values and configured", relPath, heading, configured)
	}
	if _, _, ok := captureDestination("", "## Captures"); ok {
		t.Fatal("an empty inbox note must not count as configured")
	}
	if _, _, ok := captureDestination("Inbox.md", "  "); ok {
		t.Fatal("an empty heading must not count as configured")
	}
}
