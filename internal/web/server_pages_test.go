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
		if !strings.Contains(body, `placeholder="Search title or body…"`) {
			t.Fatalf("missing search placeholder on %s, got %q", path, body)
		}
		if !strings.Contains(body, `id="search-results"`) {
			t.Fatalf("missing search result list on %s, got %q", path, body)
		}
		if !strings.Contains(body, `id="search-feedback"`) || !strings.Contains(body, `aria-live="polite"`) {
			t.Fatalf("missing accessible search feedback on %s, got %q", path, body)
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
	assertFacetUI("/graph", "graph", "graph", "Filter graph notes")
}

// graphPageMarkup lists what /graph must render, each control by the markup
// that proves it, so a failure names the missing control.
var graphPageMarkup = []struct{ what, markup string }{
	{"graph filter input", `id="graph-filter-input"`},
	{"graph filter label", `Filter graph`},
	{"graph filter placeholder", `placeholder="Filter graph by title or body…"`},
	{"graph spread slider", `id="graph-spread-input"`},
	{"graph spread min", `min="0.8"`},
	{"graph spread max", `max="6.0"`},
	{"graph spread step", `step="0.1"`},
	{"graph spread default value", `value="2.0"`},
	{"graph spread value text", `id="graph-spread-value"`},
	{"default spread multiplier", `>2.0x</output>`},
	{"graph fit button", `id="graph-fit-button"`},
	{"graph reset layout button", `id="graph-reset-layout-button"`},
	{"graph zoom to matches button", `id="graph-zoom-matches-button"`},
	{"graph filter search mode control", `id="graph-search-mode-filter"`},
	{"checked filter search mode", `name="graph-search-mode" value="filter" checked`},
	{"graph focus search mode control", `id="graph-search-mode-focus"`},
	{"graph auto label control", `id="graph-label-mode-auto"`},
	{"checked auto label mode", `name="graph-label-mode" value="auto" checked`},
	{"graph all label control", `id="graph-label-mode-all"`},
	{"graph open note link", `id="graph-open-note"`},
	{"graph degree detail", `id="graph-hover-degree"`},
	{"graph issue detail", `id="graph-hover-issues"`},
	{"initially disabled graph open note link", `aria-disabled="true"`},
	{"focusable graph canvas", `id="graph-canvas" aria-label="Notes graph" tabindex="0"`},
	{"app.js asset", `/static/app.js`},
}

// graphPageAbsentMarkup is the shared search chrome /graph replaces with its
// own filter.
var graphPageAbsentMarkup = []struct{ what, markup string }{
	{"shared search input", `id="search-input"`},
	{"shared search result list", `id="search-results"`},
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
	for _, want := range graphPageMarkup {
		if !strings.Contains(body, want.markup) {
			t.Fatalf("missing %s on /graph, got %q", want.what, body)
		}
	}
	for _, absent := range graphPageAbsentMarkup {
		if strings.Contains(body, absent.markup) {
			t.Fatalf("unexpected %s on /graph, got %q", absent.what, body)
		}
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

// TestServer_GraphPageViewControlsMarkup pins which display knobs the rendered
// page offers. Isolation is fixed at one hop, so "Isolate selection" takes no
// radius: the Hops control is gone from the template, and markup left behind
// would be a dead input the controller no longer reads or writes — visible,
// draggable, and silently inert.
func TestServer_GraphPageViewControlsMarkup(t *testing.T) {
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

	for _, want := range []string{
		`id="graph-isolate-button"`,
		`id="graph-isolate-clear-button"`,
		`id="graph-mindegree-input"`,
		`id="graph-mindegree-value"`,
		`id="graph-collapse-input"`,
		`id="graph-collapse-value"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("graph view controls missing %q, got %q", want, body)
		}
	}

	for _, gone := range []string{
		`graph-hops-input`,
		`graph-hops-value`,
		`graph-hops`,
		`>Hops<`,
	} {
		if strings.Contains(body, gone) {
			t.Fatalf("graph page still renders removed Hops markup %q, got %q", gone, body)
		}
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

		navAt := strings.Index(body, `class="saved-views-strip"`)
		containerAt := strings.Index(body, `data-saved-views-nav`)
		navEndAt := strings.Index(body[navAt:], "</nav>")
		if navAt < 0 || navEndAt < 0 || containerAt < navAt || containerAt > navAt+navEndAt {
			t.Fatalf("saved-views container is not inside its separate navigation on %s, got %q", path, body)
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
	searchAt := strings.Index(body, `data-library-search`)
	resultsAt := strings.Index(body, `data-overview-search-results`)
	if searchAt < 0 || controlAt < searchAt || controlAt > resultsAt {
		t.Fatal("save-as-view control must be inside the Library search region")
	}
}
