package web

import (
	"strings"
	"testing"
)

func TestGraphViewControlsCSSContract(t *testing.T) {
	css := readStaticCSS(t)

	for _, want := range []string{
		".graph-view-controls",
		".graph-view-actions",
		".graph-view-controls-header",
		"#graph-mindegree-value",
		".graph-mindegree-input",
		".graph-mindegree-input:focus-visible",
		"#graph-collapse-value",
		".graph-collapse-input",
		".graph-collapse-input:focus-visible",
		"accent-color: var(--bn-accent);",
		"font-variant-numeric: tabular-nums;",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("graph view controls CSS missing %q", want)
		}
	}

	// Isolation is fixed at one hop, so the Hops range no longer exists in the
	// markup. A selector that outlives its element is dead weight nobody trips
	// over until they try to style something else in this block.
	for _, gone := range []string{
		"#graph-hops-value",
		".graph-hops-input",
	} {
		if strings.Contains(css, gone) {
			t.Fatalf("graph view controls CSS still carries the removed Hops selector %q", gone)
		}
	}

	// The isolate range shares the one focus outline rule with the other graph
	// controls, so keyboard focus cannot go invisible on the new input.
	focusRule := cssBlockAfter(t, css, ".graph-filter-input:focus-visible,")
	if !strings.Contains(focusRule, "outline: 2px solid var(--bn-focus);") {
		t.Fatalf("graph focus outline rule missing its outline declaration:\n%s", focusRule)
	}

	viewControls := cssBlockAfter(t, css, ".graph-view-controls {")
	for _, want := range []string{
		"display: flex;",
		"flex-direction: column;",
		"gap: 0.4rem;",
	} {
		if !strings.Contains(viewControls, want) {
			t.Fatalf("graph view controls block missing %q in block:\n%s", want, viewControls)
		}
	}

	// The block is a cell of the .graph-controls grid, so it has to be re-checked
	// at the same two breakpoints the grid changes its track count at. The 960px
	// override is anchored on the two-column track list rather than on the media
	// query itself, because app.css opens two `max-width: 960px` blocks and only
	// the later one carries the graph overrides.
	medium := cssBlockAfter(t, css, "grid-template-columns: minmax(0, 1fr) minmax(12rem, 0.8fr);")
	for _, want := range []string{
		".graph-view-controls",
		"grid-column: 1 / -1;",
	} {
		if !strings.Contains(medium, want) {
			t.Fatalf("medium graph view controls CSS missing %q in block:\n%s", want, medium)
		}
	}

	narrow := cssBlockAfter(t, css, "@media (max-width: 640px)")
	for _, want := range []string{
		".graph-view-controls",
		".graph-view-actions",
		"grid-template-columns: repeat(2, minmax(0, 1fr));",
	} {
		if !strings.Contains(narrow, want) {
			t.Fatalf("narrow graph view controls CSS missing %q in block:\n%s", want, narrow)
		}
	}
}

// TestSourceEditorPanelsCSSContract pins the pair every toggled panel on /edit
// needs: a rule that sets `display` and a matching `[hidden]` rule that takes it
// back.
//
// `hidden` is a user-agent `display: none` and any author rule setting `display`
// on the same element outranks it, so a panel styled as a grid and toggled by
// `hidden` is *visible from page load* with nothing in it — an empty "unsaved
// text kept in this browser" claim over a note nobody has touched, and a
// discard that appears to do nothing. jsdom applies no stylesheet, so the
// TypeScript suite's `hidden` assertions pass either way; this is the cheap
// check, and internal/web/e2e/editor.spec.js is the one that actually renders.
func TestSourceEditorPanelsCSSContract(t *testing.T) {
	css := readStaticCSS(t)

	for _, want := range []string{
		".source-editor-recovery",
		".source-editor-recovery[hidden]",
		".source-editor-recovery-actions > [hidden]",
		".source-editor-conflict",
		".source-editor-conflict[hidden]",
		"display: none;",
		// The draft line is styled apart from .source-editor-status deliberately:
		// one reports the vault and the other reports a copy that has never left
		// the browser.
		".source-editor-draft-status",
		".source-editor-draft-status.is-warning",
		".source-editor-draft-status.is-error",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("source editor panel CSS missing %q", want)
		}
	}
}
