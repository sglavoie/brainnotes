package web

import (
	"net/http"
	"strings"
	"testing"
)

func TestServer_NoteView200(t *testing.T) {
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
	for _, want := range []string{
		`<h1>Source</h1>`,
		`href="/n/target"`,
		`<main class="note-main">`,
		`data-note-back-results hidden>Back to results</a>`,
		`data-copy-note-url`,
		`data-note-sidebar-details open>`,
		`<div class="note-content">`,
		`data-note-insight`,
		`data-note-insight-endpoint="/api/note/insight"`,
		`data-note-insight-key="source"`,
		`data-note-insight-enabled="false"`,
		`data-related-notes`,
		`data-related-source-key="source"`,
		`data-related-endpoint="/api/related"`,
		`data-related-item-endpoint="/api/related/item"`,
		`data-note-insight-prompt`,
		`maxlength="2000"`,
		`<a class="note-skip-link" href="#bn:note-body">Skip to note</a>`,
		`<div id="bn:note-body" class="note-body" tabindex="-1" data-note-body data-note-context-links>`,
		`href="https://www.merakialive.com"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected note HTML to contain %q, got %q", want, body)
		}
	}
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatalf("unsafe raw HTML rendered in note body, got %q", body)
	}
	insightIndex := strings.Index(body, `data-note-insight`)
	bodyIndex := strings.Index(body, `aria-label="Note content"`)
	if insightIndex < 0 || bodyIndex < 0 || insightIndex <= bodyIndex {
		t.Fatalf("Ask AI panel must render below note body: insight=%d body=%d", insightIndex, bodyIndex)
	}
}

func TestServer_NoteViewUnknown404(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Source.md": []byte("Source body\n"),
	})
	missingResp, err := http.Get(ts.URL + "/n/does-not-exist") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET missing note: %v", err)
	}
	defer missingResp.Body.Close()
	if missingResp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want %d", missingResp.StatusCode, http.StatusNotFound)
	}
}

func TestServer_NoteViewRendersExtendedMarkdown(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Source.md": []byte(strings.Join([]string{
			"---",
			"title: Source",
			"---",
			"| Name | Link |",
			"| :--- | ---: |",
			"| Alpha | [[Target]] |",
			"",
			"- [x] done",
			"- [ ] todo",
			"",
			"~~gone~~ stays",
			"",
			"Term",
			": Definition of the term.",
			"",
			"Text with a note.[^1]",
			"",
			"[^1]: The footnote body.",
			"",
		}, "\n")),
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
	// Assert the extended Markdown constructs reach the browser as semantic HTML
	// through the production handleNote -> renderWithGraph path, and that a
	// wikilink inside a table cell resolves against the snapshot graph.
	for _, want := range []string{
		// GFM table as semantic HTML with a resolved wikilink in a cell.
		"<table>",
		"<thead>",
		"<tbody>",
		`href="/n/target"`,
		// Read-only (disabled) task-list checkboxes.
		`<input checked="" disabled="" type="checkbox">`,
		`<input disabled="" type="checkbox">`,
		// Strikethrough.
		"<del>gone</del>",
		// Definition list.
		"<dt>Term</dt>",
		"<dd>Definition of the term.</dd>",
		// Footnote reference and navigable endnote block.
		`id="fnref:1"`,
		`href="#fn:1"`,
		`class="footnotes"`,
		`id="fn:1"`,
		"The footnote body.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected extended Markdown note HTML to contain %q, got %q", want, body)
		}
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
		"Source.md": []byte("---\nmeta: \"See [[Target]] and [[Missing]]\"\nnested:\n  child: [[Target#Heading|Alias]]\n  missing: [[Missing]]\n---\nbody\n"),
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
