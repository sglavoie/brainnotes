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

func TestManagementCSSAccessibilityAndMobileContract(t *testing.T) {
	css := readStaticCSS(t)

	for _, want := range []string{
		"a:focus-visible",
		".overview-result-link:focus-visible",
		".overview-bulk-button:focus-visible",
		".history-button:focus-visible",
		".trash-note-button:focus-visible",
		".frontmatter-button:focus-visible",
		".create-note-button:focus-visible",
		"outline: 2px solid #5f8fff;",
		"white-space: nowrap;",
		".history-item > .history-button",
		"justify-self: end;",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("management accessibility CSS missing %q", want)
		}
	}

	narrow := cssBlockAfter(t, css, "@media (max-width: 640px)")
	for _, want := range []string{
		".overview-bulk-form",
		"width: 100%;",
		".overview-bulk-form input",
		".overview-bulk-form select",
		"flex: 1 1 10rem;",
		".history-item",
		"grid-template-columns: minmax(0, 1fr);",
		"justify-self: stretch;",
	} {
		if !strings.Contains(narrow, want) {
			t.Fatalf("narrow management CSS missing %q in block:\n%s", want, narrow)
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
