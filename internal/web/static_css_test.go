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
		"minmax(13.5rem, 31.25rem) minmax(0, 1fr)",
		"minmax(13.5rem, 1fr)",
		"minmax(0, 48rem)",
		"minmax(12rem, 1fr)",
		"max-width: 31.25rem;",
		"box-sizing: border-box;",
		"justify-self: start;",
		"justify-self: end;",
		"position: sticky;",
		"max-height: calc(100vh - 2rem);",
		"overflow-y: auto;",
	} {
		if !strings.Contains(desktop, want) {
			t.Fatalf("desktop note layout CSS missing %q in block:\n%s", want, desktop)
		}
	}
	for _, unwanted := range []string{
		"justify-content: center;",
		"minmax(13.5rem, 17rem)",
		"minmax(12rem, 16rem)",
	} {
		if strings.Contains(desktop, unwanted) {
			t.Fatalf("desktop note layout CSS should not contain %q in block:\n%s", unwanted, desktop)
		}
	}
	for _, want := range []string{
		".note-main",
		"width: 100%;",
		"max-width: none;",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("note page width CSS missing %q", want)
		}
	}
}

func TestManagementCSSAccessibilityAndMobileContract(t *testing.T) {
	css := readStaticCSS(t)

	for _, want := range []string{
		"a:focus-visible",
		".overview-result-link:focus-visible",
		".overview-bulk-button:focus-visible",
		".overview-bulk-preview",
		".overview-bulk-preview-actions",
		".overview-bulk-preview-result-header",
		".overview-bulk-preview-frontmatter-item::before",
		"overflow-wrap: anywhere;",
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
		".overview-bulk-preview-header",
		".overview-bulk-preview-result-header",
		".overview-bulk-preview-actions .overview-bulk-button",
		".history-item",
		"grid-template-columns: minmax(0, 1fr);",
		"justify-self: stretch;",
	} {
		if !strings.Contains(narrow, want) {
			t.Fatalf("narrow management CSS missing %q in block:\n%s", want, narrow)
		}
	}
}

func TestReviewCockpitCSSAccessibilityAndMobileContract(t *testing.T) {
	css := readStaticCSS(t)

	for _, want := range []string{
		".review-panel[aria-busy=\"true\"]",
		".review-counts",
		"grid-template-columns: repeat(4, minmax(4.6rem, 1fr));",
		".review-status.is-loading",
		".review-status.is-error",
		".review-status.is-success",
		".review-error",
		".review-empty",
		".review-warnings[hidden]",
		".review-list[hidden]",
		".review-item",
		"grid-template-columns: minmax(0, 1fr) auto;",
		".review-item-title",
		".review-item-details summary",
		".review-affected-note",
		"overflow-wrap: anywhere;",
		".review-item-actions",
		"grid-template-columns: repeat(3, minmax(7.2rem, 1fr));",
		".review-action:focus-visible",
		".review-item-details summary:focus-visible",
		".review-action[data-review-action=\"done\"]",
		".review-action[data-review-action=\"snooze\"]",
		".review-open-editor",
		".review-action:disabled",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("review cockpit CSS missing %q", want)
		}
	}

	narrow := cssBlockAfter(t, css, "@media (max-width: 640px)")
	for _, want := range []string{
		".review-panel",
		"padding: 0.75rem;",
		".review-counts",
		"grid-template-columns: repeat(2, minmax(0, 1fr));",
		".review-item",
		"padding: 0.65rem;",
		".review-item-actions",
		"gap: 0.35rem;",
		".review-action",
		"min-height: 2.55rem;",
		"font-size: 0.78rem;",
		".review-open-editor",
		"flex: 1 1 9rem;",
	} {
		if !strings.Contains(narrow, want) {
			t.Fatalf("narrow review cockpit CSS missing %q in block:\n%s", want, narrow)
		}
	}
}

func TestNoteActionsStatusLineDoesNotResizeButtonRow(t *testing.T) {
	css := readStaticCSS(t)

	for _, want := range []string{
		".note-action-group",
		"display: contents;",
		".note-actions .open-editor-button",
		".note-actions .trash-note-button",
		".note-actions .copy-button",
		"order: 1;",
		".note-actions .open-editor-status",
		".note-actions .trash-note-status",
		".note-actions .copy-status",
		"order: 2;",
		"flex: 0 0 100%;",
		"overflow-wrap: anywhere;",
		".note-actions .open-editor-status:empty",
		"display: none;",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("note action status CSS missing %q", want)
		}
	}
}

func TestScrollbarsUseDarkTheme(t *testing.T) {
	css := readStaticCSS(t)

	for _, want := range []string{
		"color-scheme: dark;",
		"scrollbar-color: #3a465c transparent;",
		"scrollbar-width: thin;",
		"*::-webkit-scrollbar",
		"width: 0.7rem;",
		"height: 0.7rem;",
		"*::-webkit-scrollbar-thumb",
		"border-radius: 999px;",
		"background-color: #3a465c;",
		"background-clip: content-box;",
		"*::-webkit-scrollbar-thumb:hover",
		"background-color: #52617a;",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("dark scrollbar CSS missing %q", want)
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
