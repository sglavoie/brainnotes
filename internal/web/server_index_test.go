package web

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
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

	resp, err := http.Get(ts.URL + "/") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}

	assertHealthMetric := func(label, want string) {
		t.Helper()
		re := regexp.MustCompile(`(?s)<span class="diagnostic-card-label">` + regexp.QuoteMeta(label) + `</span>\s*<span class="metric-value">([0-9]+)</span>`)
		match := re.FindStringSubmatch(body)
		if len(match) != 2 {
			t.Fatalf("metric %q missing in body: %q", label, body)
		}
		if match[1] != want {
			t.Fatalf("metric %q: got %q, want %q", label, match[1], want)
		}
	}

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
	assertHealthMetric("Broken targets", "1")
	assertHealthMetric("Orphans", "2")

	recentStart := strings.Index(body, "<h2>Recent notes</h2>")
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
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{})

	resp, err := http.Get(ts.URL + "/") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, `data-brief-empty-vault`) {
		t.Fatalf("empty-vault state missing on index page: %q", body)
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

// TestServer_LibraryIncludesQueryBarAboveFacets pins both halves of the query
// bar contract: the hydration hooks initQueryBar() resolves, and the ordering —
// the expression bar reads as the outer filter, so it must render above the
// facet chips rather than below them.
func TestServer_LibraryIncludesQueryBarAboveFacets(t *testing.T) {
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
	if queryBarAt > facetsAt {
		t.Fatalf("query bar renders below the facet controls: query bar at %d, facets at %d", queryBarAt, facetsAt)
	}
}

func TestServer_HealthIncludesDailyFixesCockpit(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Missing]]\n"),
		"Linked.md": []byte("---\ntitle: Linked\n---\n[[Source]]\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	resp, err := http.Get(ts.URL + "/health") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /health: %v", err)
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
	metricsIdx := strings.Index(body, `aria-label="Vault statistics"`)
	diagnosticsIdx := strings.Index(body, `id="diagnostics-heading"`)
	if fixesIdx < 0 || metricsIdx < 0 || diagnosticsIdx < 0 {
		t.Fatalf("expected fixes, metrics, and diagnostics sections; got %q", body)
	}
	if !(fixesIdx < metricsIdx && metricsIdx < diagnosticsIdx) {
		t.Fatalf("unexpected health section order: fixes=%d metrics=%d diagnostics=%d", fixesIdx, metricsIdx, diagnosticsIdx)
	}
}

func TestServer_HealthFixesPanelMatchesSharedLoad(t *testing.T) {
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

	resp, err := http.Get(ts.URL + "/health") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /health: %v", err)
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

func TestServer_HealthRendersDiagnosticsDashboard(t *testing.T) {
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
		cfg.Frontmatter.Properties = map[string]config.PropertyDefinition{
			"status": {Type: config.KindString, Required: true, Allowed: []string{"active"}},
			"type":   {Type: config.KindString, Required: true},
		}
	}, Options{})

	resp, err := http.Get(ts.URL + "/health") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}

	required := []string{
		`id="diagnostics-heading"`,
		`Vault health`,
		`class="diagnostic-card-grid"`,
		`href="/health#diagnostic-broken-targets"`,
		`href="/health#diagnostic-orphans"`,
		`href="/health#diagnostic-dead-ends"`,
		`href="/health#diagnostic-duplicate-titles"`,
		`Broken targets`,
		`Orphan notes`,
		`Dead-end notes`,
		`Duplicate titles`,
		`Frontmatter errors`,
		`Schema violations`,
		`Long-tail values`,
		`href="/broken?q=missing&#43;%3Ctarget%3E"`,
		`missing &lt;target&gt;`,
		`href="/library?folder=Work"`,
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
