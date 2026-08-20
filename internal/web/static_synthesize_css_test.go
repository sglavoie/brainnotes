package web

import (
	"strings"
	"testing"
)

// TestSynthesizeWorkbenchCSSContract pins the workbench layout to the shared
// breakpoints. The browser tests can assert the DOM the controller builds but
// not how it lays out, so the responsive half of that contract lives here.
func TestSynthesizeWorkbenchCSSContract(t *testing.T) {
	rules := parseCSSRules(readStaticCSS(t))

	// Desktop is the base declaration: two side-by-side columns, outside any
	// media query. The narrow override collapses them to one.
	columns := selectRules(t, rules, ".synthesize-columns")
	if len(columns) != 2 {
		t.Fatalf("expected a base and a narrow .synthesize-columns rule, got %d", len(columns))
	}
	if columns[0].media != "" {
		t.Fatalf("desktop .synthesize-columns must be unconditional, found inside %q", columns[0].media)
	}
	requireDeclaration(t, columns[0], "grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);")
	if columns[1].media != "@media (max-width: 960px)" {
		t.Fatalf("narrow .synthesize-columns must live in the 960px query, found %q", columns[1].media)
	}
	requireDeclaration(t, columns[1], "grid-template-columns: minmax(0, 1fr);")

	// The source row drops its trailing controls column on a phone, and the
	// action rows stack rather than overflowing sideways.
	sources := selectRules(t, rules, ".synthesize-source")
	if len(sources) != 2 {
		t.Fatalf("expected a base and a narrow .synthesize-source rule, got %d", len(sources))
	}
	if sources[0].media != "" || sources[1].media != "@media (max-width: 640px)" {
		t.Fatalf("source rules must be base then 640px, got %q and %q", sources[0].media, sources[1].media)
	}
	requireDeclaration(t, sources[0], "grid-template-columns: auto minmax(0, 1fr) auto;")
	requireDeclaration(t, sources[1], "grid-template-columns: auto minmax(0, 1fr);")

	stacked := selectRules(t, rules,
		".synthesize-search-form, .synthesize-context-actions, .synthesize-generate-actions, .synthesize-create-actions, .synthesize-candidate-actions")
	if len(stacked) != 1 || stacked[0].media != "@media (max-width: 640px)" {
		t.Fatalf("search, context, generate, create, and candidate actions must stack inside the 640px query, got %d rules", len(stacked))
	}
	requireDeclaration(t, stacked[0], "flex-direction: column;")

	// The packet is user content of unbounded width, so it must never widen the
	// page on a narrow screen.
	packet := selectRules(t, rules, ".synthesize-packet-body")
	if len(packet) != 1 {
		t.Fatalf("expected one .synthesize-packet-body rule, got %d", len(packet))
	}
	for _, want := range []string{"overflow: auto;", "white-space: pre-wrap;", "overflow-wrap: anywhere;"} {
		requireDeclaration(t, packet[0], want)
	}

	// The candidate and the previewed note are rendered Markdown of unbounded
	// width, so they scroll inside their panel rather than stretching the column.
	rendered := selectRules(t, rules, ".synthesize-candidate-rendered, .synthesize-preview-rendered")
	if len(rendered) != 1 {
		t.Fatalf("expected one rendered-output rule, got %d", len(rendered))
	}
	for _, want := range []string{"overflow: auto;", "overflow-wrap: anywhere;", "min-width: 0;"} {
		requireDeclaration(t, rendered[0], want)
	}
}

type cssRule struct {
	media    string
	selector string
	body     string
}

// parseCSSRules walks the stylesheet by brace depth so a rule reports the
// at-rule it is nested in. Rules come back in source order because a nested
// block always closes before its parent.
func parseCSSRules(css string) []cssRule {
	css = stripCSSComments(css)
	var rules []cssRule
	var stack []string
	starts := []int{}
	prelude := strings.Builder{}
	for index := 0; index < len(css); index++ {
		switch css[index] {
		case '{':
			stack = append(stack, collapseWhitespace(prelude.String()))
			starts = append(starts, index+1)
			prelude.Reset()
		case '}':
			if len(stack) > 0 {
				parent := ""
				if len(stack) > 1 {
					parent = stack[len(stack)-2]
				}
				rules = append(rules, cssRule{
					media:    parent,
					selector: stack[len(stack)-1],
					body:     css[starts[len(starts)-1]:index],
				})
				stack = stack[:len(stack)-1]
				starts = starts[:len(starts)-1]
			}
			prelude.Reset()
		default:
			prelude.WriteByte(css[index])
		}
	}
	return rules
}

func stripCSSComments(css string) string {
	out := strings.Builder{}
	for {
		open := strings.Index(css, "/*")
		if open == -1 {
			out.WriteString(css)
			return out.String()
		}
		out.WriteString(css[:open])
		close := strings.Index(css[open:], "*/")
		if close == -1 {
			return out.String()
		}
		css = css[open+close+2:]
	}
}

func collapseWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func selectRules(t *testing.T, rules []cssRule, selector string) []cssRule {
	t.Helper()
	var matched []cssRule
	for _, rule := range rules {
		if rule.selector == selector {
			matched = append(matched, rule)
		}
	}
	if len(matched) == 0 {
		t.Fatalf("CSS selector %q not found", selector)
	}
	return matched
}

func requireDeclaration(t *testing.T, rule cssRule, declaration string) {
	t.Helper()
	if !strings.Contains(collapseWhitespace(rule.body), declaration) {
		t.Fatalf("rule %q missing %q, body:\n%s", rule.selector, declaration, rule.body)
	}
}
