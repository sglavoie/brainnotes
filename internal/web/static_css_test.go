package web

import (
	"strings"
	"testing"
)

func TestSiteNavigationCSSContract(t *testing.T) {
	css := readStaticCSS(t)

	for _, want := range []string{
		".site-nav",
		"display: flex;",
		"flex-wrap: wrap;",
		"gap: 0.3rem;",
		"margin: 0 0 1.25rem;",
		".site-nav a",
		"min-height: 2.1rem;",
		"padding: 0.25rem 0.7rem;",
		".site-nav a:hover",
		"text-decoration: none;",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("site navigation CSS missing %q", want)
		}
	}
}

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

func TestNoteInsightCSSContract(t *testing.T) {
	css := readStaticCSS(t)
	for _, want := range []string{
		".note-insight",
		".note-insight-controls",
		"grid-template-columns: minmax(300px, 1fr) auto;",
		".note-insight-prompt",
		"min-width: 300px;",
		".note-insight-button:focus-visible",
		".note-insight-result",
		".note-insight-copy",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("note insight CSS missing %q", want)
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
		".brief-action-button:focus-visible",
		".brief-insight-copy:focus-visible",
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

func TestBriefCSSAccessibilityAndResponsiveContract(t *testing.T) {
	css := readStaticCSS(t)

	for _, want := range []string{
		".brief-panel",
		".brief-slot",
		".brief-slot.is-busy",
		".brief-slot-grid",
		".brief-card-actions",
		".brief-action-button",
		".brief-action-button.is-opened",
		".brief-insight-prompt-field",
		".brief-insight-prompt-controls",
		".brief-insight-prompt",
		".brief-action-hint",
		".brief-slot-status",
		".brief-global-status",
		".brief-insight",
		".brief-insight-header",
		".brief-insight-body",
		".brief-insight-copy",
		"overflow-wrap: anywhere;",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("brief CSS missing %q", want)
		}
	}

	reducedMotion := cssBlockAfter(t, css, "@media (prefers-reduced-motion: reduce)")
	for _, want := range []string{
		".brief-slot",
		".brief-action-button",
		".brief-insight-copy",
		"transition: none;",
	} {
		if !strings.Contains(reducedMotion, want) {
			t.Fatalf("reduced-motion brief CSS missing %q in block:\n%s", want, reducedMotion)
		}
	}

	for _, want := range []string{
		"@media (max-width: 960px)",
		".brief-slot-grid",
		".brief-card-actions",
		"gap: 0.35rem;",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("max-width 960 brief CSS missing %q", want)
		}
	}

	narrow640 := cssBlockAfter(t, css, "@media (max-width: 640px)")
	for _, want := range []string{
		".brief-panel",
		"padding: 0.75rem;",
		".brief-card-actions",
		"grid-template-columns: repeat(2, minmax(0, 1fr));",
		".brief-action-button",
		".brief-insight-copy",
		".brief-insight-header",
		"flex-direction: column;",
	} {
		if !strings.Contains(narrow640, want) {
			t.Fatalf("max-width 640 brief CSS missing %q in block:\n%s", want, narrow640)
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
