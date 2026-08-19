package web

import (
	"strings"
	"testing"
)

func TestMarkdownRenderer_AppliesTypographerInProseNotCode(t *testing.T) {
	t.Parallel()

	root := buildWebTestVault(t)
	renderer := newTestMarkdownRenderer(t, root)

	out, err := renderer.render("She said \"hello\" -- really...\n\n`\"literal\" -- ...`\n\n```\n\"literal\" -- ...\n```\n")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	html := string(out)
	for _, want := range []string{"&ldquo;", "&rdquo;", "&ndash;", "&hellip;"} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected typographic punctuation %q in prose, got %q", want, html)
		}
	}
	if !strings.Contains(html, `<code>&quot;literal&quot; -- ...</code>`) {
		t.Fatalf("expected inline code to keep literal punctuation, got %q", html)
	}
	if !strings.Contains(html, "&quot;literal&quot; -- ...\n</code></pre>") {
		t.Fatalf("expected fenced code to keep literal punctuation, got %q", html)
	}
}
