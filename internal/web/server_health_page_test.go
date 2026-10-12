package web

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

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
