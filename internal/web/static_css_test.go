package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoteLayoutCSSResponsiveContract(t *testing.T) {
	css := readStaticCSS(t)

	narrow := cssBlockAfter(t, css, "@media (max-width: 960px)")
	for _, want := range []string{
		".note-layout",
		"grid-template-columns: minmax(0, 1fr);",
		".note-meta-sidebar",
		".note-toc-sidebar",
		".note-content",
		"grid-column: auto;",
		"grid-row: auto;",
		"position: static;",
		"max-height: none;",
		"overflow-y: visible;",
	} {
		if !strings.Contains(narrow, want) {
			t.Fatalf("narrow note layout CSS missing %q in block:\n%s", want, narrow)
		}
	}

	desktop := cssBlockAfter(t, css, "@media (min-width: 961px)")
	for _, want := range []string{
		".note-layout",
		".note-layout-with-toc",
		"grid-template-columns:",
		"position: sticky;",
		"max-height: calc(100vh - 2rem);",
		"overflow-y: auto;",
	} {
		if !strings.Contains(desktop, want) {
			t.Fatalf("desktop note layout CSS missing %q in block:\n%s", want, desktop)
		}
	}
}

func readStaticCSS(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("static", "app.css"))
	if err != nil {
		t.Fatalf("read static CSS: %v", err)
	}
	return string(body)
}

func cssBlockAfter(t *testing.T, css, marker string) string {
	t.Helper()
	start := strings.Index(css, marker)
	if start == -1 {
		t.Fatalf("CSS marker %q not found", marker)
	}
	rest := css[start:]
	next := strings.Index(rest[len(marker):], "@media ")
	if next == -1 {
		return rest
	}
	return rest[:len(marker)+next]
}
