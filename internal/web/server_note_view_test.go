package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

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
		`<div class="note-body" data-note-body>`,
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
	bodyIndex := strings.Index(body, `<h2>Body</h2>`)
	if insightIndex < 0 || bodyIndex < 0 || insightIndex <= bodyIndex {
		t.Fatalf("Ask AI panel must render below note body: insight=%d body=%d", insightIndex, bodyIndex)
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
		`href="/library?folder=Projects"`,
		`<span aria-current="page">Source</span>`,
		`data-copy-controls`,
		`data-copy-text="[[Projects/Source]]"`,
		`data-copy-text="Projects/Source.md"`,
		`data-copy-text="/n/projects/source"`,
		`data-copy-text="[Source](/n/projects/source)"`,
		`<div class="note-layout-toolbar">`,
		`<button type="button" class="note-sidebar-toggle" data-note-sidebar-toggle aria-controls="note-metadata-sidebar" aria-expanded="true" aria-label="Hide sidebar" title="Hide sidebar">`,
		`<svg class="note-sidebar-toggle-icon" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false">`,
		`<rect class="note-sidebar-toggle-panel" x="0.75" y="2.75" width="5" height="10.5" fill="currentColor" />`,
		`<div class="note-layout note-layout-with-toc" data-note-layout>`,
		`<aside id="note-metadata-sidebar" class="note-meta-sidebar" aria-label="Note metadata">`,
		`<div class="note-meta-resize-handle" data-note-meta-resize role="separator" aria-orientation="vertical" aria-label="Resize metadata sidebar"`,
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
			`<aside id="note-metadata-sidebar" class="note-meta-sidebar"`,
			`<aside class="note-toc-sidebar"`,
			`<div class="note-content">`,
		},
		{
			`<div class="note-content">`,
			`<h2>Body</h2>`,
			`<h2>Related Notes</h2>`,
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

func TestServer_NoteViewRelatedMountEscapesDataAttributes(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Folder/Source.md": []byte("---\ntitle: Source\n---\nBody [[Missing]]\n"),
	}, nil, Options{CSRFToken: `csrf-"<&`})

	resp, err := http.Get(ts.URL + "/n/folder/source") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET note: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}
	for _, want := range []string{
		`data-related-source-key="folder/source"`,
		`data-related-endpoint="/api/related"`,
		`data-related-item-endpoint="/api/related/item"`,
		`data-related-csrf="csrf-&#34;&lt;&amp;"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("related mount missing escaped fragment %q: %s", want, body)
		}
	}
	bodyIndex := strings.Index(body, `<h2>Body</h2>`)
	relatedIndex := strings.Index(body, `<h2>Related Notes</h2>`)
	brokenIndex := strings.Index(body, `<h2>Broken links</h2>`)
	if bodyIndex < 0 || relatedIndex <= bodyIndex || brokenIndex <= relatedIndex {
		t.Fatalf("mount order body/related/broken = %d/%d/%d", bodyIndex, relatedIndex, brokenIndex)
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
		cfg.Frontmatter.Properties = map[string]config.PropertyDefinition{
			"parent": {Type: config.KindWikilink},
			"status": {Type: config.KindString, Allowed: []string{"active", "done"}},
			"tags":   {Type: config.KindList, Items: config.KindString, Allowed: []string{"alpha", "beta"}},
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
