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
