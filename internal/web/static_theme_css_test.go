package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestPropertiesWorkbenchCSSContract(t *testing.T) {
	css := readStaticCSS(t)

	for _, want := range []string{
		".properties-panel[aria-busy=\"true\"]",
		".properties-button:focus-visible",
		".properties-button:disabled",
		".properties-status.is-empty",
		".properties-status.is-error",
		".properties-config",
		".properties-totals",
		".properties-stat",
		".properties-filters",
		".properties-list",
		"grid-template-columns: repeat(auto-fill, minmax(min(18rem, 100%), 1fr));",
		".properties-item[data-properties-issues]",
		".properties-filter-input:focus-visible",
		".properties-filter-select:focus-visible",
		".properties-item-header",
		".properties-badge-configured",
		".properties-badge-unconfigured",
		".properties-badge-issues",
		".properties-proposal-toml",
		".properties-proposal-actions",
		".properties-config-open:focus-visible",
		".properties-value-button:focus-visible",
		".properties-value-button.is-active",
		".properties-value-button.is-unsupported",
		".properties-row-value.is-missing",
		".properties-row-value.is-unsupported",
		".properties-row-issue",
		".properties-pagination-status",
		"outline: 2px solid var(--bn-focus);",
		"overflow-wrap: anywhere;",
		// Configuration health banners.
		".properties-banner-invalid",
		".properties-banner-restart",
		// Definition editor.
		".properties-schema-fields",
		".properties-schema-toggle",
		".properties-schema-error",
		".properties-schema-actions",
		// Typed value controls.
		".properties-control:focus-visible",
		".properties-control-input",
		".properties-control-textarea",
		".properties-control-list-items",
		".properties-control-list-item",
		// Selection toolbar and keyboard affordances.
		".properties-keyboard-hint",
		".properties-selection-count",
		".properties-selection-actions",
		".properties-row.is-selected",
		".properties-row:focus-visible",
		".properties-row-select",
		".properties-row-actions",
		// Inline editor.
		".properties-editor-title",
		".properties-editor-error",
		".properties-editor-actions",
		".properties-button-primary",
		// Preview dialog.
		".properties-preview-dialog::backdrop",
		".properties-preview-dialog:not([open])",
		".properties-dialog-blockers",
		".properties-dialog-warnings",
		".properties-dialog-file-path",
		".properties-dialog-config-diff",
		".properties-dialog-projection",
		".properties-dialog-expiry",
		".properties-dialog-actions",
		// Undo toast.
		".properties-toast[hidden]",
		".properties-toast-message",
		".properties-toast-actions",
		".properties-toast-history",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("properties workbench CSS missing %q", want)
		}
	}

	narrow := cssBlockAfter(t, css, "@media (max-width: 640px)")
	for _, want := range []string{
		".properties-panel",
		"padding: 0.75rem;",
		".properties-totals",
		"grid-template-columns: repeat(2, minmax(0, 1fr));",
		".properties-filters",
		"flex-direction: column;",
		".properties-value-list",
		"grid-template-columns: minmax(0, 1fr);",
		".properties-pagination",
		".properties-selection",
		".properties-selection-actions",
		".properties-schema-fields",
		".properties-control-list-item",
		".properties-row-actions",
		".properties-dialog",
		".properties-toast",
	} {
		if !strings.Contains(narrow, want) {
			t.Fatalf("narrow properties workbench CSS missing %q in block:\n%s", want, narrow)
		}
	}
}

// readStaticCSS returns app.css with quoting normalized to double quotes.
//
// CSS treats '…' and "…" as the same token, so a contract that pins a selector
// like [aria-expanded="false"] is asserting the selector, not the quote
// character the stylesheet happens to use. The stylesheet's convention is
// single quotes for attribute selectors and font names; normalizing here lets
// every contract in these files spell its expectations with Go's plain
// double-quoted strings without becoming a de facto formatting lock. Nothing
// else is relaxed: a selector, declaration or media query that actually
// disappears still fails its contract.
func readStaticCSS(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("static", "app.css"))
	if err != nil {
		t.Fatalf("read static CSS: %v", err)
	}
	return strings.ReplaceAll(string(body), "'", `"`)
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
