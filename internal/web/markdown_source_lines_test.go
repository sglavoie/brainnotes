package web

import (
	"strings"
	"testing"
)

func TestMarkdownSourceLines(t *testing.T) {
	renderer := newTestMarkdownRenderer(t, buildWebTestVault(t))
	html, err := renderer.render("## 日本語\r\n\r\nFirst paragraph.\r\n\r\n## 日本語\r\n\r\nLast paragraph.")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"1", "3", "5", "7"} {
		if !strings.Contains(string(html), `data-source-line="`+line+`"`) {
			t.Fatalf("line %s missing from %s", line, html)
		}
	}
}
