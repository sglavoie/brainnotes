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
	"testing/fstest"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
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

func TestServer_UsesInjectedStaticFiles(t *testing.T) {
	root := buildWebTestVault(t)
	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")
	srv, err := NewServer(cfg, v, Options{
		StaticFiles: fstest.MapFS{
			"app.js": {Data: []byte("fresh build")},
		},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/static/app.js") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /static/app.js: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if body != "fresh build" {
		t.Fatalf("body = %q, want fresh build", body)
	}
}

func TestServer_IndexIncludesOverviewSearchResultsPanel(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	})

	resp, err := http.Get(ts.URL + "/") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}

	required := []string{
		`data-facet-mode="search"`,
		`data-overview-search-results`,
		`aria-labelledby="overview-results-heading"`,
		`aria-describedby="overview-results-status"`,
		`aria-busy="false"`,
		`id="overview-results-heading"`,
		`Search results`,
		`id="overview-results-status"`,
		`data-overview-search-status`,
		`role="status"`,
		`aria-live="polite"`,
		`No query or filters selected.`,
		`class="overview-result-list"`,
		`data-overview-search-list`,
		`hidden`,
		`data-history-panel`,
		`data-history-csrf=`,
		`aria-labelledby="history-heading"`,
		`id="history-status"`,
		`data-history-status`,
		`data-history-refresh`,
		`data-history-undo-latest`,
		`Undo latest change`,
		`data-history-force`,
		`data-history-force disabled`,
		`aria-label="Move selected notes to Trash"`,
		`data-overview-bulk-preview`,
		`data-overview-bulk-preview-title`,
		`data-overview-bulk-preview-message`,
		`data-overview-bulk-preview-apply`,
		`data-overview-bulk-preview-clear`,
		`data-overview-bulk-preview-results`,
		`data-overview-bulk-preview-failures`,
	}
	for _, want := range required {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in overview search panel, got %q", want, body)
		}
	}
}

func TestServer_IndexIncludesDailyFixesCockpit(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Missing]]\n"),
		"Linked.md": []byte("---\ntitle: Linked\n---\n[[Source]]\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	resp, err := http.Get(ts.URL + "/") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}

	required := []string{
		`data-fixes-panel`,
		`data-fixes-endpoint="/api/cockpit?view=daily"`,
		`data-fixes-item-endpoint="/api/cockpit/item?view=daily"`,
		`data-fixes-preview-endpoint="/api/fixes/preview"`,
		`data-fixes-apply-endpoint="/api/fixes/apply"`,
		`data-fixes-open-endpoint="/api/open"`,
		`data-fixes-csrf="csrf-token"`,
		`aria-labelledby="fixes-heading"`,
		`aria-describedby="fixes-status"`,
		`aria-busy="false"`,
		`id="fixes-heading"`,
		`Daily fixes`,
		`data-fixes-status`,
		`data-fixes-loading`,
		`Loading fixes queue...`,
		`data-fixes-error`,
		`data-fixes-empty`,
		`data-fixes-warnings`,
		`data-fixes-count-active`,
		`data-fixes-count-done-hidden`,
		`data-fixes-count-snoozed-hidden`,
		`data-fixes-count-total-generated`,
		`data-fixes-list`,
	}
	for _, want := range required {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in daily fixes cockpit, got %q", want, body)
		}
	}

	fixesIdx := strings.Index(body, `data-fixes-panel`)
	overviewIdx := strings.Index(body, `data-overview-search-results`)
	historyIdx := strings.Index(body, `data-history-panel`)
	metricsIdx := strings.Index(body, `aria-label="Vault statistics"`)
	diagnosticsIdx := strings.Index(body, `id="diagnostics-heading"`)
	if fixesIdx < 0 || overviewIdx < 0 || historyIdx < 0 || metricsIdx < 0 || diagnosticsIdx < 0 {
		t.Fatalf("expected fixes, overview, history, metrics, and diagnostics sections; got %q", body)
	}
	if !(fixesIdx < overviewIdx && overviewIdx < historyIdx && historyIdx < metricsIdx && metricsIdx < diagnosticsIdx) {
		t.Fatalf(
			"unexpected home section order: fixes=%d overview=%d history=%d metrics=%d diagnostics=%d",
			fixesIdx,
			overviewIdx,
			historyIdx,
			metricsIdx,
			diagnosticsIdx,
		)
	}
}

func TestServer_IndexFixesPanelMatchesSharedLoad(t *testing.T) {
	files := map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Missing]]\n"),
		"Linked.md": []byte("---\ntitle: Linked\n---\n[[Source]]\n"),
	}
	ts, root := newHTTPServerForFilesWithOptions(t, files, nil, Options{CSRFToken: "csrf-token"})

	// Build the canonical queue independently via cockpit.BuildQueue (the
	// separate-walk path handleIndex used to take) and assert the homepage's
	// server-rendered fixes panel, now fed from the shared diagnostics load,
	// reports the same active count. A fresh vault has no suppression state, so
	// the filtered active count equals BuildQueue's.
	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")
	want, err := cockpit.BuildQueue(v, cfg)
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}
	if want.Counts.Active == 0 {
		t.Fatalf("fixture should generate hygiene tasks, got 0 active")
	}

	resp, err := http.Get(ts.URL + "/") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}

	marker := fmt.Sprintf(`<dd data-fixes-count-active>%d</dd>`, want.Counts.Active)
	if !strings.Contains(body, marker) {
		t.Fatalf("fixes panel active count from shared load; want %q in %q", marker, body)
	}
	total := fmt.Sprintf(`<dd data-fixes-count-total-generated>%d</dd>`, want.Counts.TotalGenerated)
	if !strings.Contains(body, total) {
		t.Fatalf("fixes panel total-generated count from shared load; want %q in %q", total, body)
	}
}

func TestServer_IndexRendersDiagnosticsDashboard(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Work/Source.md":    []byte("---\ntitle: Source <script>\nstatus: invalid\n---\n[[Missing <target>]]\n"),
		"Work/Twin.md":      []byte("---\ntitle: Twin\nstatus: invalid\n---\nBody\n"),
		"Archive/Twin.md":   []byte("---\ntitle: Twin\nstatus: active\n---\nBody\n"),
		"Bad <meta>.md":     []byte("---\n: bad\n---\nBody\n"),
		"Linked/Target.md":  []byte("---\ntitle: Target\nstatus: active\n---\n[[Work/Source]]\n"),
		"Linked/Source.md":  []byte("---\ntitle: Linked Source\nstatus: active\n---\n[[Work/Source]]\n"),
		"Linked/Another.md": []byte("---\ntitle: Another\nstatus: active\n---\n[[Archive/Twin]]\n"),
		// Twin appears 3x so title has a dominant sibling (>= headFactor), which
		// keeps the rare "Source <script>" title as a qualifying long-tail value
		// so the diagnostics card renders it (and we can assert it is escaped).
		"Linked/Twin.md": []byte("---\ntitle: Twin\nstatus: active\n---\n[[Work/Source]]\n"),
	}, func(cfg *config.Config) {
		cfg.Frontmatter.Schema = map[string]config.PropertySchema{
			"status": {Type: "string", Required: true, Allowed: []string{"active"}},
			"type":   {Type: "string", Required: true},
		}
	}, Options{})

	resp, err := http.Get(ts.URL + "/") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}

	required := []string{
		`id="diagnostics-heading"`,
		`Vault health`,
		`class="diagnostic-card-grid"`,
		`href="/?broken=1"`,
		`href="/?orphan=1"`,
		`href="/?dead_end=1"`,
		`href="#diagnostic-duplicate-titles"`,
		`Broken targets`,
		`Orphan notes`,
		`Dead-end notes`,
		`Duplicate titles`,
		`Frontmatter errors`,
		`Schema violations`,
		`Long-tail values`,
		`href="/broken?q=missing&#43;%3Ctarget%3E"`,
		`missing &lt;target&gt;`,
		`href="/?folder=Work"`,
		`Source &lt;script&gt;`,
		`Bad &lt;meta&gt;.md`,
		`status invalid value`,
		`allowed: active`,
	}
	for _, want := range required {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in diagnostics dashboard, got %q", want, body)
		}
	}
	if strings.Contains(body, "Source <script>") || strings.Contains(body, "Bad <meta>.md") {
		t.Fatalf("diagnostics dashboard did not escape unsafe fixture text: %q", body)
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
		writeTestFile(t, root, rel, string(data))
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

func writeTestFile(t *testing.T, root, rel, body string) {
	t.Helper()

	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(abs), err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatalf("write %q: %v", rel, err)
	}
}

func readNoteFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return string(data)
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

func postTrashRequest(t *testing.T, baseURL, key, headerToken, formToken string) *http.Response {
	t.Helper()

	form := url.Values{}
	if key != "" {
		form.Set("key", key)
	}
	if formToken != "" {
		form.Set("token", formToken)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/notes/trash", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/notes/trash: %v", err)
	}
	return resp
}

func postCreateRequest(t *testing.T, baseURL, key, target, headerToken, formToken string) *http.Response {
	t.Helper()

	form := url.Values{}
	if key != "" {
		form.Set("key", key)
	}
	form.Set("target", target)
	if formToken != "" {
		form.Set("token", formToken)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/notes/create", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/notes/create: %v", err)
	}
	return resp
}

func postRenameRequest(t *testing.T, baseURL, key, title, headerToken, formToken string) *http.Response {
	t.Helper()

	form := url.Values{}
	if key != "" {
		form.Set("key", key)
	}
	form.Set("title", title)
	if formToken != "" {
		form.Set("token", formToken)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/notes/rename", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/notes/rename: %v", err)
	}
	return resp
}

func postMoveRequest(t *testing.T, baseURL, key, folder, headerToken, formToken string) *http.Response {
	t.Helper()

	form := url.Values{}
	if key != "" {
		form.Set("key", key)
	}
	form.Set("folder", folder)
	if formToken != "" {
		form.Set("token", formToken)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/notes/move", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/notes/move: %v", err)
	}
	return resp
}

func postFrontmatterSetRequest(t *testing.T, baseURL, key, property, op, value, headerToken, formToken string) *http.Response {
	t.Helper()

	form := url.Values{}
	if key != "" {
		form.Set("key", key)
	}
	form.Set("property", property)
	if op != "" {
		form.Set("op", op)
	}
	form.Set("value", value)
	if formToken != "" {
		form.Set("token", formToken)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/notes/frontmatter/set", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/notes/frontmatter/set: %v", err)
	}
	return resp
}

func postFrontmatterUnsetRequest(t *testing.T, baseURL, key, property, headerToken, formToken string) *http.Response {
	t.Helper()

	form := url.Values{}
	if key != "" {
		form.Set("key", key)
	}
	form.Set("property", property)
	if formToken != "" {
		form.Set("token", formToken)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/notes/frontmatter/unset", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/notes/frontmatter/unset: %v", err)
	}
	return resp
}

func postBulkRequest(t *testing.T, baseURL, action string, keys []string, fields map[string]string, headerToken, formToken string) *http.Response {
	t.Helper()

	return postBulkFormRequest(t, baseURL+"/api/notes/bulk", action, keys, fields, headerToken, formToken)
}

func bulkFieldsWithPreviewID(fields map[string]string, previewID string) map[string]string {
	out := make(map[string]string, len(fields)+1)
	for name, value := range fields {
		out[name] = value
	}
	out["preview_id"] = previewID
	return out
}

func postBulkPreviewRequest(t *testing.T, baseURL, action string, keys []string, fields map[string]string, headerToken, formToken string) *http.Response {
	t.Helper()

	return postBulkFormRequest(t, baseURL+"/api/notes/bulk/preview", action, keys, fields, headerToken, formToken)
}

func postBulkFormRequest(t *testing.T, rawURL, action string, keys []string, fields map[string]string, headerToken, formToken string) *http.Response {
	t.Helper()

	form := url.Values{}
	form.Set("action", action)
	for _, key := range keys {
		form.Add("keys[]", key)
	}
	for name, value := range fields {
		form.Set(name, value)
	}
	if formToken != "" {
		form.Set("token", formToken)
	}
	req, err := http.NewRequest(http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", rawURL, err)
	}
	return resp
}

func postUndoRequest(t *testing.T, baseURL, entryID, headerToken, formToken string, force, confirmForce bool) *http.Response {
	t.Helper()

	form := url.Values{}
	if entryID != "" {
		form.Set("id", entryID)
	}
	if force {
		form.Set("force", "1")
	}
	if confirmForce {
		form.Set("confirm_force", "1")
	}
	if formToken != "" {
		form.Set("token", formToken)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/undo", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if headerToken != "" {
		req.Header.Set(csrfHeaderName, headerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/undo: %v", err)
	}
	return resp
}

type managementResultPayload struct {
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
	Key       string `json:"key"`
	Path      string `json:"path"`
	RelPath   string `json:"rel_path"`
	JournalID string `json:"journal_id"`
}

type managementErrorPayload struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type bulkManagementPayload struct {
	OK        bool                 `json:"ok"`
	Code      string               `json:"code"`
	Message   string               `json:"message"`
	Changed   int                  `json:"changed"`
	Unchanged int                  `json:"unchanged"`
	Failed    int                  `json:"failed"`
	JournalID string               `json:"journal_id"`
	Results   []bulkResultPayload  `json:"results"`
	Failures  []bulkFailurePayload `json:"failures"`
}

type bulkResultPayload struct {
	Key     string `json:"key"`
	Path    string `json:"path"`
	RelPath string `json:"rel_path"`
	Changed bool   `json:"changed"`
}

type bulkPreviewPayload struct {
	OK        bool                       `json:"ok"`
	Code      string                     `json:"code"`
	Action    string                     `json:"action"`
	Message   string                     `json:"message"`
	Changed   int                        `json:"changed"`
	Unchanged int                        `json:"unchanged"`
	Failed    int                        `json:"failed"`
	PreviewID string                     `json:"preview_id"`
	ExpiresAt string                     `json:"expires_at"`
	Results   []bulkPreviewResultPayload `json:"results"`
	Failures  []bulkFailurePayload       `json:"failures"`
}

type bulkPreviewResultPayload struct {
	Key         string                           `json:"key"`
	Path        string                           `json:"path"`
	RelPath     string                           `json:"rel_path"`
	Source      bulkPreviewPathPayload           `json:"source"`
	Destination *bulkPreviewPathPayload          `json:"destination"`
	Changed     bool                             `json:"changed"`
	Move        *bulkPreviewMovePayload          `json:"move"`
	Frontmatter []bulkPreviewFrontmatterPayload  `json:"frontmatter"`
	Affected    []bulkPreviewAffectedPathPayload `json:"affected"`
}

type bulkPreviewPathPayload struct {
	Key     string `json:"key"`
	Path    string `json:"path"`
	RelPath string `json:"rel_path"`
}

type bulkPreviewMovePayload struct {
	RewriteCount            int `json:"rewrite_count"`
	RewriteFileCount        int `json:"rewrite_file_count"`
	BodyRewriteCount        int `json:"body_rewrite_count"`
	FrontmatterRewriteCount int `json:"frontmatter_rewrite_count"`
}

type bulkPreviewValuePayload struct {
	Present bool `json:"present"`
	Value   any  `json:"value"`
}

type bulkPreviewFrontmatterPayload struct {
	Key     string                  `json:"key"`
	Before  bulkPreviewValuePayload `json:"before"`
	After   bulkPreviewValuePayload `json:"after"`
	Changed bool                    `json:"changed"`
}

type bulkPreviewAffectedPathPayload struct {
	Role    string `json:"role"`
	Key     string `json:"key"`
	Path    string `json:"path"`
	RelPath string `json:"rel_path"`
	Exists  bool   `json:"exists"`
}

type bulkFailurePayload struct {
	Key     string `json:"key"`
	RelPath string `json:"rel_path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type historyPayload struct {
	OK      bool                  `json:"ok"`
	Entries []historyEntryPayload `json:"entries"`
}

type historyEntryPayload struct {
	ID        string               `json:"id"`
	Timestamp string               `json:"timestamp"`
	Command   string               `json:"command"`
	FileCount int                  `json:"file_count"`
	Files     []historyFilePayload `json:"files"`
}

type historyFilePayload struct {
	Path   string `json:"path"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type undoPayload struct {
	OK            bool                  `json:"ok"`
	Code          string                `json:"code"`
	Message       string                `json:"message"`
	EntryID       string                `json:"entry_id"`
	Command       string                `json:"command"`
	RequiresForce bool                  `json:"requires_force"`
	Changes       []undoChangePayload   `json:"changes"`
	Conflicts     []undoConflictPayload `json:"conflicts"`
}

type undoChangePayload struct {
	Path          string `json:"path"`
	CurrentState  string `json:"current_state"`
	RestoredState string `json:"restored_state"`
}

type undoConflictPayload struct {
	Path          string `json:"path"`
	ExpectedState string `json:"expected_state"`
	CurrentState  string `json:"current_state"`
}

func decodeManagementResult(t *testing.T, body string) managementResultPayload {
	t.Helper()
	var payload managementResultPayload
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode management result: %v; body=%q", err, body)
	}
	return payload
}

func decodeManagementError(t *testing.T, body string) managementErrorPayload {
	t.Helper()
	var payload managementErrorPayload
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode management error: %v; body=%q", err, body)
	}
	return payload
}

func decodeBulkManagementResult(t *testing.T, body string) bulkManagementPayload {
	t.Helper()
	var payload bulkManagementPayload
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode bulk management result: %v; body=%q", err, body)
	}
	return payload
}

func decodeBulkPreviewResult(t *testing.T, body string) bulkPreviewPayload {
	t.Helper()
	var payload bulkPreviewPayload
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode bulk preview result: %v; body=%q", err, body)
	}
	return payload
}

func decodeHistoryResult(t *testing.T, body string) historyPayload {
	t.Helper()
	var payload historyPayload
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode history result: %v; body=%q", err, body)
	}
	return payload
}

func decodeUndoResult(t *testing.T, body string) undoPayload {
	t.Helper()
	var payload undoPayload
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode undo result: %v; body=%q", err, body)
	}
	return payload
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

func graphNodeIDs(payload graphJSON) []string {
	ids := make([]string, 0, len(payload.Nodes))
	for _, node := range payload.Nodes {
		ids = append(ids, node.Data.ID)
	}
	return ids
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
		"Source.md": []byte("---\nmeta: \"[[Target]]\"\n---\nBody [[Target]]\nhttps://www.merakialive.com\n<script>alert(1)</script>\n"),
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
	for _, want := range []string{
		`<main class="note-main">`,
		`<div class="note-content">`,
		`<div class="note-body">`,
		`href="https://www.merakialive.com"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected note HTML to contain %q, got %q", want, body)
		}
	}
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatalf("unsafe raw HTML rendered in note body, got %q", body)
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

func TestServer_NoteViewRendersPolishedNavigationAndBacklinks(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Projects/Source.md": []byte(strings.Join([]string{
			"---",
			"title: Source",
			"---",
			"# Overview",
			"",
			"## Details",
			"Body [[Ref]] [[Missing]]",
			"",
		}, "\n")),
		"Ref.md": []byte(strings.Join([]string{
			"---",
			"title: Ref",
			"related: \"See [[Source]]\"",
			"---",
			"Body <script>alert(1)</script> [[Source]]",
			"",
		}, "\n")),
	})

	resp, err := http.Get(ts.URL + "/n/projects/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /n/projects/source: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}

	for _, want := range []string{
		`<main class="note-main">`,
		`<nav class="note-breadcrumbs" aria-label="Breadcrumb">`,
		`href="/?folder=Projects"`,
		`<span aria-current="page">Source</span>`,
		`data-copy-controls`,
		`data-copy-text="[[Projects/Source]]"`,
		`data-copy-text="Projects/Source.md"`,
		`data-copy-text="/n/projects/source"`,
		`data-copy-text="[Source](/n/projects/source)"`,
		`<div class="note-layout note-layout-with-toc">`,
		`<aside class="note-meta-sidebar" aria-label="Note metadata">`,
		`<aside class="note-toc-sidebar" aria-label="Table of contents">`,
		`<div class="note-content">`,
		`<section class="panel note-toc" aria-labelledby="note-toc-heading">`,
		`href="/n/projects/source#overview"`,
		`href="/n/projects/source#details"`,
		`id="overview"`,
		`href="#overview"`,
		`class="heading-anchor"`,
		`<h2>Frontmatter</h2>`,
		`<dl class="frontmatter-rows">`,
		`<div class="frontmatter-row">`,
		`<dt>title</dt>`,
		`<dd>`,
		`<span class="frontmatter-text">Source</span>`,
		`class="backlink-occurrences"`,
		`frontmatter.related`,
		`See [[Source]]`,
		`&lt;script&gt;alert(1)&lt;/script&gt; [[Source]]`,
		`<h2>Outgoing links</h2>`,
		`href="/n/ref"`,
		`<h2>Broken links</h2>`,
		`href="/broken?q=missing"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected note HTML to contain %q, got %q", want, body)
		}
	}
	for _, fragments := range [][]string{
		{
			`<aside class="note-meta-sidebar"`,
			`<aside class="note-toc-sidebar"`,
			`<div class="note-content">`,
		},
		{
			`<div class="note-content">`,
			`<h2>Body</h2>`,
			`<h2>Broken links</h2>`,
		},
	} {
		last := -1
		for _, fragment := range fragments {
			idx := strings.Index(body, fragment)
			if idx == -1 {
				t.Fatalf("expected note HTML to contain %q, got %q", fragment, body)
			}
			if idx <= last {
				t.Fatalf("expected %q to appear after previous fragment in %v, got %q", fragment, fragments, body)
			}
			last = idx
		}
	}
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatalf("backlink snippet rendered unsafe raw HTML, got %q", body)
	}
	if strings.Contains(body, "<table>") {
		t.Fatalf("frontmatter should render as compact metadata rows, got %q", body)
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

func TestServer_NoteViewIncludesTrashControls(t *testing.T) {
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
	for _, want := range []string{
		`data-trash-note`,
		`data-trash-key="source"`,
		`data-trash-csrf="csrf-test-token"`,
		`data-trash-confirm="Move &quot;Source&quot; to Trash?"`,
		`data-trash-redirect="/"`,
		`data-trash-button`,
		`aria-label="Move this note to Trash"`,
		`data-trash-status`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing trash control fragment %q, got %q", want, body)
		}
	}
}

func TestServer_NoteViewIncludesCreateControlsForBrokenLinks(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Notes/Source.md": []byte("---\ntitle: Source\n---\n[[MiXeD Missing#Heading|Alias]]\n"),
	}, nil, Options{
		CSRFToken: "csrf-test-token",
	})

	resp, err := http.Get(ts.URL + "/n/notes/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /n/notes/source: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}
	for _, want := range []string{
		`data-create-note`,
		`data-create-source-key="notes/source"`,
		`data-create-target="MiXeD Missing"`,
		`data-create-csrf="csrf-test-token"`,
		`data-create-button`,
		`aria-label="Create missing note"`,
		`data-create-status`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing create control fragment %q, got %q", want, body)
		}
	}
}

func TestServer_NoteViewIncludesFrontmatterEditControls(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\nparent: \"[[Other]]\"\nstatus: active\ntags:\n  - alpha\n---\nBody\n"),
	}, func(cfg *config.Config) {
		cfg.Frontmatter.ListProperties = []string{"tags"}
		cfg.Frontmatter.WikilinkProperties = []string{"parent"}
		cfg.Frontmatter.Schema = map[string]config.PropertySchema{
			"status": {Type: "string", Allowed: []string{"active", "done"}},
			"tags":   {Type: "list", Allowed: []string{"alpha", "beta"}},
		}
	}, Options{
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
	for _, want := range []string{
		`data-frontmatter-panel`,
		`data-frontmatter-key="source"`,
		`data-frontmatter-csrf="csrf-test-token"`,
		`data-frontmatter-status`,
		`data-frontmatter-set data-frontmatter-property="status" data-frontmatter-op="="`,
		`data-frontmatter-unset data-frontmatter-property="status"`,
		`data-frontmatter-set data-frontmatter-property="tags" data-frontmatter-op="+="`,
		`data-frontmatter-set data-frontmatter-property="tags" data-frontmatter-op="-="`,
		`<option value="active"></option>`,
		`<option value="done"></option>`,
		`list="frontmatter-property-choices"`,
		`<option value="parent"></option>`,
		`<option value="status"></option>`,
		`<option value="tags"></option>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing frontmatter edit fragment %q, got %q", want, body)
		}
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

func TestServer_CreateAPI_BareTargetCreatesStubJournalsAndInvalidatesState(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Notes/Source.md": []byte("---\ntitle: Source\n---\nBody [[Missing Target]].\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	warmResp, err := http.Get(ts.URL + "/n/notes/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /n/notes/source: %v", err)
	}
	warmBody := readResponseBody(t, warmResp)
	if warmResp.StatusCode != http.StatusOK {
		t.Fatalf("warm status: got %d, want %d", warmResp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(warmBody, `/broken?q=missing&#43;target`) {
		t.Fatalf("warm note should show a broken-link route, got %q", warmBody)
	}

	resp := postCreateRequest(t, ts.URL, "notes/source", "Missing Target", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeManagementResult(t, body)
	if !payload.OK || payload.Message != "Created note." {
		t.Fatalf("payload = %#v, want create success", payload)
	}
	if payload.Key != "notes/missing target" || payload.Path != "/n/notes/missing%20target" || payload.RelPath != "Notes/Missing Target.md" {
		t.Fatalf("payload path fields unexpected: %#v", payload)
	}
	if strings.TrimSpace(payload.JournalID) == "" {
		t.Fatalf("journal id missing in payload: %#v", payload)
	}
	assertFile := filepath.Join(root, "Notes", "Missing Target.md")
	created := readNoteFile(t, assertFile)
	if created != "---\ntitle: Missing Target\n---\n" {
		t.Fatalf("created stub = %q, want title-only frontmatter", created)
	}
	if _, err := os.Stat(filepath.Join(root, config.DefaultJournalDir, payload.JournalID, "manifest.json")); err != nil {
		t.Fatalf("journal manifest missing: %v", err)
	}

	sourceResp, err := http.Get(ts.URL + "/n/notes/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET refreshed source note: %v", err)
	}
	sourceBody := readResponseBody(t, sourceResp)
	if sourceResp.StatusCode != http.StatusOK {
		t.Fatalf("refreshed source status: got %d, want %d", sourceResp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(sourceBody, `href="/n/notes/missing%20target"`) {
		t.Fatalf("source note did not resolve created target after refresh, got %q", sourceBody)
	}
	if strings.Contains(sourceBody, `/broken?q=missing&#43;target`) {
		t.Fatalf("source note still renders created target as broken, got %q", sourceBody)
	}
}

func TestServer_CreateAPI_CSRFRequired(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Missing]]\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	resp := postCreateRequest(t, ts.URL, "source", "Missing", "", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusForbidden, body)
	}
	payload := decodeManagementError(t, body)
	if payload.OK || payload.Code != "forbidden" {
		t.Fatalf("payload = %#v, want forbidden", payload)
	}
	if _, err := os.Stat(filepath.Join(root, "Missing.md")); !os.IsNotExist(err) {
		t.Fatalf("created file stat err = %v, want missing", err)
	}
}

func TestServer_CreateAPI_InvalidTargetReturnsBadRequest(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[../Escape]]\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	resp := postCreateRequest(t, ts.URL, "source", "../Escape", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusBadRequest, body)
	}
	payload := decodeManagementError(t, body)
	if payload.OK || payload.Code != "invalid_request" {
		t.Fatalf("payload = %#v, want invalid_request", payload)
	}
	if _, err := os.Stat(filepath.Join(root, "Escape.md")); !os.IsNotExist(err) {
		t.Fatalf("invalid target created file, stat err=%v", err)
	}
}

func TestServer_RenameAPI_RewritesWikilinksJournalsAndInvalidatesState(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nSelf [[Source]].\n"),
		"Ref.md":    []byte("---\ntitle: Ref\ncategories:\n  - \"[[Source]]\"\n---\nSee [[Source]] and [[Source|alias]].\n"),
	}, func(cfg *config.Config) {
		cfg.Frontmatter.WikilinkProperties = []string{"categories"}
	}, Options{
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

	resp := postRenameRequest(t, ts.URL, "source", "Renamed", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeManagementResult(t, body)
	if !payload.OK || payload.Message != "Renamed note." {
		t.Fatalf("payload = %#v, want rename success", payload)
	}
	if payload.Key != "renamed" || payload.Path != "/n/renamed" || payload.RelPath != "Renamed.md" {
		t.Fatalf("payload path fields unexpected: %#v", payload)
	}
	if strings.TrimSpace(payload.JournalID) == "" {
		t.Fatalf("journal id missing in payload: %#v", payload)
	}
	if _, err := os.Stat(filepath.Join(root, "Source.md")); !os.IsNotExist(err) {
		t.Fatalf("source stat err = %v, want missing", err)
	}
	renamed := readNoteFile(t, filepath.Join(root, "Renamed.md"))
	if !strings.Contains(renamed, "Self [[Renamed]].") {
		t.Fatalf("renamed note self-link not rewritten:\n%s", renamed)
	}
	ref := readNoteFile(t, filepath.Join(root, "Ref.md"))
	if strings.Count(ref, "[[Renamed]]") < 2 {
		t.Fatalf("ref body/frontmatter links not rewritten after rename:\n%s", ref)
	}
	if !strings.Contains(ref, "[[Renamed|alias]]") || strings.Contains(ref, "[[Source]]") {
		t.Fatalf("ref links unexpected after rename:\n%s", ref)
	}
	if _, err := os.Stat(filepath.Join(root, config.DefaultJournalDir, payload.JournalID, "manifest.json")); err != nil {
		t.Fatalf("journal manifest missing: %v", err)
	}

	oldResp, err := http.Get(ts.URL + "/n/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET old note: %v", err)
	}
	readResponseBody(t, oldResp)
	if oldResp.StatusCode != http.StatusNotFound {
		t.Fatalf("old route status: got %d, want %d", oldResp.StatusCode, http.StatusNotFound)
	}
	newResp, err := http.Get(ts.URL + "/n/renamed") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET renamed note: %v", err)
	}
	readResponseBody(t, newResp)
	if newResp.StatusCode != http.StatusOK {
		t.Fatalf("new route status: got %d, want %d", newResp.StatusCode, http.StatusOK)
	}
}

func TestServer_RenameAPI_ConflictDoesNotRewrite(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md":  []byte("---\ntitle: Source\n---\nBody\n"),
		"Renamed.md": []byte("---\ntitle: Renamed\n---\nExisting\n"),
		"Ref.md":     []byte("---\ntitle: Ref\n---\nSee [[Source]].\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	resp := postRenameRequest(t, ts.URL, "source", "Renamed", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusConflict, body)
	}
	payload := decodeManagementError(t, body)
	if payload.OK || payload.Code != "conflict" {
		t.Fatalf("payload = %#v, want conflict", payload)
	}
	if got := readNoteFile(t, filepath.Join(root, "Ref.md")); !strings.Contains(got, "[[Source]]") || strings.Contains(got, "[[Renamed]]") {
		t.Fatalf("reference was rewritten despite conflict:\n%s", got)
	}
	if got := readNoteFile(t, filepath.Join(root, "Renamed.md")); !strings.Contains(got, "Existing") {
		t.Fatalf("destination was overwritten despite conflict:\n%s", got)
	}
}

func TestServer_RenameAPI_CSRFRequired(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	resp := postRenameRequest(t, ts.URL, "source", "Renamed", "", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusForbidden, body)
	}
	payload := decodeManagementError(t, body)
	if payload.OK || payload.Code != "forbidden" {
		t.Fatalf("payload = %#v, want forbidden", payload)
	}
	if _, err := os.Stat(filepath.Join(root, "Source.md")); err != nil {
		t.Fatalf("source should remain: %v", err)
	}
}

func TestServer_MoveAPI_RewritesWikilinksJournalsAndInvalidatesState(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Books/Source.md": []byte("---\ntitle: Source\n---\nFolder self [[Books/Source]]. Bare [[Source]].\n"),
		"Ref.md":          []byte("---\ntitle: Ref\ncategories:\n  - \"[[Books/Source]]\"\n---\nSee [[Books/Source]] and bare [[Source]].\n"),
	}, func(cfg *config.Config) {
		cfg.Frontmatter.WikilinkProperties = []string{"categories"}
	}, Options{
		CSRFToken: "csrf-token",
	})

	warmResp, err := http.Get(ts.URL + "/n/books/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /n/books/source: %v", err)
	}
	readResponseBody(t, warmResp)
	if warmResp.StatusCode != http.StatusOK {
		t.Fatalf("warm status: got %d, want %d", warmResp.StatusCode, http.StatusOK)
	}

	resp := postMoveRequest(t, ts.URL, "books/source", "Archive", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeManagementResult(t, body)
	if !payload.OK || payload.Message != "Moved note." {
		t.Fatalf("payload = %#v, want move success", payload)
	}
	if payload.Key != "archive/source" || payload.Path != "/n/archive/source" || payload.RelPath != "Archive/Source.md" {
		t.Fatalf("payload path fields unexpected: %#v", payload)
	}
	if strings.TrimSpace(payload.JournalID) == "" {
		t.Fatalf("journal id missing in payload: %#v", payload)
	}
	if _, err := os.Stat(filepath.Join(root, "Books", "Source.md")); !os.IsNotExist(err) {
		t.Fatalf("source stat err = %v, want missing", err)
	}
	moved := readNoteFile(t, filepath.Join(root, "Archive", "Source.md"))
	if !strings.Contains(moved, "Folder self [[Archive/Source]]. Bare [[Source]].") {
		t.Fatalf("moved note links unexpected:\n%s", moved)
	}
	ref := readNoteFile(t, filepath.Join(root, "Ref.md"))
	if strings.Count(ref, "[[Archive/Source]]") < 2 {
		t.Fatalf("ref body/frontmatter links not rewritten after move:\n%s", ref)
	}
	if !strings.Contains(ref, "bare [[Source]]") || strings.Contains(ref, "[[Books/Source]]") {
		t.Fatalf("ref links unexpected after move:\n%s", ref)
	}
	if _, err := os.Stat(filepath.Join(root, config.DefaultJournalDir, payload.JournalID, "manifest.json")); err != nil {
		t.Fatalf("journal manifest missing: %v", err)
	}

	oldResp, err := http.Get(ts.URL + "/n/books/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET old note: %v", err)
	}
	readResponseBody(t, oldResp)
	if oldResp.StatusCode != http.StatusNotFound {
		t.Fatalf("old route status: got %d, want %d", oldResp.StatusCode, http.StatusNotFound)
	}
	newResp, err := http.Get(ts.URL + "/n/archive/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET moved note: %v", err)
	}
	readResponseBody(t, newResp)
	if newResp.StatusCode != http.StatusOK {
		t.Fatalf("new route status: got %d, want %d", newResp.StatusCode, http.StatusOK)
	}
}

func TestServer_MoveAPI_ConflictDoesNotRewrite(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Books/Source.md":   []byte("---\ntitle: Source\n---\nBody\n"),
		"Archive/Source.md": []byte("---\ntitle: Source\n---\nExisting\n"),
		"Ref.md":            []byte("---\ntitle: Ref\n---\nSee [[Books/Source]].\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	resp := postMoveRequest(t, ts.URL, "books/source", "Archive", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusConflict, body)
	}
	payload := decodeManagementError(t, body)
	if payload.OK || payload.Code != "conflict" {
		t.Fatalf("payload = %#v, want conflict", payload)
	}
	if got := readNoteFile(t, filepath.Join(root, "Ref.md")); !strings.Contains(got, "[[Books/Source]]") || strings.Contains(got, "[[Archive/Source]]") {
		t.Fatalf("reference was rewritten despite conflict:\n%s", got)
	}
	if got := readNoteFile(t, filepath.Join(root, "Archive", "Source.md")); !strings.Contains(got, "Existing") {
		t.Fatalf("destination was overwritten despite conflict:\n%s", got)
	}
}

func TestServer_FrontmatterAPI_SetUnsetJournalsAndInvalidatesState(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\nstatus: draft\ntitle: Source\n---\nBody marker.\n"),
	}, func(cfg *config.Config) {
		cfg.Frontmatter.Schema = map[string]config.PropertySchema{
			"status": {Type: "string", Allowed: []string{"draft", "done"}},
		}
	}, Options{
		CSRFToken: "csrf-token",
	})

	warmResp, err := http.Get(ts.URL + "/n/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /n/source: %v", err)
	}
	warmBody := readResponseBody(t, warmResp)
	if warmResp.StatusCode != http.StatusOK || !strings.Contains(warmBody, "draft") {
		t.Fatalf("warm note response unexpected: status=%d body=%q", warmResp.StatusCode, warmBody)
	}

	resp := postFrontmatterSetRequest(t, ts.URL, "source", "status", "=", "done", "csrf-token", "")
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeManagementResult(t, body)
	if !payload.OK || payload.Message != "Updated frontmatter." {
		t.Fatalf("payload = %#v, want frontmatter update", payload)
	}
	if payload.Key != "source" || payload.Path != "/n/source" || payload.RelPath != "Source.md" {
		t.Fatalf("payload path fields unexpected: %#v", payload)
	}
	if strings.TrimSpace(payload.JournalID) == "" {
		t.Fatalf("journal id missing in payload: %#v", payload)
	}
	if _, err := os.Stat(filepath.Join(root, config.DefaultJournalDir, payload.JournalID, "manifest.json")); err != nil {
		t.Fatalf("journal manifest missing: %v", err)
	}
	updated := readNoteFile(t, filepath.Join(root, "Source.md"))
	if !strings.Contains(updated, "status: done") || !strings.Contains(updated, "Body marker.") {
		t.Fatalf("set output unexpected:\n%s", updated)
	}

	refreshedResp, err := http.Get(ts.URL + "/n/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET refreshed /n/source: %v", err)
	}
	refreshedBody := readResponseBody(t, refreshedResp)
	if refreshedResp.StatusCode != http.StatusOK ||
		!strings.Contains(refreshedBody, `<span class="frontmatter-text">done</span>`) ||
		strings.Contains(refreshedBody, `<span class="frontmatter-text">draft</span>`) {
		t.Fatalf("state was not refreshed after set: status=%d body=%q", refreshedResp.StatusCode, refreshedBody)
	}

	unsetResp := postFrontmatterUnsetRequest(t, ts.URL, "source", "status", "csrf-token", "")
	unsetBody := readResponseBody(t, unsetResp)
	if unsetResp.StatusCode != http.StatusOK {
		t.Fatalf("unset status: got %d, want %d; body=%q", unsetResp.StatusCode, http.StatusOK, unsetBody)
	}
	unsetPayload := decodeManagementResult(t, unsetBody)
	if !unsetPayload.OK || unsetPayload.Message != "Unset frontmatter." || strings.TrimSpace(unsetPayload.JournalID) == "" {
		t.Fatalf("unset payload = %#v, want unset success with journal", unsetPayload)
	}
	afterUnset := readNoteFile(t, filepath.Join(root, "Source.md"))
	if strings.Contains(afterUnset, "status:") || !strings.Contains(afterUnset, "title: Source") || !strings.Contains(afterUnset, "Body marker.") {
		t.Fatalf("unset output unexpected:\n%s", afterUnset)
	}
}

func TestServer_FrontmatterAPI_ListAndWikilinkMutations(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\nparent: \"[[Old]]\"\ntags:\n  - alpha\n---\nBody marker.\n"),
	}, func(cfg *config.Config) {
		cfg.Frontmatter.ListProperties = []string{"tags"}
		cfg.Frontmatter.WikilinkProperties = []string{"parent"}
	}, Options{
		CSRFToken: "csrf-token",
	})

	for _, step := range []struct {
		prop  string
		op    string
		value string
	}{
		{prop: "tags", op: "+=", value: "beta"},
		{prop: "tags", op: "-=", value: "alpha"},
		{prop: "parent", op: "=", value: "New Parent"},
	} {
		resp := postFrontmatterSetRequest(t, ts.URL, "source", step.prop, step.op, step.value, "csrf-token", "")
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("set %s %s: got %d, want %d; body=%q", step.prop, step.op, resp.StatusCode, http.StatusOK, body)
		}
		payload := decodeManagementResult(t, body)
		if !payload.OK || strings.TrimSpace(payload.JournalID) == "" {
			t.Fatalf("payload = %#v, want success with journal", payload)
		}
	}

	got := readNoteFile(t, filepath.Join(root, "Source.md"))
	if !strings.Contains(got, "- beta") || strings.Contains(got, "- alpha") {
		t.Fatalf("list mutation output unexpected:\n%s", got)
	}
	if !strings.Contains(got, "[[New Parent]]") || strings.Contains(got, "[[Old]]") {
		t.Fatalf("wikilink mutation output unexpected:\n%s", got)
	}
	if !strings.Contains(got, "Body marker.") {
		t.Fatalf("body changed unexpectedly:\n%s", got)
	}
}

func TestServer_FrontmatterAPI_CSRFRequired(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\nstatus: draft\n---\nBody\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	resp := postFrontmatterSetRequest(t, ts.URL, "source", "status", "=", "done", "", "")
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
		cfg.Frontmatter.WikilinkProperties = []string{"parent"}
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

func TestServer_BulkAPI_TrashCollisionPreviewAndApplyKeepPlannedDestination(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"A.md":       []byte("body\n"),
		"Trash/A.md": []byte("existing trash\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	previewResp := postBulkPreviewRequest(t, ts.URL, "trash", []string{"a"}, nil, "csrf-token", "")
	previewBody := readResponseBody(t, previewResp)
	if previewResp.StatusCode != http.StatusOK {
		t.Fatalf("preview status: got %d, want %d; body=%q", previewResp.StatusCode, http.StatusOK, previewBody)
	}
	previewPayload := decodeBulkPreviewResult(t, previewBody)
	if !previewPayload.OK || previewPayload.Action != "trash" || previewPayload.Changed != 1 || previewPayload.Failed != 0 {
		t.Fatalf("preview payload = %#v, want one trashed preview", previewPayload)
	}
	if len(previewPayload.Results) != 1 || previewPayload.Results[0].Destination == nil {
		t.Fatalf("preview results = %#v, want one trash destination", previewPayload.Results)
	}
	previewResult := previewPayload.Results[0]
	previewDest := previewResult.Destination.RelPath
	if matched := regexp.MustCompile(`^Trash/A\.deleted-\d{8}T\d{6}Z-[A-Za-z0-9]+\.md$`).MatchString(previewDest); !matched {
		t.Fatalf("preview destination = %q, want deleted timestamp suffix", previewDest)
	}

	resp := postBulkRequest(t, ts.URL, "trash", []string{"a"},
		bulkFieldsWithPreviewID(nil, previewPayload.PreviewID),
		"csrf-token",
		"",
	)
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("apply status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeBulkManagementResult(t, body)
	if !payload.OK || payload.Changed != 1 || payload.Unchanged != 0 || payload.Failed != 0 || strings.TrimSpace(payload.JournalID) == "" {
		t.Fatalf("apply payload = %#v, want one changed trash result and a journal", payload)
	}
	if len(payload.Results) != 1 || payload.Results[0].RelPath != previewDest {
		t.Fatalf("apply results = %#v, want previewed destination %q", payload.Results, previewDest)
	}
	if _, err := os.Stat(filepath.Join(root, "A.md")); !os.IsNotExist(err) {
		t.Fatalf("source still exists after apply: %v", err)
	}
	if got := readNoteFile(t, filepath.Join(root, "Trash", "A.md")); got != "existing trash\n" {
		t.Fatalf("existing trash note changed:\n%s", got)
	}
	if got := readNoteFile(t, filepath.Join(root, filepath.FromSlash(previewDest))); got != "body\n" {
		t.Fatalf("planned trash destination missing content:\n%s", got)
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
	if len(manifest.Files) != 2 {
		t.Fatalf("journal files = %#v, want source and planned trash destination", manifest.Files)
	}
	if manifest.Files[0].Path != "A.md" || manifest.Files[1].Path != previewDest {
		t.Fatalf("journal files = %#v, want A.md then %q", manifest.Files, previewDest)
	}
}

func TestServer_HistoryAPI_ListsRecentJournalEntries(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\nstatus: draft\ntitle: Source\n---\nBody marker.\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	mutationResp := postFrontmatterSetRequest(t, ts.URL, "source", "status", "=", "done", "csrf-token", "")
	mutationBody := readResponseBody(t, mutationResp)
	if mutationResp.StatusCode != http.StatusOK {
		t.Fatalf("mutation status: got %d, want %d; body=%q", mutationResp.StatusCode, http.StatusOK, mutationBody)
	}
	mutationPayload := decodeManagementResult(t, mutationBody)

	resp, err := http.Get(ts.URL + "/api/history") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /api/history: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("content-type: got %q, want application/json...", got)
	}
	payload := decodeHistoryResult(t, body)
	if !payload.OK || len(payload.Entries) != 1 {
		t.Fatalf("history payload = %#v, want one entry", payload)
	}
	entry := payload.Entries[0]
	if entry.ID != mutationPayload.JournalID || entry.Command != "bn web frontmatter set" {
		t.Fatalf("entry = %#v, want mutation journal %q", entry, mutationPayload.JournalID)
	}
	if entry.FileCount != 1 || len(entry.Files) != 1 || entry.Files[0].Path != "Source.md" {
		t.Fatalf("entry files = %#v, want Source.md", entry.Files)
	}
	if entry.Files[0].Before != "file" || entry.Files[0].After != "file" || strings.TrimSpace(entry.Timestamp) == "" {
		t.Fatalf("entry state/timestamp unexpected: %#v", entry)
	}
}

func TestServer_UndoAPI_DefaultsToLatestAndInvalidatesState(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\nstatus: draft\ntitle: Source\n---\nBody marker.\n"),
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

	mutationResp := postFrontmatterSetRequest(t, ts.URL, "source", "status", "=", "done", "csrf-token", "")
	mutationBody := readResponseBody(t, mutationResp)
	if mutationResp.StatusCode != http.StatusOK {
		t.Fatalf("mutation status: got %d, want %d; body=%q", mutationResp.StatusCode, http.StatusOK, mutationBody)
	}
	mutationPayload := decodeManagementResult(t, mutationBody)
	if got := readNoteFile(t, filepath.Join(root, "Source.md")); !strings.Contains(got, "status: done") {
		t.Fatalf("mutation did not update note:\n%s", got)
	}

	resp := postUndoRequest(t, ts.URL, "", "csrf-token", "", false, false)
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	payload := decodeUndoResult(t, body)
	if !payload.OK || payload.EntryID != mutationPayload.JournalID || payload.Message != "Undo restored 1 path." {
		t.Fatalf("undo payload = %#v, want latest undo success", payload)
	}
	if len(payload.Changes) != 1 || payload.Changes[0].Path != "Source.md" {
		t.Fatalf("undo changes = %#v, want Source.md", payload.Changes)
	}
	if got := readNoteFile(t, filepath.Join(root, "Source.md")); !strings.Contains(got, "status: draft") || strings.Contains(got, "status: done") {
		t.Fatalf("undo did not restore note:\n%s", got)
	}

	refreshedResp, err := http.Get(ts.URL + "/n/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET refreshed /n/source: %v", err)
	}
	refreshedBody := readResponseBody(t, refreshedResp)
	if refreshedResp.StatusCode != http.StatusOK ||
		!strings.Contains(refreshedBody, `<span class="frontmatter-text">draft</span>`) ||
		strings.Contains(refreshedBody, `<span class="frontmatter-text">done</span>`) {
		t.Fatalf("state was not refreshed after undo: status=%d body=%q", refreshedResp.StatusCode, refreshedBody)
	}
}

func TestServer_UndoAPI_ConflictPreviewAndConfirmedForce(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\nstatus: draft\ntitle: Source\n---\nBody marker.\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	mutationResp := postFrontmatterSetRequest(t, ts.URL, "source", "status", "=", "done", "csrf-token", "")
	mutationBody := readResponseBody(t, mutationResp)
	if mutationResp.StatusCode != http.StatusOK {
		t.Fatalf("mutation status: got %d, want %d; body=%q", mutationResp.StatusCode, http.StatusOK, mutationBody)
	}
	entryID := decodeManagementResult(t, mutationBody).JournalID
	notePath := filepath.Join(root, "Source.md")
	if err := os.WriteFile(notePath, []byte("---\nstatus: blocked\ntitle: Source\n---\nBody marker.\n"), 0o644); err != nil {
		t.Fatalf("write conflicting note: %v", err)
	}

	resp := postUndoRequest(t, ts.URL, entryID, "csrf-token", "", false, false)
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("conflict status: got %d, want %d; body=%q", resp.StatusCode, http.StatusConflict, body)
	}
	payload := decodeUndoResult(t, body)
	if payload.OK || payload.Code != "conflict" || !payload.RequiresForce || payload.EntryID != entryID {
		t.Fatalf("conflict payload = %#v, want force-required conflict", payload)
	}
	if len(payload.Conflicts) != 1 || payload.Conflicts[0].Path != "Source.md" {
		t.Fatalf("conflicts = %#v, want Source.md", payload.Conflicts)
	}
	if strings.TrimSpace(payload.Conflicts[0].ExpectedState) == "" || strings.TrimSpace(payload.Conflicts[0].CurrentState) == "" {
		t.Fatalf("conflict states missing: %#v", payload.Conflicts[0])
	}
	if got := readNoteFile(t, notePath); !strings.Contains(got, "status: blocked") {
		t.Fatalf("conflict preview should not change note:\n%s", got)
	}

	unconfirmedForceResp := postUndoRequest(t, ts.URL, entryID, "csrf-token", "", true, false)
	unconfirmedForceBody := readResponseBody(t, unconfirmedForceResp)
	if unconfirmedForceResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unconfirmed force status: got %d, want %d; body=%q", unconfirmedForceResp.StatusCode, http.StatusBadRequest, unconfirmedForceBody)
	}
	if got := readNoteFile(t, notePath); !strings.Contains(got, "status: blocked") {
		t.Fatalf("unconfirmed force should not change note:\n%s", got)
	}

	forceResp := postUndoRequest(t, ts.URL, entryID, "csrf-token", "", true, true)
	forceBody := readResponseBody(t, forceResp)
	if forceResp.StatusCode != http.StatusOK {
		t.Fatalf("force status: got %d, want %d; body=%q", forceResp.StatusCode, http.StatusOK, forceBody)
	}
	forcePayload := decodeUndoResult(t, forceBody)
	if !forcePayload.OK || forcePayload.EntryID != entryID {
		t.Fatalf("force payload = %#v, want undo success", forcePayload)
	}
	if got := readNoteFile(t, notePath); !strings.Contains(got, "status: draft") || strings.Contains(got, "status: blocked") {
		t.Fatalf("force undo did not restore note:\n%s", got)
	}
}

func TestServer_UndoAPI_CSRFRequired(t *testing.T) {
	ts, root := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\nstatus: draft\ntitle: Source\n---\nBody marker.\n"),
	}, nil, Options{
		CSRFToken: "csrf-token",
	})

	mutationResp := postFrontmatterSetRequest(t, ts.URL, "source", "status", "=", "done", "csrf-token", "")
	readResponseBody(t, mutationResp)
	if mutationResp.StatusCode != http.StatusOK {
		t.Fatalf("mutation status: got %d, want %d", mutationResp.StatusCode, http.StatusOK)
	}

	resp := postUndoRequest(t, ts.URL, "", "", "", false, false)
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusForbidden, body)
	}
	payload := decodeManagementError(t, body)
	if payload.OK || payload.Code != "forbidden" {
		t.Fatalf("payload = %#v, want forbidden", payload)
	}
	if got := readNoteFile(t, filepath.Join(root, "Source.md")); !strings.Contains(got, "status: done") {
		t.Fatalf("note changed without csrf:\n%s", got)
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

func TestServer_BrokenPageIncludesCreateControls(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Notes/Source.md": []byte("---\ntitle: Source\n---\n[[MiXeD Missing]]\n"),
	}, nil, Options{
		CSRFToken: "csrf-test-token",
	})

	resp, err := http.Get(ts.URL + "/broken") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /broken: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}
	for _, want := range []string{
		`data-create-note`,
		`data-create-source-key="notes/source"`,
		`data-create-target="MiXeD Missing"`,
		`data-create-csrf="csrf-test-token"`,
		`aria-label="Create missing note"`,
		`MiXeD Missing`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing broken create fragment %q, got %q", want, body)
		}
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
	if !sameStrings(results[0].MatchedFields, []string{"title"}) {
		t.Fatalf("title result matched fields: got %#v, want [title]", results[0].MatchedFields)
	}
	if results[1].Title != "BodyOne" || results[2].Title != "BodyTwo" || results[3].Title != "Escape" {
		t.Fatalf("unexpected body-match ordering: %#v", results)
	}
	if !sameStrings(results[1].MatchedFields, []string{"body"}) {
		t.Fatalf("body result matched fields: got %#v, want [body]", results[1].MatchedFields)
	}

	for _, result := range results {
		if result.Key == "" {
			t.Fatalf("expected non-empty key in result: %#v", result)
		}
		if !strings.HasPrefix(result.Path, "/n/") {
			t.Fatalf("expected note path to start with /n/, got %#v", result)
		}
	}
	pathsByTitle := make(map[string]string, len(results))
	for _, result := range results {
		pathsByTitle[result.Title] = result.Path
	}
	if got, want := pathsByTitle["BodyTwo"], "/n/work/bodytwo"; got != want {
		t.Fatalf("nested result path: got %q, want %q; results=%#v", got, want, results)
	}
}

func TestServer_SearchAPI_FilterOnlyAndCombinedFilters(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Work/Alpha.md":     []byte("---\ntags: [foo]\nstatus: active\n---\nAlpha body.\n"),
		"Work/Needle.md":    []byte("---\ntags: [foo]\nstatus: active\n---\nNeedle body.\n"),
		"Archive/Needle.md": []byte("---\ntags: [foo]\nstatus: archived\n---\nNeedle body.\n"),
		"Work/Other.md":     []byte("---\ntags: [bar]\nstatus: active\n---\nNeedle body.\n"),
	})

	filterOnly, _, filterStatus := fetchSearchJSON(t, ts.URL+"/api/search?tag=foo&folder=Work")
	if filterStatus != http.StatusOK {
		t.Fatalf("filter-only status: got %d, want %d", filterStatus, http.StatusOK)
	}
	if len(filterOnly) != 2 {
		t.Fatalf("filter-only result count: got %d, want 2; results=%#v", len(filterOnly), filterOnly)
	}
	if filterOnly[0].Title != "Alpha" || filterOnly[1].Title != "Needle" {
		t.Fatalf("filter-only results not sorted as expected: %#v", filterOnly)
	}
	if filterOnly[0].Snippet != "" || len(filterOnly[0].MatchedFields) != 0 {
		t.Fatalf("filter-only result should not include text metadata: %#v", filterOnly[0])
	}

	combined, _, combinedStatus := fetchSearchJSON(t, ts.URL+"/api/search?q=needle&tag=foo&folder=Work&status=active")
	if combinedStatus != http.StatusOK {
		t.Fatalf("combined status: got %d, want %d", combinedStatus, http.StatusOK)
	}
	if len(combined) != 1 || combined[0].Title != "Needle" {
		t.Fatalf("combined result mismatch: %#v", combined)
	}
	if !sameStrings(combined[0].MatchedFields, []string{"title", "body"}) {
		t.Fatalf("combined matched fields: got %#v, want [title body]", combined[0].MatchedFields)
	}
}

func TestServer_SearchAPI_IssueMetadata(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Broken.md": []byte("---\ntitle: Broken\n---\nNeedle [[Missing]]\n"),
		"Linked.md": []byte("---\ntitle: Linked\n---\n[[Broken]]\n"),
	})

	results, _, statusCode := fetchSearchJSON(t, ts.URL+"/api/search?q=needle")
	if statusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", statusCode, http.StatusOK)
	}
	if len(results) != 1 {
		t.Fatalf("result count: got %d, want 1; results=%#v", len(results), results)
	}
	if !sameStrings(results[0].MatchedFields, []string{"body"}) {
		t.Fatalf("matched fields: got %#v, want [body]", results[0].MatchedFields)
	}
	if !sameStrings(results[0].Issues, []string{"broken", "dead_end"}) {
		t.Fatalf("issue metadata: got %#v, want [broken dead_end]", results[0].Issues)
	}
}

func TestServer_SearchAPI_InvalidFilterParams(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Note.md": []byte("---\ntitle: Note\n---\nBody text.\n"),
	})

	resp, err := http.Get(ts.URL + "/api/search?broken=maybe") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /api/search: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusBadRequest)
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

func TestServer_PagesIncludeFacetControls(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	})

	assertFacetUI := func(path, prefix, mode, label string) {
		t.Helper()
		resp, err := http.Get(ts.URL + path) // #nosec G107 -- test-only local URL
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status for %s: got %d, want %d", path, resp.StatusCode, http.StatusOK)
		}
		required := []string{
			`data-facet-controls`,
			`data-facet-mode="` + mode + `"`,
			`id="` + prefix + `-facet-input"`,
			`role="combobox"`,
			`aria-controls="` + prefix + `-facet-options"`,
			`id="` + prefix + `-facet-options"`,
			`role="listbox"`,
			`id="` + prefix + `-facet-chips"`,
			`data-facet-chips`,
			`data-facet-input-clear`,
			`data-facet-clear-all`,
			label,
		}
		for _, want := range required {
			if !strings.Contains(body, want) {
				t.Fatalf("missing %q on %s, got %q", want, path, body)
			}
		}
	}

	assertFacetUI("/", "overview", "search", "Filter notes")
	assertFacetUI("/graph", "graph", "graph", "Filter graph facets")
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
	if !strings.Contains(body, `id="graph-hover-issues"`) {
		t.Fatalf("missing graph issue detail on /graph, got %q", body)
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

func TestServer_GraphAPIJSON_FiltersNodesEdgesAndFilteredDegrees(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Work/Alpha.md":    []byte("---\ntitle: Alpha\ntags: [keep]\nstatus: active\n---\n[[Beta]] [[Gamma]]\n"),
		"Work/Beta.md":     []byte("---\ntitle: Beta\ntags: [keep]\nstatus: active\n---\n[[Gamma]]\n"),
		"Work/Gamma.md":    []byte("---\ntitle: Gamma\ntags: [drop]\nstatus: active\n---\nBody\n"),
		"Archive/Delta.md": []byte("---\ntitle: Delta\ntags: [keep]\nstatus: active\n---\n[[Alpha]]\n"),
	})

	payload, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json?tag=keep&folder=Work")
	if !sameStrings(graphNodeIDs(payload), []string{"work/alpha", "work/beta"}) {
		t.Fatalf("filtered node ids: got %#v, want work/alpha and work/beta", graphNodeIDs(payload))
	}
	if len(payload.Edges) != 1 {
		t.Fatalf("edge count: got %d, want 1", len(payload.Edges))
	}
	edge := payload.Edges[0].Data
	if edge.Source != "work/alpha" || edge.Target != "work/beta" {
		t.Fatalf("filtered edge: got %#v, want work/alpha->work/beta", edge)
	}

	nodes := graphNodeDataByID(payload)
	alpha := nodes["work/alpha"]
	if alpha.InDegree != 1 || alpha.OutDegree != 2 || alpha.Degree != 3 {
		t.Fatalf("alpha full degrees: got in=%d out=%d total=%d, want in=1 out=2 total=3", alpha.InDegree, alpha.OutDegree, alpha.Degree)
	}
	if alpha.FilteredInDegree != 0 || alpha.FilteredOutDegree != 1 || alpha.FilteredDegree != 1 {
		t.Fatalf("alpha filtered degrees: got in=%d out=%d total=%d, want in=0 out=1 total=1", alpha.FilteredInDegree, alpha.FilteredOutDegree, alpha.FilteredDegree)
	}
	beta := nodes["work/beta"]
	if beta.InDegree != 1 || beta.OutDegree != 1 || beta.Degree != 2 {
		t.Fatalf("beta full degrees: got in=%d out=%d total=%d, want in=1 out=1 total=2", beta.InDegree, beta.OutDegree, beta.Degree)
	}
	if beta.FilteredInDegree != 1 || beta.FilteredOutDegree != 0 || beta.FilteredDegree != 1 {
		t.Fatalf("beta filtered degrees: got in=%d out=%d total=%d, want in=1 out=0 total=1", beta.FilteredInDegree, beta.FilteredOutDegree, beta.FilteredDegree)
	}
}

func TestServer_GraphAPIJSON_IssueMetadataAndBrokenFilter(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Broken.md": []byte("---\ntitle: Broken\n---\n[[Missing]]\n"),
		"Linked.md": []byte("---\ntitle: Linked\n---\n[[Broken]]\n"),
		"Island.md": []byte("---\ntitle: Island\n---\nNo links\n"),
	})

	payload, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json")
	nodes := graphNodeDataByID(payload)

	broken := nodes["broken"]
	if !broken.HasBroken || broken.BrokenCount != 1 || broken.IsOrphan || !broken.IsDeadEnd {
		t.Fatalf("broken metadata: got %#v, want broken count 1, not orphan, dead-end", broken)
	}
	linked := nodes["linked"]
	if linked.HasBroken || linked.BrokenCount != 0 || !linked.IsOrphan || linked.IsDeadEnd {
		t.Fatalf("linked metadata: got %#v, want clean orphan with outgoing link", linked)
	}
	island := nodes["island"]
	if island.HasBroken || island.BrokenCount != 0 || !island.IsOrphan || !island.IsDeadEnd {
		t.Fatalf("island metadata: got %#v, want clean orphan dead-end", island)
	}

	brokenOnly, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json?broken=1")
	if !sameStrings(graphNodeIDs(brokenOnly), []string{"broken"}) {
		t.Fatalf("broken filter node ids: got %#v, want broken", graphNodeIDs(brokenOnly))
	}
	if len(brokenOnly.Edges) != 0 {
		t.Fatalf("broken filter edges: got %#v, want none", brokenOnly.Edges)
	}
}

func TestServer_GraphAPIJSON_InvalidFilterParams(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Note.md": []byte("---\ntitle: Note\n---\nBody\n"),
	})

	resp, err := http.Get(ts.URL + "/api/graph.json?broken=maybe") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /api/graph.json: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusBadRequest)
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
