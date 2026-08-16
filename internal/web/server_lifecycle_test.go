package web

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

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
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}
}

// TestServer_EndToEnd_EndpointsStayResponsiveWithStalledNote is the session-3
// regression for the resilient loader: with one note read stalled forever, the
// homepage, facets, search, graph JSON, and the daily cockpit must each return
// after a single bounded wait and consistently omit the stalled note from every
// loaded-content surface. A regression to an unbounded full-vault load would
// hang the first of these requests forever (the loader never unblocks).
func TestServer_EndToEnd_EndpointsStayResponsiveWithStalledNote(t *testing.T) {
	ts, root, callCount := newBlockedLoaderServer(t, map[string][]byte{
		"Source.md":      []byte("---\ntitle: Source\ntags: [healthytag]\n---\n[[Target]] [[Missing]]\n"),
		"Target.md":      []byte("---\ntitle: Target\ntags: [healthytag]\n---\nNo links.\n"),
		"StalledNote.md": []byte("---\ntitle: StalledNote\ntags: [stalledtag]\n---\n[[GhostStall]]\n"),
	}, "StalledNote.md", nil, Options{CSRFToken: "csrf-token"})
	blockedPath := filepath.Join(root, "StalledNote.md")

	// Each endpoint must complete well within a bound; the stalled read is 50ms
	// and only happens once, so a generous ceiling still proves boundedness while
	// a true unbounded-load regression would never return.
	get := func(path string) string {
		t.Helper()
		start := time.Now()
		resp, err := http.Get(ts.URL + path) // #nosec G107 -- test-only local URL
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200; body=%q", path, resp.StatusCode, body)
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("GET %s took %s, want a bounded response", path, elapsed)
		}
		return body
	}

	// The stalled note must never leak into any loaded-content payload.
	assertOmitsStalled := func(path, body string) {
		t.Helper()
		for _, needle := range []string{"StalledNote", "stalledtag", "GhostStall"} {
			if strings.Contains(body, needle) {
				t.Fatalf("GET %s leaked stalled-note content %q: %q", path, needle, body)
			}
		}
	}

	index := get("/")
	health := get("/health")
	// The health page surfaces the stalled path only through the skipped-notes
	// diagnostic; it must never be reachable as a loaded/recent note link.
	if !strings.Contains(health, `id="diagnostic-skipped-notes"`) {
		t.Fatalf("health page missing skipped-notes section: %q", health)
	}
	if !strings.Contains(health, "StalledNote.md") {
		t.Fatalf("health page skipped-notes section did not list the stalled path: %q", health)
	}
	if strings.Contains(index, `href="/n/stallednote`) || strings.Contains(health, `href="/n/stallednote`) {
		t.Fatalf("stalled note leaked into a loaded-content route: %q", index)
	}

	facets := get("/api/facets")
	if !strings.Contains(facets, "healthytag") {
		t.Fatalf("facets dropped healthy content: %q", facets)
	}
	assertOmitsStalled("/api/facets", facets)

	search := get("/api/search?tag=healthytag")
	if !strings.Contains(search, "Source") || !strings.Contains(search, "Target") {
		t.Fatalf("search dropped healthy notes: %q", search)
	}
	assertOmitsStalled("/api/search", search)

	// A tag carried only by the stalled note must resolve to nothing.
	if stalledSearch := get("/api/search?tag=stalledtag"); strings.TrimSpace(stalledSearch) != "[]" {
		t.Fatalf("search on the stalled note's tag returned results: %q", stalledSearch)
	}

	graph := get("/api/graph.json")
	if !strings.Contains(graph, "Source") {
		t.Fatalf("graph dropped healthy notes: %q", graph)
	}
	assertOmitsStalled("/api/graph.json", graph)

	cockpit := get("/api/cockpit?view=daily")
	if !strings.Contains(cockpit, "missing") {
		t.Fatalf("cockpit dropped the healthy broken-target task: %q", cockpit)
	}
	assertOmitsStalled("/api/cockpit?view=daily", cockpit)

	// One bounded wait, shared across every endpoint: the stalled note is read
	// exactly once and quarantined for the rest of the process, so no later
	// endpoint re-attempts (and re-blocks on) the stalled read.
	if got := callCount(blockedPath); got != 1 {
		t.Fatalf("stalled note read %d times across the endpoint sweep, want exactly 1", got)
	}
}
