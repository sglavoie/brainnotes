package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/vault"
)

func TestMarkdownRenderer_ResolvesAndFallsBackForWikilinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	testFiles := map[string]string{
		"Project Note.md": "---\ntitle: Project Note\n---\nA\n",
		"Work/Alpha.md":   "---\ntitle: Alpha\n---\nA\n",
		"Play/Alpha.md":   "---\ntitle: Alpha\n---\nA\n",
	}
	for rel, body := range testFiles {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	disabled := false
	v, err := vault.Open(&config.Config{
		Vault: root,
		Serve: config.ServeConfig{
			OpenBrowser: &disabled,
		},
	})
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}

	state, err := newVaultState(v, time.Second)
	if err != nil {
		t.Fatalf("newVaultState: %v", err)
	}
	renderer, err := newMarkdownRenderer(state)
	if err != nil {
		t.Fatalf("newMarkdownRenderer: %v", err)
	}

	out, err := renderer.render("[[Project Note]] [[Missing]] [[Alpha]]")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	html := string(out)
	if !strings.Contains(html, `href="/n/project%20note"`) {
		t.Fatalf("expected resolved link, got %q", html)
	}
	if !strings.Contains(html, `href="/broken?q=missing"`) {
		t.Fatalf("expected broken fallback for missing target, got %q", html)
	}
	if !strings.Contains(html, `href="/broken?q=alpha"`) {
		t.Fatalf("expected broken fallback for ambiguous target, got %q", html)
	}
}

func TestMarkdownRenderer_DoesNotRenderUnsafeRawHTML(t *testing.T) {
	t.Parallel()

	root := buildWebTestVault(t)
	renderer := newTestMarkdownRenderer(t, root)

	out, err := renderer.render("raw <script>alert(1)</script>")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(string(out), "<script>") {
		t.Fatalf("expected unsafe html to be escaped/omitted, got %q", string(out))
	}
}

func TestMarkdownRenderer_HighlightsRecognizedCodeFence(t *testing.T) {
	t.Parallel()

	root := buildWebTestVault(t)
	renderer := newTestMarkdownRenderer(t, root)

	out, err := renderer.render("```go\npackage main\n\nfunc main() {\n\tfmt.Println(\"ok\")\n}\n```\n")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	html := string(out)
	for _, want := range []string{
		`<pre tabindex="0" class="chroma">`,
		`<span class="kn">package</span>`,
		`<span class="nf">Println</span>`,
		`package`,
		`Println`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected class-highlighted code fence to contain %q, got %q", want, html)
		}
	}
	for _, unwanted := range []string{`style="color:`, `<table`, `class="ln"`, `class="lnt"`} {
		if strings.Contains(html, unwanted) {
			t.Fatalf("expected class-based highlighting without inline styles or line numbers, got %q", html)
		}
	}
}

func TestMarkdownRenderer_HighlightedCodeFenceEscapesUnsafeHTML(t *testing.T) {
	t.Parallel()

	root := buildWebTestVault(t)
	renderer := newTestMarkdownRenderer(t, root)

	out, err := renderer.render("```go\nfmt.Println(\"<script>alert(1)</script>\")\n```\n")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	html := string(out)
	if strings.Contains(html, "<script>") || strings.Contains(html, "</script>") {
		t.Fatalf("expected unsafe html in code fence to be escaped, got %q", html)
	}
	if !strings.Contains(html, `&lt;script&gt;alert(1)&lt;/script&gt;`) {
		t.Fatalf("expected escaped script text in code fence, got %q", html)
	}
}

func TestMarkdownRenderer_LeavesUnlabeledCodeFencePlain(t *testing.T) {
	t.Parallel()

	root := buildWebTestVault(t)
	renderer := newTestMarkdownRenderer(t, root)

	out, err := renderer.render("```\npackage main\nfunc main() {}\n```\n")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	html := string(out)
	if !strings.Contains(html, "<pre><code>package main\nfunc main() {}\n</code></pre>") {
		t.Fatalf("expected unlabeled code fence to render as plain code, got %q", html)
	}
	for _, unwanted := range []string{`<pre tabindex="0"`, `<span`, `style="color:`, `<table`, `class="ln"`, `class="lnt"`} {
		if strings.Contains(html, unwanted) {
			t.Fatalf("expected unlabeled code fence without guessed highlighting or line numbers, got %q", html)
		}
	}
}

func TestMarkdownRenderer_LeavesUnknownCodeFencePlain(t *testing.T) {
	t.Parallel()

	root := buildWebTestVault(t)
	renderer := newTestMarkdownRenderer(t, root)

	out, err := renderer.render("```brainnotes\n<script>alert(1)</script>\n```\n")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	html := string(out)
	for _, want := range []string{`class="language-brainnotes"`, `&lt;script&gt;alert(1)&lt;/script&gt;`} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected unknown code fence to contain %q, got %q", want, html)
		}
	}
	if strings.Contains(html, "<script>") || strings.Contains(html, "</script>") {
		t.Fatalf("expected unsafe html in unknown code fence to be escaped, got %q", html)
	}
	for _, unwanted := range []string{`<pre tabindex="0"`, `<span`, `style="color:`, `<table`, `class="ln"`, `class="lnt"`} {
		if strings.Contains(html, unwanted) {
			t.Fatalf("expected unknown code fence without guessed highlighting or line numbers, got %q", html)
		}
	}
}

func TestMarkdownRenderer_AddsHeadingAnchorsWithoutUnsafeRawHTML(t *testing.T) {
	t.Parallel()

	root := buildWebTestVault(t)
	renderer := newTestMarkdownRenderer(t, root)

	out, err := renderer.render("## Safe Heading\n\nraw <script>alert(1)</script>")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	html := string(out)
	for _, want := range []string{`id="safe-heading"`, `href="#safe-heading"`, `class="heading-anchor"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected rendered markdown to contain %q, got %q", want, html)
		}
	}
	if strings.Contains(html, "<script>") {
		t.Fatalf("expected unsafe html to be escaped/omitted, got %q", html)
	}
}

func TestMarkdownRenderer_LinkifiesBareURLs(t *testing.T) {
	t.Parallel()

	root := buildWebTestVault(t)
	renderer := newTestMarkdownRenderer(t, root)

	source := strings.Join([]string{
		"https://www.merakialive.com",
		"www.example.com",
		"foo@example.com",
		"ftp://example.com",
		"https://example.com/search?q=a&hl=en",
		"https://github.com?q=stars:>1",
		"https://example.com/path.",
		"javascript:alert(1)",
	}, "\n")
	out, err := renderer.render(source)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	html := string(out)
	for _, want := range []string{
		`<a href="https://www.merakialive.com">https://www.merakialive.com</a>`,
		`<a href="http://www.example.com">www.example.com</a>`,
		`<a href="mailto:foo@example.com">foo@example.com</a>`,
		`<a href="ftp://example.com">ftp://example.com</a>`,
		`<a href="https://example.com/search?q=a&amp;hl=en">https://example.com/search?q=a&amp;hl=en</a>`,
		`<a href="https://github.com?q=stars:%3E1">https://github.com?q=stars:&gt;1</a>`,
		`<a href="https://example.com/path">https://example.com/path</a>.`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected rendered markdown to contain %q, got %q", want, html)
		}
	}
	if strings.Contains(html, `href="javascript:alert(1)"`) {
		t.Fatalf("expected javascript URL to remain plain text, got %q", html)
	}
}

func TestMarkdownRenderer_DoesNotLinkifyCodeText(t *testing.T) {
	t.Parallel()

	root := buildWebTestVault(t)
	renderer := newTestMarkdownRenderer(t, root)

	out, err := renderer.render("Outside https://example.com\n\nInside `https://example.com`\n\n```\nhttps://example.com\n```")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	html := string(out)
	if got := strings.Count(html, `href="https://example.com"`); got != 1 {
		t.Fatalf("expected only one bare URL link outside code, got %d in %q", got, html)
	}
	if !strings.Contains(html, `<code>https://example.com</code>`) {
		t.Fatalf("expected inline code to preserve URL text, got %q", html)
	}
	if !strings.Contains(html, "https://example.com\n</code></pre>") {
		t.Fatalf("expected fenced code to preserve URL text, got %q", html)
	}
}

func TestMarkdownRenderer_PreservesExplicitLinksAndWikilinksWithLinkify(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	notePath := filepath.Join(root, "Project Note.md")
	if err := os.WriteFile(notePath, []byte("---\ntitle: Project Note\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	renderer := newTestMarkdownRenderer(t, root)

	out, err := renderer.render("[explicit](https://example.com) [[Project Note]]")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	html := string(out)
	if got := strings.Count(html, `href="https://example.com"`); got != 1 {
		t.Fatalf("expected explicit markdown link to render once, got %d in %q", got, html)
	}
	if !strings.Contains(html, `<a href="https://example.com">explicit</a>`) {
		t.Fatalf("expected explicit markdown link text to be preserved, got %q", html)
	}
	if !strings.Contains(html, `href="/n/project%20note"`) {
		t.Fatalf("expected wikilink to still resolve, got %q", html)
	}
}

func TestMarkdownRenderer_DoesNotLinkCodeFenceText(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	notePath := filepath.Join(root, "Project Note.md")
	if err := os.WriteFile(notePath, []byte("---\ntitle: Project Note\n---\nBody\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
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

	out, err := renderer.render("Outside [[Project Note]]\n\n```md\n[[Project Note]]\n```\n")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	html := string(out)
	if got := strings.Count(html, `href="/n/project%20note"`); got != 1 {
		t.Fatalf("expected only one resolved link outside code fence, got %d in %q", got, html)
	}
	if !strings.Contains(html, "[[Project Note]]") {
		t.Fatalf("expected code fence to preserve wikilink text, got %q", html)
	}
}
