package web

import (
	"strings"
	"testing"
)

func TestSearchSnippetPresentation(t *testing.T) {
	for _, tc := range []struct{ name, body, query, want string }{
		{"links", "Read [[Notes/Fieldwork|earlier observations]] and [the journal](journal.md).", "observations", "Read earlier <mark>observations</mark> and the journal."},
		{"plain target", "See [[Beta]] again.", "beta", "See <mark>Beta</mark> again."},
		{"unsafe text", "<script>alert('x')</script>", "script", "&lt;<mark>script</mark>&gt;alert(&#39;x&#39;)&lt;/<mark>script</mark>&gt;"},
		{"unicode offsets", "İstanbul café CAFÉ", "café", "İstanbul <mark>café</mark> <mark>CAFÉ</mark>"},
		{"whitespace", "  A note\n\nwith   space.  ", "note", "A <mark>note</mark> with space."},
		{"block markers", "## Field journal\n\n- First observation\n> Keep looking.", "field", "<mark>Field</mark> journal First observation Keep looking."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := makeSearchSnippet(tc.body, tc.query); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSearchSnippetWindow(t *testing.T) {
	body := strings.Repeat("before ", 30) + "observations " + strings.Repeat("afterwards ", 30)
	got := makeSearchSnippet(body, "observations")
	if !strings.Contains(got, "<mark>observations</mark>") || !strings.HasPrefix(got, "…before ") || !strings.HasSuffix(got, "afterwards…") {
		t.Fatalf("expected a centered excerpt with complete boundary words: %q", got)
	}
	longQuery := strings.Repeat("長", 100)
	if got := makeSearchSnippet("prefix "+longQuery+" suffix", longQuery); !strings.Contains(got, "<mark>"+longQuery+"</mark>") {
		t.Fatalf("long match was cut: %q", got)
	}
}
