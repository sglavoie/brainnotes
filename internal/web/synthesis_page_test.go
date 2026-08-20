package web

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func getSynthesizePage(t *testing.T, baseURL, query string) (int, string) {
	t.Helper()

	resp, err := http.Get(baseURL + synthesizeRoutePath + query) // #nosec G107 -- test-only local listener URL
	if err != nil {
		t.Fatalf("GET %s%s: %v", synthesizeRoutePath, query, err)
	}
	return resp.StatusCode, readResponseBody(t, resp)
}

func TestSynthesizePage_RendersAnchorAndDestinationDefault(t *testing.T) {
	ts, srv, _ := newSynthesisServer(t, Options{})

	status, body := getSynthesizePage(t, ts.URL, "?source="+synthesisAnchorKey)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}

	for _, want := range []string{
		`data-synthesize-root`,
		`data-synthesize-anchor-key="` + synthesisAnchorKey + `"`,
		`data-synthesize-anchor-path="Notes/Anchor.md"`,
		`data-synthesize-default-folder="Notes"`,
		`data-synthesize-default-purpose="synthesis"`,
		`data-synthesize-max-supporters="10"`,
		`data-synthesize-context-endpoint="` + synthesisContextAPIRoutePath + `"`,
		`data-synthesize-generate-endpoint="` + synthesisGenerateAPIRoutePath + `"`,
		`data-synthesize-preview-endpoint="` + synthesisPreviewAPIRoutePath + `"`,
		`data-synthesize-apply-endpoint="` + synthesisApplyAPIRoutePath + `"`,
		`data-synthesize-csrf="` + srv.csrfToken + `"`,
		`data-synthesize-vault="` + srv.vaultID + `"`,
		`href="/n/notes/anchor"`,
		`>Anchor<`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("page is missing %q", want)
		}
	}
}

// TestSynthesizePage_RootAnchorFolderDefault covers the vault-root anchor, whose
// destination default is the root rather than a named folder.
func TestSynthesizePage_RootAnchorFolderDefault(t *testing.T) {
	files := map[string]string{
		"Root.md": "---\ntitle: Root\n---\nRoot body paragraph.\n",
	}
	ts, _, _ := newSynthesisServerWithConfig(t, synthesisTestConfigTOML, files, Options{})

	status, body := getSynthesizePage(t, ts.URL, "?source=root")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if !strings.Contains(body, `data-synthesize-default-folder=""`) {
		t.Fatal("root anchor did not render an empty destination folder default")
	}
}

func TestSynthesizePage_RequiresExactlyOneSource(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})

	for name, query := range map[string]string{
		"missing":   "",
		"empty":     "?source=",
		"blank":     "?source=%20",
		"duplicate": "?source=" + synthesisAnchorKey + "&source=" + synthesisAlphaKey,
	} {
		t.Run(name, func(t *testing.T) {
			status, _ := getSynthesizePage(t, ts.URL, query)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", status)
			}
		})
	}
}

func TestSynthesizePage_UnknownAnchorIsNotFound(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})

	status, _ := getSynthesizePage(t, ts.URL, "?source=notes/missing")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
}

func TestSynthesizePage_RejectsNonGET(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})

	resp, err := http.Post(ts.URL+synthesizeRoutePath+"?source="+synthesisAnchorKey, "text/plain", strings.NewReader(""))
	if err != nil {
		t.Fatalf("POST %s: %v", synthesizeRoutePath, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
	if got := resp.Header.Get("Allow"); got != http.MethodGet {
		t.Fatalf("Allow = %q, want GET", got)
	}
}

func TestSynthesizePage_SubpathIsNotFound(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})

	status, _ := getSynthesizePage(t, ts.URL, "/extra?source="+synthesisAnchorKey)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
}

// TestSynthesizePage_SharesPageChrome keeps the workbench on the same theme,
// search, and navigation contract as every other server-rendered page.
func TestSynthesizePage_SharesPageChrome(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})

	_, body := getSynthesizePage(t, ts.URL, "?source="+synthesisAnchorKey)
	for _, want := range []string{
		`<link rel="stylesheet" href="/static/app.css">`,
		`window.localStorage.getItem("bn.theme")`,
		`data-theme-control`,
		`data-search-box`,
		`class="site-nav"`,
		`href="/library"`,
		`href="/pinned"`,
		`href="/properties"`,
		`href="/graph"`,
		`href="/health"`,
		`<script src="/static/app.js" defer></script>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("page is missing shared chrome %q", want)
		}
	}
}

// TestSynthesizePage_ExplainsItselfWithoutJavaScript covers the pre-enhancement
// state: the page must say why it is inert rather than render dead controls.
func TestSynthesizePage_ExplainsItselfWithoutJavaScript(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})

	_, body := getSynthesizePage(t, ts.URL, "?source="+synthesisAnchorKey)
	if !strings.Contains(body, "<noscript>") {
		t.Fatal("page has no noscript fallback")
	}
	if !strings.Contains(body, "data-synthesize-fallback") {
		t.Fatal("page has no scriptless explanation to replace")
	}
	if strings.Count(body, "needs JavaScript enabled")+strings.Count(body, "JavaScript is disabled") != 2 {
		t.Fatal("page does not explain the JavaScript requirement in both states")
	}
	if !strings.Contains(body, `href="/library"`) {
		t.Fatal("scriptless fallback offers no way forward")
	}
}

func TestSynthesizePage_AgentStatusFollowsConfiguration(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})
	_, body := getSynthesizePage(t, ts.URL, "?source="+synthesisAnchorKey)
	if !strings.Contains(body, `data-synthesize-agent="disabled"`) {
		t.Fatal("unconfigured agent did not render as disabled")
	}
	if !strings.Contains(body, "[agent].command") {
		t.Fatal("unconfigured agent rendered no explanation")
	}

	agentTS, _, _ := newSynthesisServerWithConfig(
		t,
		synthesisTestConfigTOML+"\n[agent]\ncommand = [\"echo\", \"hi\"]\n",
		synthesisTestNotes(),
		Options{},
	)
	_, agentBody := getSynthesizePage(t, agentTS.URL, "?source="+synthesisAnchorKey)
	if !strings.Contains(agentBody, `data-synthesize-agent="enabled"`) {
		t.Fatal("configured agent did not render as enabled")
	}
}

// launchLinkSource returns the note key a browser would actually send when it
// follows the launch link, so the assertions survive both the query escaping
// the handler applies and the HTML escaping the template applies on top of it.
func launchLinkSource(t *testing.T, body string) string {
	t.Helper()

	matches := launchLinkPattern.FindStringSubmatch(body)
	if matches == nil {
		t.Fatal("note page is missing the launch link")
	}
	parsed, err := url.Parse(html.UnescapeString(matches[1]))
	if err != nil {
		t.Fatalf("parse launch link %q: %v", matches[1], err)
	}
	if parsed.Path != synthesizeRoutePath {
		t.Fatalf("launch link path = %q, want %q", parsed.Path, synthesizeRoutePath)
	}
	sources := parsed.Query()["source"]
	if len(sources) != 1 {
		t.Fatalf("launch link carries %d source keys, want exactly 1", len(sources))
	}
	return sources[0]
}

var launchLinkPattern = regexp.MustCompile(`href="([^"]+)"[^>]*data-synthesize-launch`)

func TestNotePage_LinksToSynthesisWorkbench(t *testing.T) {
	ts, _, _ := newSynthesisServer(t, Options{})

	resp, err := http.Get(ts.URL + "/n/" + synthesisAnchorKey) // #nosec G107 -- test-only local listener URL
	if err != nil {
		t.Fatalf("GET note: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := launchLinkSource(t, body); got != synthesisAnchorKey {
		t.Fatalf("launch link source = %q, want %q", got, synthesisAnchorKey)
	}
}

// TestNotePage_LaunchLinkEscapesKey covers a note whose key contains characters
// the query string would otherwise reinterpret.
func TestNotePage_LaunchLinkEscapesKey(t *testing.T) {
	files := map[string]string{
		"Notes/A & B.md": "---\ntitle: A & B\n---\nBody paragraph about ampersands.\n",
	}
	ts, _, _ := newSynthesisServerWithConfig(t, synthesisTestConfigTOML, files, Options{})

	resp, err := http.Get(ts.URL + "/n/notes/a%20&%20b") // #nosec G107 -- test-only local listener URL
	if err != nil {
		t.Fatalf("GET note: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := launchLinkSource(t, body); got != "notes/a & b" {
		t.Fatalf("launch link source = %q, want %q", got, "notes/a & b")
	}
	if strings.Contains(body, synthesizeRoutePath+"?source=notes/a & b") {
		t.Fatal("launch link carries an unescaped note key")
	}
}
