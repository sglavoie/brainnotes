package web

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
)

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
	if !strings.Contains(string(body), "<h1>Daily Brief</h1>") {
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

	body := readWorkspacePage(t, ts.URL+"/")
	assertBriefHomepage(t, body)
	health := readWorkspacePage(t, ts.URL+"/health")
	assertHealthMetric(t, health, "Broken targets", "1")
	assertHealthMetric(t, health, "Orphans", "2")
	assertRecentNoteOrdering(t, readWorkspacePage(t, ts.URL+"/library"))
}

func assertBriefHomepage(t *testing.T, body string) {
	t.Helper()

	required := []string{
		`data-brief-root`,
		`data-brief-view-endpoint="/api/brief/view"`,
		`data-brief-item-endpoint="/api/brief/item"`,
		`data-brief-insight-endpoint="/api/brief/insight"`,
		`data-brief-action-open`,
		`data-brief-action-another`,
		`data-brief-action-insight`,
		`data-brief-insight-prompt`,
		`maxlength="2000"`,
		`Most recently modified substantive note.`,
		`Longest-unseen eligible note.`,
	}
	for _, want := range required {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in brief homepage, got %q", want, body)
		}
	}
	if strings.Contains(body, `data-fixes-panel`) ||
		strings.Contains(body, `data-overview-search-results`) ||
		strings.Contains(body, `data-history-panel`) ||
		strings.Contains(body, `id="diagnostics-heading"`) {
		t.Fatalf("homepage leaked maintenance dashboard sections: %q", body)
	}
}

func assertRecentNoteOrdering(t *testing.T, body string) {
	t.Helper()
	recentStart := strings.Index(body, "<h2>Recently modified</h2>")
	if recentStart < 0 {
		t.Fatalf("recent notes section missing, got %q", body)
	}
	recentBody := body[recentStart:]
	newestIdx := strings.Index(recentBody, `href="/n/newest"`)
	middleIdx := strings.Index(recentBody, `href="/n/middle"`)
	oldestIdx := strings.Index(recentBody, `href="/n/oldest"`)
	if newestIdx < 0 || middleIdx < 0 || oldestIdx < 0 {
		t.Fatalf("recent note links missing, got %q", recentBody)
	}
	if !(newestIdx < middleIdx && middleIdx < oldestIdx) {
		t.Fatalf("recent notes not sorted by recency; indexes newest=%d middle=%d oldest=%d", newestIdx, middleIdx, oldestIdx)
	}
}

func TestServer_IndexRendersDegradedBriefStates(t *testing.T) {
	t.Setenv("EDITOR", "")
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"SubstantiveMissing.md": []byte("---\ntitle: Substantive Missing\n---\n"),
		"Bad.md":                []byte("---\n: bad\n---\nBody\n"),
		".bnotes/brief.json":    []byte("{invalid json"),
	}, func(cfg *config.Config) {
		cfg.Open.Editor = ""
		cfg.Agent.Command = nil
	}, Options{CSRFToken: "csrf-token"})

	resp, err := http.Get(ts.URL + "/") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}

	required := []string{
		`data-brief-state-unreadable`,
		`data-brief-partial-state`,
		`data-brief-editor-unconfigured`,
		`data-brief-agent-unconfigured`,
		`data-brief-slot-empty="continue"`,
		`No substantive recent note`,
		`data-brief-slot-empty="connect"`,
		`No valid connection`,
	}
	for _, want := range required {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in degraded brief states, got %q", want, body)
		}
	}
}

func TestServer_IndexRendersEmptyVaultState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		capture bool
		opts    Options
		want    string
	}{
		{"capture", true, Options{}, `href="/new">Create your first note</a>`},
		{"unconfigured", false, Options{}, "Create your first note"},
		{"hosted", true, Options{Hosted: true}, `href="/new">Create your first note</a>`},
		{"hosted-unconfigured", false, Options{Hosted: true}, "Configure capture"},
		{"replica", true, Options{ReadOnly: true, StateDir: t.TempDir()}, "Add a Markdown note to the source vault and sync it to this replica"},
		{"replica-unconfigured", false, Options{ReadOnly: true, StateDir: t.TempDir()}, "Add a Markdown note to the source vault and sync it to this replica"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertEmptyVaultGuidance(t, tc.capture, tc.opts, tc.want)

		})
	}
}

func assertEmptyVaultGuidance(t *testing.T, capture bool, opts Options, want string) {
	t.Helper()
	ts, _ := newHTTPServerForFilesWithOptions(t, nil, func(cfg *config.Config) {
		cfg.Capture.InboxNote, cfg.Capture.Heading = "", ""
		if capture {
			cfg.Capture.InboxNote, cfg.Capture.Heading = "Inbox.md", "## Captures"
		}
	}, opts)
	resp, err := http.Get(ts.URL + "/") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, `data-brief-empty-vault`) || !strings.Contains(body, want) {
		t.Fatalf("empty-vault guidance missing %q: %q", want, body)
	}
	if strings.Contains(body, "Create your first note in") {
		t.Fatal("empty-vault guidance still points to Library")
	}
	setupLink := strings.Contains(body, `href="/settings#setting-capture-inbox_note"`)
	if setupLink != (!capture && !opts.ReadOnly) {
		t.Fatalf("capture setup link present = %v", setupLink)
	}
}

func TestServer_LibraryIncludesOverviewSearchResultsPanel(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	})

	resp, err := http.Get(ts.URL + "/library") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /library: %v", err)
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

// TestServer_LibraryGroupsSearchAndOptionalExpression pins both halves of the query
// bar contract: the hydration hooks initQueryBar() resolves, and the ordering —
// the expression bar reads as the outer filter, so it must render above the
// facet chips rather than below them.
func TestServer_LibraryGroupsSearchAndOptionalExpression(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	})

	resp, err := http.Get(ts.URL + "/library") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /library: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}

	required := []string{
		`data-query-bar`,
		`data-query-bar-input`,
		`data-query-bar-completions`,
		`data-query-bar-status`,
		`data-query-bar-error`,
		`data-query-bar-error-message`,
		`data-query-bar-error-query`,
		`data-query-bar-error-caret`,
		`data-query-bar-error-suggestions`,
		`data-query-bar-clear`,
		`id="query-bar-input"`,
		`role="combobox"`,
		`aria-autocomplete="list"`,
		`aria-expanded="false"`,
		`aria-invalid="false"`,
		`aria-controls="query-bar-completions"`,
		`id="query-bar-completions"`,
		`role="listbox"`,
	}
	for _, want := range required {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in query bar panel, got %q", want, body)
		}
	}

	queryBarAt := strings.Index(body, `data-query-bar`)
	facetsAt := strings.Index(body, `data-facet-mode="search"`)
	if facetsAt < 0 {
		t.Fatalf("facet controls missing from library page, got %q", body)
	}
	searchAt := strings.Index(body, `data-library-search`)
	disclosureAt := strings.Index(body, `data-library-expression`)
	if searchAt < 0 || facetsAt < searchAt || disclosureAt < facetsAt || queryBarAt < disclosureAt {
		t.Fatal("Library must group search and facets before the optional expression")
	}
}

func assertHealthMetric(t *testing.T, body, label, want string) {
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

func readWorkspacePage(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatal(err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d %s", url, resp.StatusCode, body)
	}
	return body
}
