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
		"--bn-note-meta-width",
		"min(31.25rem, max(13.5rem, calc((100vw - 48rem - 2rem) / 2)))",
		"minmax(0, 48rem)",
		"minmax(12rem, 1fr)",
		".note-layout.is-metadata-hidden",
		"--bn-note-toc-sidebar-width",
		".note-meta-resize-handle",
		"cursor: col-resize;",
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
		"outline: 2px solid var(--bn-focus);",
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

func TestThemeTokenFoundationContract(t *testing.T) {
	css := readStaticCSS(t)

	// Light is the base palette declared on :root, with color-scheme light.
	for _, want := range []string{
		":root {",
		"color-scheme: light;",
		"--bn-bg: #f7f9fc;",
		"--bn-surface: #ffffff;",
		"--bn-text: #18202f;",
		"--bn-muted: #5d687a;",
		"--bn-border: #cfd8e6;",
		"--bn-link: #2563eb;",
		"--bn-focus: #2563eb;",
		"--bn-scrollbar-thumb: #b9c4d6;",
		"--bn-scrollbar-thumb-hover: #9aa8c0;",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("light theme token CSS missing %q", want)
		}
	}

	// System follows prefers-color-scheme: the dark media query re-declares the
	// dark token values, preserving the original dark palette.
	darkMedia := cssBlockAfter(t, css, "@media (prefers-color-scheme: dark)")
	for _, want := range []string{
		":root {",
		"color-scheme: dark;",
		"--bn-bg: #10131a;",
		"--bn-scrollbar-thumb: #3a465c;",
		"--bn-scrollbar-thumb-hover: #52617a;",
		"--bn-focus: #5f8fff;",
	} {
		if !strings.Contains(darkMedia, want) {
			t.Fatalf("system dark theme token CSS missing %q in block:\n%s", want, darkMedia)
		}
	}

	// Explicit overrides are browser-local, higher-specificity selectors that
	// win over both the base palette and the system media query.
	for _, want := range []string{
		":root[data-theme=\"light\"] {",
		":root[data-theme=\"dark\"] {",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("explicit theme selector CSS missing %q", want)
		}
	}

	// Shared scrollbar styling and the generic focus outline are tokenized.
	for _, want := range []string{
		"scrollbar-color: var(--bn-scrollbar-thumb) transparent;",
		"scrollbar-width: thin;",
		"*::-webkit-scrollbar",
		"width: 0.7rem;",
		"height: 0.7rem;",
		"border-radius: 999px;",
		"background-color: var(--bn-scrollbar-thumb);",
		"background-clip: content-box;",
		"background-color: var(--bn-scrollbar-thumb-hover);",
		"outline: 2px solid var(--bn-focus);",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("tokenized theme CSS missing %q", want)
		}
	}

	// The old hard-coded dark scrollbar literals must be gone.
	for _, unwanted := range []string{
		"scrollbar-color: #3a465c transparent;",
		"background-color: #3a465c;",
		"background-color: #52617a;",
	} {
		if strings.Contains(css, unwanted) {
			t.Fatalf("theme CSS should no longer contain literal dark scrollbar rule %q", unwanted)
		}
	}
}

func TestComponentCSSTokenizationContract(t *testing.T) {
	css := readStaticCSS(t)

	// Component sections (overview/bulk, history, cockpit, diagnostics, recent
	// notes, note actions/layout, frontmatter, backlinks, broken links, filter
	// form, and graph page chrome) reference semantic theme tokens rather than
	// raw literal colors. Spot-check a distinctive tokenized declaration from
	// each token family instead of a brittle full-file snapshot.
	for _, want := range []string{
		// Surfaces.
		"background: var(--bn-raised);",
		"background: var(--bn-sunken);",
		"background: var(--bn-hover);",
		// Borders and accents.
		"border: 1px solid var(--bn-border);",
		"border-color: var(--bn-border-strong);",
		"background: var(--bn-accent-subtle-bg);",
		"border: 1px solid var(--bn-accent-subtle-border);",
		// Text, links, accent.
		"color: var(--bn-text);",
		"color: var(--bn-muted);",
		"color: var(--bn-text-subtle);",
		"color: var(--bn-link);",
		"accent-color: var(--bn-accent);",
		// Status triples.
		"color: var(--bn-danger);",
		"background: var(--bn-danger-bg);",
		"border-color: var(--bn-danger-border);",
		"color: var(--bn-warning);",
		"background: var(--bn-warning-bg);",
		"color: var(--bn-success);",
		"background: var(--bn-success-bg);",
		// Code surface and graph page chrome hooks.
		"background: var(--bn-code-bg) !important;",
		"background: var(--bn-graph-canvas-bg);",
		"border-color: var(--bn-graph-locked);",
		"box-shadow: inset 3px 0 0 var(--bn-graph-locked);",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("component tokenization CSS missing %q", want)
		}
	}

	// Representative component-only literals that were consolidated into tokens
	// must be gone everywhere. These values are not theme-token declarations, so
	// their reappearance signals a regression back to hard-coded component colors.
	for _, unwanted := range []string{
		"#301b23", // danger hover background
		"#9db2c9", // muted text
		"#3a4b68", // hover border
		"#cfd9ea", // subtle text
		"#eaf0ff", // strong text
	} {
		if strings.Contains(css, unwanted) {
			t.Fatalf("component CSS should no longer contain consolidated literal %q", unwanted)
		}
	}
}

func TestNoteBodyExtendedMarkdownCSSContract(t *testing.T) {
	css := readStaticCSS(t)

	for _, want := range []string{
		// Wide tables scroll horizontally on the table element itself (Goldmark
		// emits a bare <table>, not the .table-wrap helper).
		".note-body table",
		"display: block;",
		"max-width: 100%;",
		"overflow-x: auto;",
		// Read-only task lists: no marker, non-interactive checkbox cursor.
		".note-body li:has(> input[type=\"checkbox\"])",
		"list-style: none;",
		".note-body li > input[type=\"checkbox\"]",
		"cursor: default;",
		// Definition list term/definition hierarchy.
		".note-body dt",
		"font-weight: 600;",
		".note-body dd",
		// Footnote references and the endnote block.
		".note-body .footnote-ref",
		".note-body .footnotes",
		"border-top: 1px solid var(--bn-border);",
		".note-body .footnote-backref",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("note-body extended Markdown CSS missing %q", want)
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
