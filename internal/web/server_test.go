package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func buildWebTestVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	note := filepath.Join(root, "Note.md")
	if err := os.WriteFile(note, []byte("---\ntitle: Note\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	return root
}

func testVaultOpen(t *testing.T, root string, addr string) (*config.Config, *vault.Vault) {
	t.Helper()
	disabled := false
	cfg := &config.Config{
		Vault: root,
		Serve: config.ServeConfig{
			Addr:        addr,
			OpenBrowser: &disabled,
		},
	}
	v, err := vault.Open(cfg)
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	return cfg, v
}

func awaitGET(t *testing.T, rawURL string, timeout time.Duration) (*http.Response, error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		resp, err := http.Get(rawURL) // #nosec G107 -- test-only local listener URL
		if err == nil {
			return resp, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestServerRun_RandomPortAndGracefulShutdown(t *testing.T) {
	root := buildWebTestVault(t)
	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")

	notify := make(chan string, 1)
	srv, err := NewServer(cfg, v, Options{
		NotifyListening: func(rawURL string) {
			notify <- rawURL
		},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Run(ctx)
	}()

	var baseURL string
	select {
	case baseURL = <-notify:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for listener notification")
	}

	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse baseURL: %v", err)
	}
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host/port: %v", err)
	}
	if port == "0" || port == "" {
		t.Fatalf("expected random non-zero port, got %q", port)
	}

	resp, err := awaitGET(t, baseURL+"/", 2*time.Second)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(string(body), "Recent notes") {
		t.Fatalf("index response missing recent notes section; got %q", string(body))
	}
	if !strings.Contains(string(body), `href="/n/note"`) {
		t.Fatalf("index response missing note link; got %q", string(body))
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error after cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for graceful shutdown")
	}
}

func TestServerServe_IndexHandler(t *testing.T) {
	root := buildWebTestVault(t)
	cfg, v := testVaultOpen(t, root, "127.0.0.1:8729")
	srv, err := NewServer(cfg, v, Options{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ctx, ln)
	}()

	baseURL := "http://" + ln.Addr().String()
	resp, err := awaitGET(t, baseURL+"/", 2*time.Second)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(string(body), "<h1>bn serve</h1>") {
		t.Fatalf("expected index heading, got %q", string(body))
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Serve returned error after cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for graceful shutdown")
	}
}

func TestServer_IndexViewStatsAndRecentOrdering(t *testing.T) {
	ts, root := newHTTPServerForFiles(t, map[string][]byte{
		"Newest.md": []byte("---\ntitle: Newest\n---\n[[Missing]]\n"),
		"Middle.md": []byte("---\ntitle: Middle\n---\n[[Newest]]\n"),
		"Oldest.md": []byte("---\ntitle: Oldest\n---\nBody\n"),
	})

	now := time.Now()
	stamps := map[string]time.Time{
		"Newest.md": now.Add(-1 * time.Minute),
		"Middle.md": now.Add(-2 * time.Minute),
		"Oldest.md": now.Add(-3 * time.Minute),
	}
	for rel, stamp := range stamps {
		path := filepath.Join(root, rel)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatalf("chtimes %s: %v", rel, err)
		}
	}

	resp, err := http.Get(ts.URL + "/") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}

	assertMetric := func(label, want string) {
		t.Helper()
		re := regexp.MustCompile(`(?s)<h2>` + regexp.QuoteMeta(label) + `</h2>\s*<p class="metric-value">([0-9]+)</p>`)
		match := re.FindStringSubmatch(body)
		if len(match) != 2 {
			t.Fatalf("metric %q missing in body: %q", label, body)
		}
		if match[1] != want {
			t.Fatalf("metric %q: got %q, want %q", label, match[1], want)
		}
	}

	assertMetric("Total notes", "3")
	assertMetric("Broken targets", "1")
	assertMetric("Orphans", "2")

	newestIdx := strings.Index(body, `href="/n/newest"`)
	middleIdx := strings.Index(body, `href="/n/middle"`)
	oldestIdx := strings.Index(body, `href="/n/oldest"`)
	if newestIdx < 0 || middleIdx < 0 || oldestIdx < 0 {
		t.Fatalf("recent note links missing, got %q", body)
	}
	if !(newestIdx < middleIdx && middleIdx < oldestIdx) {
		t.Fatalf("recent notes not sorted by recency; indexes newest=%d middle=%d oldest=%d", newestIdx, middleIdx, oldestIdx)
	}
}

func newHTTPServerForFiles(t *testing.T, files map[string][]byte) (*httptest.Server, string) {
	return newHTTPServerForFilesWithOptions(t, files, nil, Options{})
}

func newHTTPServerForFilesWithOptions(
	t *testing.T,
	files map[string][]byte,
	configure func(cfg *config.Config),
	opts Options,
) (*httptest.Server, string) {
	t.Helper()

	root := t.TempDir()
	for rel, data := range files {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", filepath.Dir(abs), err)
		}
		if err := os.WriteFile(abs, data, 0o644); err != nil {
			t.Fatalf("write %q: %v", rel, err)
		}
	}

	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")
	if configure != nil {
		configure(cfg)
	}
	srv, err := NewServer(cfg, v, opts)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, root
}

func postOpenRequest(t *testing.T, baseURL, key, headerToken, formToken string) *http.Response {
	t.Helper()

	form := url.Values{}
	if key != "" {
		form.Set("key", key)
	}
	if formToken != "" {
		form.Set("token", formToken)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/open", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/open: %v", err)
	}
	return resp
}

func readResponseBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

func fetchGraphJSON(t *testing.T, rawURL string) (graphJSON, string, string) {
	t.Helper()

	resp, err := http.Get(rawURL) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET %s: %v", rawURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, string(body))
	}

	var payload graphJSON
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode graph json: %v", err)
	}

	return payload, string(body), resp.Header.Get("Content-Type")
}

func graphNodeDataByID(payload graphJSON) map[string]graphJSONNodeData {
	nodes := make(map[string]graphJSONNodeData, len(payload.Nodes))
	for _, node := range payload.Nodes {
		nodes[node.Data.ID] = node.Data
	}
	return nodes
}

func denseGraphVaultFixture() map[string][]byte {
	return map[string][]byte{
		"Hub.md":    []byte("---\ntitle: Hub\n---\n[[SpokeA]] [[SpokeB]] [[SpokeC]] [[SpokeD]] [[SpokeE]]\n"),
		"SpokeA.md": []byte("---\ntitle: Spoke A\n---\n[[Hub]] [[SpokeB]] [[SpokeC]]\n"),
		"SpokeB.md": []byte("---\ntitle: Spoke B\n---\n[[Hub]] [[SpokeC]] [[SpokeD]]\n"),
		"SpokeC.md": []byte("---\ntitle: Spoke C\n---\n[[Hub]] [[SpokeD]] [[SpokeE]]\n"),
		"SpokeD.md": []byte("---\ntitle: Spoke D\n---\n[[Hub]] [[SpokeE]] [[SpokeA]]\n"),
		"SpokeE.md": []byte("---\ntitle: Spoke E\n---\n[[Hub]] [[SpokeA]] [[SpokeB]]\n"),
		"Leaf.md":   []byte("---\ntitle: Leaf\n---\n[[Hub]]\n"),
		"Island.md": []byte("---\ntitle: Island\n---\nNo links.\n"),
	}
}

func fetchSearchJSON(t *testing.T, rawURL string) ([]searchResult, string, int) {
	t.Helper()

	resp, err := http.Get(rawURL) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET %s: %v", rawURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	var payload []searchResult
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode search json: %v; body=%q", err, string(body))
	}

	return payload, resp.Header.Get("Content-Type"), resp.StatusCode
}

func TestServer_NoteView200AndUnknown404(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Source.md": []byte("---\nmeta: \"[[Target]]\"\n---\nBody [[Target]]\n"),
		"Target.md": []byte("---\ntitle: Target\n---\nTarget body\n"),
	})

	resp, err := http.Get(ts.URL + "/n/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /n/source: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(body, "<h1>Source</h1>") {
		t.Fatalf("note title missing, got %q", body)
	}
	if !strings.Contains(body, `href="/n/target"`) {
		t.Fatalf("resolved note link missing, got %q", body)
	}

	missingResp, err := http.Get(ts.URL + "/n/does-not-exist") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET missing note: %v", err)
	}
	defer missingResp.Body.Close()
	if missingResp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want %d", missingResp.StatusCode, http.StatusNotFound)
	}
}

func TestServer_NoteViewBacklinksPanel(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Alpha.md": []byte("---\ntitle: Alpha\n---\nAlpha body\n"),
		"Beta.md":  []byte("---\ntitle: Beta\n---\n[[Alpha]]\n"),
	})

	resp, err := http.Get(ts.URL + "/n/alpha") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /n/alpha: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(body, "<h2>Backlinks</h2>") {
		t.Fatalf("backlinks header missing, got %q", body)
	}
	if !strings.Contains(body, `href="/n/beta"`) {
		t.Fatalf("backlink entry missing, got %q", body)
	}
}

func TestServer_NoteViewFrontmatterWikilinkAnchors(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Source.md": []byte("---\nmeta: \"See [[Target]] and [[Missing]]\"\nnested:\n  child: \"[[Target#Heading|Alias]]\"\n---\nbody\n"),
		"Target.md": []byte("---\ntitle: Target\n---\nTarget body\n"),
	})

	resp, err := http.Get(ts.URL + "/n/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /n/source: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(body, "See [[Target]] and [[Missing]]") {
		t.Fatalf("frontmatter text missing, got %q", body)
	}
	if strings.Count(body, `href="/n/target"`) < 2 {
		t.Fatalf("expected anchors for frontmatter wikilinks, got %q", body)
	}
	if !strings.Contains(body, `href="/broken?q=missing"`) {
		t.Fatalf("missing broken-link affordance for frontmatter link, got %q", body)
	}
}

func TestServer_NoteViewDuplicateTitleAmbiguity(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Work/Alpha.md": []byte("---\ntitle: Alpha\n---\nwork\n"),
		"Play/Alpha.md": []byte("---\ntitle: Alpha\n---\nplay\n"),
		"Source.md":     []byte("---\ntitle: Source\n---\n[[Alpha]]\n"),
	})

	resp, err := http.Get(ts.URL + "/n/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /n/source: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(body, `href="/broken?q=alpha"`) {
		t.Fatalf("expected ambiguous wikilink to route to broken page, got %q", body)
	}
	if strings.Contains(body, `href="/n/alpha"`) {
		t.Fatalf("unexpected direct link for ambiguous target, got %q", body)
	}
}

func TestServer_NoteViewIncludesOpenEditorControls(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, nil, Options{
		CSRFToken: "csrf-test-token",
	})

	resp, err := http.Get(ts.URL + "/n/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /n/source: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(body, `data-open-editor`) {
		t.Fatalf("missing open editor controls, got %q", body)
	}
	if !strings.Contains(body, `data-open-key="source"`) {
		t.Fatalf("missing note key data attribute, got %q", body)
	}
	if !strings.Contains(body, `data-open-csrf="csrf-test-token"`) {
		t.Fatalf("missing csrf token data attribute, got %q", body)
	}
}

func TestServer_OpenAPI_SuccessWithEditorArgs(t *testing.T) {
	var (
		gotBin  string
		gotArgv []string
		calls   int
	)
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, func(cfg *config.Config) {
		cfg.Open.Editor = "echo --wait"
	}, Options{
		CSRFToken: "csrf-token",
		StartEditor: func(bin string, argv []string) error {
			calls++
			gotBin = bin
			gotArgv = append([]string(nil), argv...)
			return nil
		},
	})

	resp := postOpenRequest(t, ts.URL, "source", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusNoContent, body)
	}
	if calls != 1 {
		t.Fatalf("start editor calls: got %d, want %d", calls, 1)
	}
	if !strings.HasPrefix(filepath.Base(gotBin), "echo") {
		t.Fatalf("bin: got %q, want echo binary", gotBin)
	}
	expectedPath, err := resolveOpenPath(root, filepath.Join(root, "Source.md"))
	if err != nil {
		t.Fatalf("resolve expected source path: %v", err)
	}
	wantArgv := []string{"echo", "--wait", "+1", expectedPath}
	if len(gotArgv) != len(wantArgv) {
		t.Fatalf("argv length: got %d, want %d (%v)", len(gotArgv), len(wantArgv), gotArgv)
	}
	for i := range wantArgv {
		if gotArgv[i] != wantArgv[i] {
			t.Fatalf("argv[%d]: got %q, want %q (full=%v)", i, gotArgv[i], wantArgv[i], gotArgv)
		}
	}
}

func TestServer_OpenAPI_CSRFRequired(t *testing.T) {
	called := false
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, func(cfg *config.Config) {
		cfg.Open.Editor = "echo"
	}, Options{
		CSRFToken: "csrf-token",
		StartEditor: func(bin string, argv []string) error {
			called = true
			return nil
		},
	})

	resp := postOpenRequest(t, ts.URL, "source", "", "")
	readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
	if called {
		t.Fatal("start editor should not be called without csrf")
	}
}

func TestServer_OpenAPI_AcceptsFormCSRFToken(t *testing.T) {
	called := false
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, func(cfg *config.Config) {
		cfg.Open.Editor = "echo"
	}, Options{
		CSRFToken: "csrf-token",
		StartEditor: func(bin string, argv []string) error {
			called = true
			return nil
		},
	})

	resp := postOpenRequest(t, ts.URL, "source", "", "csrf-token")
	readResponseBody(t, resp)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	if !called {
		t.Fatal("start editor should be called with valid form csrf token")
	}
}

func TestServer_OpenAPI_BadKeyResponses(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, func(cfg *config.Config) {
		cfg.Open.Editor = "echo"
	}, Options{
		CSRFToken: "csrf-token",
		StartEditor: func(bin string, argv []string) error {
			t.Fatal("start editor should not be called for bad key requests")
			return nil
		},
	})

	resp := postOpenRequest(t, ts.URL, "../source", "csrf-token", "")
	readResponseBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid key status: got %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	resp = postOpenRequest(t, ts.URL, "missing", "csrf-token", "")
	readResponseBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown key status: got %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestServer_OpenAPI_RejectsEscapingSymlinkPath(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "Source.md")
	if err := os.WriteFile(sourcePath, []byte("---\ntitle: Source\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write source note: %v", err)
	}
	outsideRoot := t.TempDir()
	outsideNote := filepath.Join(outsideRoot, "Outside.md")
	if err := os.WriteFile(outsideNote, []byte("---\ntitle: Outside\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write outside note: %v", err)
	}

	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")
	cfg.Open.Editor = "echo"
	srv, err := NewServer(cfg, v, Options{
		CSRFToken: "csrf-token",
		StartEditor: func(bin string, argv []string) error {
			t.Fatal("start editor should not be called for escaping path")
			return nil
		},
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

	resp := postOpenRequest(t, ts.URL, "source", "csrf-token", "")
	readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

func TestServer_OpenAPI_MissingEditorConfigured(t *testing.T) {
	t.Setenv("EDITOR", "")

	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, func(cfg *config.Config) {
		cfg.Open.Editor = ""
	}, Options{
		CSRFToken: "csrf-token",
		StartEditor: func(bin string, argv []string) error {
			t.Fatal("start editor should not be called without editor config")
			return nil
		},
	})

	resp := postOpenRequest(t, ts.URL, "source", "csrf-token", "")
	readResponseBody(t, resp)
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusPreconditionFailed)
	}
}

func TestServer_MediaRouteContainment(t *testing.T) {
	ts, root := newHTTPServerForFiles(t, map[string][]byte{
		"Source.md":  []byte("---\ntitle: Source\n---\n![[image.png]]\n![[missing.png]]\n"),
		"image.png":  []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"),
		"notes/A.md": []byte("---\ntitle: A\n---\nBody\n"),
	})

	noteResp, err := http.Get(ts.URL + "/n/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /n/source: %v", err)
	}
	noteBody := readResponseBody(t, noteResp)
	if noteResp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", noteResp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(noteBody, "/media/image.png") {
		t.Fatalf("expected existing media embed path, got %q", noteBody)
	}
	if !strings.Contains(noteBody, "missing.png") {
		t.Fatalf("expected missing media embed to stay plain text, got %q", noteBody)
	}
	if strings.Contains(noteBody, "/broken?q=missing.png") {
		t.Fatalf("media embed should not route to broken page, got %q", noteBody)
	}

	mediaResp, err := http.Get(ts.URL + "/media/image.png") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /media/image.png: %v", err)
	}
	defer mediaResp.Body.Close()
	if mediaResp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", mediaResp.StatusCode, http.StatusOK)
	}
	if got := mediaResp.Header.Get("Content-Type"); !strings.HasPrefix(got, "image/png") {
		t.Fatalf("content-type: got %q, want image/png...", got)
	}

	traversalResp, err := http.Get(ts.URL + "/media/%2e%2e/secret.png") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET traversal media path: %v", err)
	}
	defer traversalResp.Body.Close()
	if traversalResp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want %d", traversalResp.StatusCode, http.StatusNotFound)
	}

	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	link := filepath.Join(root, "leak.png")
	if err := os.Symlink(outside, link); err == nil {
		symlinkResp, err := http.Get(ts.URL + "/media/leak.png") // #nosec G107 -- test-only local URL
		if err != nil {
			t.Fatalf("GET symlink media path: %v", err)
		}
		defer symlinkResp.Body.Close()
		if symlinkResp.StatusCode != http.StatusNotFound {
			t.Fatalf("status: got %d, want %d", symlinkResp.StatusCode, http.StatusNotFound)
		}
	}
}

func TestServer_BrokenPageEscapesQuery(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Missing]]\n"),
	})

	q := url.QueryEscape("<script>alert(1)</script>")
	resp, err := http.Get(ts.URL + "/broken?q=" + q) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /broken: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatalf("unescaped query found in response: %q", body)
	}
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("escaped query not found in response: %q", body)
	}
}

func TestServer_SearchAPI_ResultShapeAndRanking(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"NeedleGuide.md":  []byte("---\ntitle: Needle Guide\n---\nIntro text\n"),
		"BodyOne.md":      []byte("---\ntitle: Body One\n---\nThis body has needle in it.\n"),
		"Work/BodyTwo.md": []byte("---\ntitle: Body Two\n---\nThis body has NEEDLE in it.\n"),
		"Escape.md":       []byte("---\ntitle: Escape\n---\n<script>alert(1)</script> with needle\n"),
		"Other.md":        []byte("---\ntitle: Other\n---\nNo hit here.\n"),
	})

	results, contentType, statusCode := fetchSearchJSON(t, ts.URL+"/api/search?q=needle")
	if statusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", statusCode, http.StatusOK)
	}
	if !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("content-type: got %q, want application/json...", contentType)
	}
	if len(results) != 4 {
		t.Fatalf("result count: got %d, want 4", len(results))
	}

	if results[0].Title != "NeedleGuide" {
		t.Fatalf("expected title match to rank first, got %#v", results[0])
	}
	if results[1].Title != "BodyOne" || results[2].Title != "BodyTwo" || results[3].Title != "Escape" {
		t.Fatalf("unexpected body-match ordering: %#v", results)
	}

	for _, result := range results {
		if result.Key == "" {
			t.Fatalf("expected non-empty key in result: %#v", result)
		}
		if !strings.HasPrefix(result.Path, "/n/") {
			t.Fatalf("expected note path to start with /n/, got %#v", result)
		}
	}
}

func TestServer_SearchAPI_EmptyAndNoMatchQueries(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Note.md": []byte("---\ntitle: Note\n---\nBody text.\n"),
	})

	emptyResults, _, emptyStatus := fetchSearchJSON(t, ts.URL+"/api/search?q=")
	if emptyStatus != http.StatusOK {
		t.Fatalf("status for empty query: got %d, want %d", emptyStatus, http.StatusOK)
	}
	if len(emptyResults) != 0 {
		t.Fatalf("expected empty result list for empty query, got %#v", emptyResults)
	}

	noMatchResults, _, noMatchStatus := fetchSearchJSON(t, ts.URL+"/api/search?q=missing")
	if noMatchStatus != http.StatusOK {
		t.Fatalf("status for no-match query: got %d, want %d", noMatchStatus, http.StatusOK)
	}
	if len(noMatchResults) != 0 {
		t.Fatalf("expected empty result list for no-match query, got %#v", noMatchResults)
	}
}

func TestServer_SearchAPI_SnippetEscaping(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Danger.md": []byte("---\ntitle: Danger\n---\n<script>alert(1)</script> marker\n"),
	})

	results, _, statusCode := fetchSearchJSON(t, ts.URL+"/api/search?q=alert")
	if statusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", statusCode, http.StatusOK)
	}
	if len(results) != 1 {
		t.Fatalf("result count: got %d, want 1", len(results))
	}
	if strings.Contains(results[0].Snippet, "<script>") {
		t.Fatalf("expected escaped snippet, got %q", results[0].Snippet)
	}
	if !strings.Contains(results[0].Snippet, "&lt;script&gt;") {
		t.Fatalf("missing escaped script tag in snippet: %q", results[0].Snippet)
	}
}

func TestServer_SearchAPI_ResultCap(t *testing.T) {
	files := make(map[string][]byte, 60)
	for i := 0; i < 60; i++ {
		rel := fmt.Sprintf("Note%02d.md", i)
		files[rel] = []byte(fmt.Sprintf("---\ntitle: Note %02d\n---\nNeedle body text\n", i))
	}
	ts, _ := newHTTPServerForFiles(t, files)

	results, _, statusCode := fetchSearchJSON(t, ts.URL+"/api/search?q=needle")
	if statusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", statusCode, http.StatusOK)
	}
	if len(results) != searchResultLimit {
		t.Fatalf("result count: got %d, want %d", len(results), searchResultLimit)
	}
}

func TestServer_PagesIncludeSearchUI(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	})

	assertSearchUI := func(path string) {
		t.Helper()
		resp, err := http.Get(ts.URL + path) // #nosec G107 -- test-only local URL
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status for %s: got %d, want %d", path, resp.StatusCode, http.StatusOK)
		}
		if !strings.Contains(body, `id="search-input"`) {
			t.Fatalf("missing search input on %s, got %q", path, body)
		}
		if !strings.Contains(body, `Search notes`) {
			t.Fatalf("missing search label on %s, got %q", path, body)
		}
		if !strings.Contains(body, `placeholder="Search title or body..."`) {
			t.Fatalf("missing search placeholder on %s, got %q", path, body)
		}
		if !strings.Contains(body, `id="search-results"`) {
			t.Fatalf("missing search result list on %s, got %q", path, body)
		}
		if !strings.Contains(body, `/static/app.js`) {
			t.Fatalf("missing app.js asset on %s, got %q", path, body)
		}
	}

	assertSearchUI("/")
	assertSearchUI("/n/source")
	assertSearchUI("/broken")
}

func TestServer_GraphPageIncludesGraphControls(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	})

	resp, err := http.Get(ts.URL + "/graph") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /graph: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status for /graph: got %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(body, `id="graph-filter-input"`) {
		t.Fatalf("missing graph filter input on /graph, got %q", body)
	}
	if !strings.Contains(body, `Filter graph`) {
		t.Fatalf("missing graph filter label on /graph, got %q", body)
	}
	if !strings.Contains(body, `placeholder="Filter graph by title or body..."`) {
		t.Fatalf("missing graph filter placeholder on /graph, got %q", body)
	}
	if !strings.Contains(body, `id="graph-spread-input"`) {
		t.Fatalf("missing graph spread slider on /graph, got %q", body)
	}
	if !strings.Contains(body, `min="0.8"`) {
		t.Fatalf("missing graph spread min on /graph, got %q", body)
	}
	if !strings.Contains(body, `max="6.0"`) {
		t.Fatalf("missing graph spread max on /graph, got %q", body)
	}
	if !strings.Contains(body, `step="0.1"`) {
		t.Fatalf("missing graph spread step on /graph, got %q", body)
	}
	if !strings.Contains(body, `value="2.0"`) {
		t.Fatalf("missing graph spread default value on /graph, got %q", body)
	}
	if !strings.Contains(body, `id="graph-spread-value"`) {
		t.Fatalf("missing graph spread value text on /graph, got %q", body)
	}
	if !strings.Contains(body, `>2.0x</output>`) {
		t.Fatalf("missing default spread multiplier on /graph, got %q", body)
	}
	if !strings.Contains(body, `id="graph-fit-button"`) {
		t.Fatalf("missing graph fit button on /graph, got %q", body)
	}
	if !strings.Contains(body, `id="graph-reset-layout-button"`) {
		t.Fatalf("missing graph reset layout button on /graph, got %q", body)
	}
	if !strings.Contains(body, `id="graph-label-mode-auto"`) {
		t.Fatalf("missing graph auto label control on /graph, got %q", body)
	}
	if !strings.Contains(body, `name="graph-label-mode" value="auto" checked`) {
		t.Fatalf("missing checked auto label mode on /graph, got %q", body)
	}
	if !strings.Contains(body, `id="graph-label-mode-all"`) {
		t.Fatalf("missing graph all label control on /graph, got %q", body)
	}
	if !strings.Contains(body, `id="graph-open-note"`) {
		t.Fatalf("missing graph open note link on /graph, got %q", body)
	}
	if !strings.Contains(body, `id="graph-hover-degree"`) {
		t.Fatalf("missing graph degree detail on /graph, got %q", body)
	}
	if !strings.Contains(body, `aria-disabled="true"`) {
		t.Fatalf("missing initially disabled graph open note link on /graph, got %q", body)
	}
	if !strings.Contains(body, `id="graph-canvas" aria-label="Notes graph" tabindex="0"`) {
		t.Fatalf("missing focusable graph canvas on /graph, got %q", body)
	}
	if strings.Contains(body, `id="search-input"`) {
		t.Fatalf("unexpected shared search input on /graph, got %q", body)
	}
	if strings.Contains(body, `id="search-results"`) {
		t.Fatalf("unexpected shared search result list on /graph, got %q", body)
	}
	if !strings.Contains(body, `/static/app.js`) {
		t.Fatalf("missing app.js asset on /graph, got %q", body)
	}
}

func TestServer_GraphAPIJSON_Golden(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Source.md":     []byte("---\ntitle: Source\n---\n[[Target]]\n"),
		"Target.md":     []byte("---\ntitle: Target\n---\nTarget body\n"),
		"Loner.md":      []byte("---\ntitle: Loner\n---\nNo links\n"),
		"Work/Alpha.md": []byte("---\ntitle: Alpha\n---\nAlpha body\n"),
	})

	payload, _, contentType := fetchGraphJSON(t, ts.URL+"/api/graph.json")
	if !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("content-type: got %q, want application/json...", contentType)
	}

	got, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("marshal indent: %v", err)
	}
	got = append(got, '\n')

	want, err := os.ReadFile(filepath.Join("testdata", "graph_api.golden.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("graph json mismatch\n--- got ---\n%s\n--- want ---\n%s", string(got), string(want))
	}
}

func TestServer_GraphAPIJSON_DegreeMetadata(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, denseGraphVaultFixture())

	payload, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json")
	nodes := graphNodeDataByID(payload)

	hub := nodes["hub"]
	if hub.InDegree != 6 || hub.OutDegree != 5 || hub.Degree != 11 {
		t.Fatalf("hub degrees: got in=%d out=%d total=%d, want in=6 out=5 total=11", hub.InDegree, hub.OutDegree, hub.Degree)
	}

	island := nodes["island"]
	if island.InDegree != 0 || island.OutDegree != 0 || island.Degree != 0 {
		t.Fatalf("island degrees: got in=%d out=%d total=%d, want all zero", island.InDegree, island.OutDegree, island.Degree)
	}
}

func TestServer_GraphAPIJSON_DeterministicOrdering(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"C.md": []byte("---\ntitle: C\n---\nBody\n"),
		"A.md": []byte("---\ntitle: A\n---\n[[C]] [[B]] [[B]]\n"),
		"B.md": []byte("---\ntitle: B\n---\n[[C]]\n"),
	})

	first, raw1, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json")
	_, raw2, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json")
	_, raw3, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json")
	if raw1 != raw2 || raw2 != raw3 {
		t.Fatalf("graph api output is not deterministic across calls")
	}

	for i := 1; i < len(first.Nodes); i++ {
		prev := first.Nodes[i-1].Data.ID
		curr := first.Nodes[i].Data.ID
		if prev > curr {
			t.Fatalf("nodes not sorted: %q before %q", prev, curr)
		}
	}

	for i := 1; i < len(first.Edges); i++ {
		prev := first.Edges[i-1].Data.Source + "\x00" + first.Edges[i-1].Data.Target
		curr := first.Edges[i].Data.Source + "\x00" + first.Edges[i].Data.Target
		if prev > curr {
			t.Fatalf("edges not sorted: %#v before %#v", first.Edges[i-1].Data, first.Edges[i].Data)
		}
	}
}

func TestServer_GraphAPIJSON_SelfLoopExclusion(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Self.md":  []byte("---\ntitle: Self\n---\n[[Self]] [[Peer]]\n"),
		"Peer.md":  []byte("---\ntitle: Peer\n---\nBody\n"),
		"Other.md": []byte("---\ntitle: Other\n---\nBody\n"),
	})

	payload, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json")
	for _, edge := range payload.Edges {
		if edge.Data.Source == edge.Data.Target {
			t.Fatalf("unexpected self-loop edge: %#v", edge.Data)
		}
	}
}

func TestServer_GraphAPIJSON_QueryFiltersTitleBodyAndEdges(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"NeedleA.md":  []byte("---\ntitle: Needle A\n---\n[[NeedleB]] [[Other]]\n"),
		"NeedleB.md":  []byte("---\ntitle: Needle B\n---\nBody\n"),
		"BodyOnly.md": []byte("---\ntitle: Body Only\n---\ncontains nEeDlE token\n"),
		"Other.md":    []byte("---\ntitle: Other\n---\nBody\n"),
	})

	payload, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json?q=NEEDLE")
	if len(payload.Nodes) != 3 {
		t.Fatalf("node count: got %d, want %d", len(payload.Nodes), 3)
	}

	nodeIDs := make(map[string]struct{}, len(payload.Nodes))
	for _, node := range payload.Nodes {
		nodeIDs[node.Data.ID] = struct{}{}
	}

	for _, id := range []string{"needlea", "needleb", "bodyonly"} {
		if _, ok := nodeIDs[id]; !ok {
			t.Fatalf("expected node %q to be included; got %#v", id, payload.Nodes)
		}
	}
	if _, ok := nodeIDs["other"]; ok {
		t.Fatalf("expected non-matching node to be excluded; got %#v", payload.Nodes)
	}

	if len(payload.Edges) != 1 {
		t.Fatalf("edge count: got %d, want %d", len(payload.Edges), 1)
	}
	edge := payload.Edges[0].Data
	if edge.Source != "needlea" || edge.Target != "needleb" {
		t.Fatalf("unexpected edge: got %#v, want NeedleA->NeedleB only", edge)
	}
}

func TestServer_GraphAPIJSON_QueryKeepsFullGraphDegreeMetadata(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Target.md":  []byte("---\ntitle: Target\n---\nrareword body [[Other]]\n"),
		"SourceA.md": []byte("---\ntitle: Source A\n---\n[[Target]]\n"),
		"SourceB.md": []byte("---\ntitle: Source B\n---\n[[Target]]\n"),
		"Other.md":   []byte("---\ntitle: Other\n---\nBody\n"),
	})

	payload, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json?q=rareword")
	if len(payload.Nodes) != 1 {
		t.Fatalf("node count: got %d, want 1", len(payload.Nodes))
	}
	if len(payload.Edges) != 0 {
		t.Fatalf("edge count: got %d, want 0", len(payload.Edges))
	}

	target := payload.Nodes[0].Data
	if target.ID != "target" {
		t.Fatalf("node id: got %q, want target", target.ID)
	}
	if target.InDegree != 2 || target.OutDegree != 1 || target.Degree != 3 {
		t.Fatalf("target degrees: got in=%d out=%d total=%d, want in=2 out=1 total=3", target.InDegree, target.OutDegree, target.Degree)
	}
}

func TestServer_GraphAPIJSON_QueryIsUncapped(t *testing.T) {
	files := make(map[string][]byte, 60)
	for i := 0; i < 60; i++ {
		rel := fmt.Sprintf("Graph%02d.md", i)
		files[rel] = []byte(fmt.Sprintf("---\ntitle: Graph %02d\n---\nneedle body text\n", i))
	}
	ts, _ := newHTTPServerForFiles(t, files)

	payload, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json?q=needle")
	if len(payload.Nodes) != 60 {
		t.Fatalf("node count: got %d, want %d", len(payload.Nodes), 60)
	}
}

func TestServer_GraphAPIJSON_EmptyQueryMatchesDefaultGraph(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Target]]\n"),
		"Target.md": []byte("---\ntitle: Target\n---\nBody\n"),
		"Other.md":  []byte("---\ntitle: Other\n---\nBody\n"),
	})

	_, rawDefault, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json")
	_, rawEmpty, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json?q=")
	_, rawWhitespace, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json?q="+url.QueryEscape("  "))
	if rawDefault != rawEmpty || rawDefault != rawWhitespace {
		t.Fatalf("empty graph query should match default output")
	}
}

func TestServer_GraphPageIncludesAssets(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Note.md": []byte("---\ntitle: Note\n---\nBody\n"),
	})

	resp, err := http.Get(ts.URL + "/graph") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /graph: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(body, `/static/cytoscape.min.js`) {
		t.Fatalf("missing cytoscape asset tag, got %q", body)
	}
	if !strings.Contains(body, `/static/app.js`) {
		t.Fatalf("missing app.js asset tag, got %q", body)
	}
	if !strings.Contains(body, `id="graph-canvas"`) {
		t.Fatalf("missing graph canvas container, got %q", body)
	}
}
