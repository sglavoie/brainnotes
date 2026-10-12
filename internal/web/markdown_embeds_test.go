package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMarkdownRenderer_MediaEmbedsUseMediaRouteWhenFileExists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "image.png"), []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	_, v := testVaultOpen(t, root, "127.0.0.1:0")

	state, err := newVaultState(v, time.Second)
	if err != nil {
		t.Fatalf("newVaultState: %v", err)
	}
	renderer, err := newMarkdownRenderer(state)
	if err != nil {
		t.Fatalf("newMarkdownRenderer: %v", err)
	}

	out, err := renderer.render("![[image.png]] ![[missing.png]]")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	html := string(out)
	if !strings.Contains(html, `/media/image.png`) {
		t.Fatalf("expected media route for existing embed, got %q", html)
	}
	if !strings.Contains(html, `missing.png`) {
		t.Fatalf("expected missing media embed to remain plain text, got %q", html)
	}
	if strings.Contains(html, `/broken?q=missing.png`) {
		t.Fatalf("expected missing media embed to avoid broken-link routing, got %q", html)
	}
}

func TestMarkdownRenderer_RendersSemanticTableWithResolvedWikilinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	notePath := filepath.Join(root, "Project Note.md")
	if err := os.WriteFile(notePath, []byte("---\ntitle: Project Note\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	renderer := newTestMarkdownRenderer(t, root)

	source := strings.Join([]string{
		"| Name | Link |",
		"| :--- | ---: |",
		"| Alpha | [[Project Note]] |",
	}, "\n")
	out, err := renderer.render(source)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	html := string(out)
	for _, want := range []string{
		"<table>",
		"<thead>",
		"<th",
		"<tbody>",
		"<td",
		`href="/n/project%20note"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected table HTML to contain %q, got %q", want, html)
		}
	}
}

func TestMarkdownRenderer_RendersStrikethrough(t *testing.T) {
	t.Parallel()

	root := buildWebTestVault(t)
	renderer := newTestMarkdownRenderer(t, root)

	out, err := renderer.render("~~gone~~ stays")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	html := string(out)
	if !strings.Contains(html, "<del>gone</del>") {
		t.Fatalf("expected strikethrough to render as <del>, got %q", html)
	}
}

func TestMarkdownRenderer_RendersDisabledTaskListInputs(t *testing.T) {
	t.Parallel()

	root := buildWebTestVault(t)
	renderer := newTestMarkdownRenderer(t, root)

	out, err := renderer.render("- [x] done\n- [ ] todo\n")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	html := string(out)
	for _, want := range []string{
		`<input checked="" disabled="" type="checkbox">`,
		`<input disabled="" type="checkbox">`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected task list input %q, got %q", want, html)
		}
	}
}

func TestMarkdownRenderer_RendersFootnotes(t *testing.T) {
	t.Parallel()

	root := buildWebTestVault(t)
	renderer := newTestMarkdownRenderer(t, root)

	out, err := renderer.render("Text with a note.[^1]\n\n[^1]: The footnote body.\n")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	html := string(out)
	for _, want := range []string{
		`id="fnref:1"`,
		`href="#fn:1"`,
		`class="footnotes"`,
		`id="fn:1"`,
		"The footnote body.",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected footnote HTML to contain %q, got %q", want, html)
		}
	}
}

func TestMarkdownRenderer_RendersDefinitionList(t *testing.T) {
	t.Parallel()

	root := buildWebTestVault(t)
	renderer := newTestMarkdownRenderer(t, root)

	out, err := renderer.render("Term\n: Definition of the term.\n")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	html := string(out)
	for _, want := range []string{
		"<dl>",
		"<dt>Term</dt>",
		"<dd>Definition of the term.</dd>",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected definition list HTML to contain %q, got %q", want, html)
		}
	}
}

func newTestMarkdownRenderer(t *testing.T, root string) *markdownRenderer {
	t.Helper()

	_, v := testVaultOpen(t, root, "127.0.0.1:0")
	state, err := newVaultState(v, time.Second)
	if err != nil {
		t.Fatalf("newVaultState: %v", err)
	}
	renderer, err := newMarkdownRenderer(state)
	if err != nil {
		t.Fatalf("newMarkdownRenderer: %v", err)
	}
	return renderer
}
