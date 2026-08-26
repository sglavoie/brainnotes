package web

import (
	"net/http"
	"strings"
	"testing"
)

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

func TestServer_PagesIncludeThemeControl(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	})

	assertThemeUI := func(path string) {
		t.Helper()
		resp, err := http.Get(ts.URL + path) // #nosec G107 -- test-only local URL
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status for %s: got %d, want %d", path, resp.StatusCode, http.StatusOK)
		}
		if !strings.Contains(body, `window.localStorage.getItem("bn.theme")`) {
			t.Fatalf("missing theme boot script on %s, got %q", path, body)
		}
		if !strings.Contains(body, `name="bn-theme"`) {
			t.Fatalf("missing theme control radio group on %s, got %q", path, body)
		}
		for _, value := range []string{"system", "light", "dark"} {
			needle := `value="` + value + `" data-theme-option`
			if !strings.Contains(body, needle) {
				t.Fatalf("missing %q theme option on %s, got %q", value, path, body)
			}
		}
	}

	assertThemeUI("/")
	assertThemeUI("/n/source")
	assertThemeUI("/broken")
	assertThemeUI("/graph")
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

	assertFacetUI("/library", "overview", "search", "Filter notes")
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
	if !strings.Contains(body, `id="graph-zoom-matches-button"`) {
		t.Fatalf("missing graph zoom to matches button on /graph, got %q", body)
	}
	if !strings.Contains(body, `id="graph-search-mode-filter"`) {
		t.Fatalf("missing graph filter search mode control on /graph, got %q", body)
	}
	if !strings.Contains(body, `name="graph-search-mode" value="filter" checked`) {
		t.Fatalf("missing checked filter search mode on /graph, got %q", body)
	}
	if !strings.Contains(body, `id="graph-search-mode-focus"`) {
		t.Fatalf("missing graph focus search mode control on /graph, got %q", body)
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

// TestServer_GraphPageIncludesQueryBar pins the graph half of the shared
// expression contract: the same hydration hooks the Library renders, so
// initQueryBar() finds a bar on this page too, placed above the facet chips
// because the expression is the outer filter.
func TestServer_GraphPageIncludesQueryBar(t *testing.T) {
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

	required := []string{
		`data-query-bar`,
		`data-query-bar-input`,
		`data-query-bar-completions`,
		`data-query-bar-status`,
		`data-query-bar-error`,
		`data-query-bar-clear`,
		`id="query-bar-input"`,
		`class="query-bar graph-query-bar"`,
	}
	for _, want := range required {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in graph query bar, got %q", want, body)
		}
	}

	queryBarAt := strings.Index(body, `data-query-bar`)
	facetsAt := strings.Index(body, `data-facet-mode="graph"`)
	if facetsAt < 0 {
		t.Fatalf("graph facet controls missing from /graph, got %q", body)
	}
	if queryBarAt > facetsAt {
		t.Fatalf("query bar renders below the graph facet controls: query bar at %d, facets at %d", queryBarAt, facetsAt)
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
	for _, asset := range []string{
		`/static/layout-base.min.js`,
		`/static/cose-base.min.js`,
		`/static/cytoscape-fcose.min.js`,
	} {
		if !strings.Contains(body, asset) {
			t.Fatalf("missing %s asset tag, got %q", asset, body)
		}
	}
	// The fcose bundle registers against the global cytoscape and is itself
	// built on cose-base -> layout-base, so the tag order is load-bearing, not
	// cosmetic. Pin the whole chain.
	orderedAssets := []string{
		`/static/cytoscape.min.js`,
		`/static/layout-base.min.js`,
		`/static/cose-base.min.js`,
		`/static/cytoscape-fcose.min.js`,
		`/static/app.js`,
	}
	for i := 1; i < len(orderedAssets); i++ {
		before, after := orderedAssets[i-1], orderedAssets[i]
		beforeAt, afterAt := strings.Index(body, before), strings.Index(body, after)
		if beforeAt > afterAt {
			t.Fatalf("graph asset load order: %s at %d must precede %s at %d", before, beforeAt, after, afterAt)
		}
	}
	if !strings.Contains(body, `id="graph-canvas"`) {
		t.Fatalf("missing graph canvas container, got %q", body)
	}
}

// TestServer_PagesIncludeSavedViewsNavContainer pins the client-side rendering
// decision: siteNav carries one static container on every page that renders it,
// and initSavedViewsNav fills it from GET /api/views. Nothing is threaded
// through a page-data struct, so a page that renders the nav needs no field.
func TestServer_PagesIncludeSavedViewsNavContainer(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	})

	for _, path := range []string{"/", "/library", "/pinned", "/graph", "/health", "/n/source"} {
		resp, err := http.Get(ts.URL + path) // #nosec G107 -- test-only local URL
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status for %s: got %d, want %d", path, resp.StatusCode, http.StatusOK)
		}
		if !strings.Contains(body, `data-saved-views-nav`) {
			t.Fatalf("missing saved-views nav container on %s, got %q", path, body)
		}

		navAt := strings.Index(body, `class="site-nav"`)
		containerAt := strings.Index(body, `data-saved-views-nav`)
		navEndAt := strings.Index(body[navAt:], "</nav>")
		if navAt < 0 || navEndAt < 0 || containerAt < navAt || containerAt > navAt+navEndAt {
			t.Fatalf("saved-views container is not inside the primary nav on %s, got %q", path, body)
		}
	}
}

// TestServer_LibraryIncludesSaveAsViewControl pins that the write half lives on
// the Library, where the CSRF token is already rendered: the nav strip itself is
// read-only and takes no token.
func TestServer_LibraryIncludesSaveAsViewControl(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody\n"),
	})

	resp, err := http.Get(ts.URL + "/library") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /library: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status for /library: got %d, want %d", resp.StatusCode, http.StatusOK)
	}

	for _, want := range []string{
		`data-saved-views-save`,
		`data-saved-views-csrf=`,
		`data-saved-views-open`,
		`Save as view`,
		`data-saved-views-form`,
		`data-saved-views-name`,
		`data-saved-views-description`,
		`data-saved-views-confirm`,
		`data-saved-views-cancel`,
		`data-saved-views-diff`,
		`data-saved-views-status`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in the library saved-views control, got %q", want, body)
		}
	}

	controlAt := strings.Index(body, `data-saved-views-save`)
	headerEndAt := strings.Index(body, "</header>")
	if headerEndAt < 0 || controlAt > headerEndAt {
		t.Fatalf("save-as-view control renders outside the library header: control at %d, header ends at %d", controlAt, headerEndAt)
	}
}
