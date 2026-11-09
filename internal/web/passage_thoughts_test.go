package web

import (
	"net/http"
	"strings"
	"testing"
)

func TestPassageThoughtPreservesLiteralQuotation(t *testing.T) {
	source, quote, thought := parsePassageThought("# My thought\n\nFrom [[Books/Source]]\n\n> A \\[\\[literal\\]\\] café \\& \\<tag\\>.\n> \n> Another line.\n\nAn idea worth keeping.")
	if source != "Books/Source" || quote != "A [[literal]] café & <tag>.\n\nAnother line." || thought != "An idea worth keeping." {
		t.Fatalf("unexpected parsed thought: %q %q %q", source, quote, thought)
	}
	for _, body := range []string{"From [[Source]]\n\nNo quotation.", "```\nFrom [[Source]]\n\n> Quote\n\nNot provenance.\n```", strings.Repeat("x", 128*1024+1)} {
		if source, _, _ := parsePassageThought(body); source != "" {
			t.Fatal("ordinary prose must not become an annotation")
		}
	}
}

func TestServerPassageThoughtsResolveExactSourcesAndEscapeContent(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Books/Source.md": []byte("# Source\n\nAn original passage."),
		"Other/Source.md": []byte("# Source\n\nAn unrelated passage."),
		"Thought.md":      []byte("# Thought\n\nFrom [[Books/Source]]\n\n> An original passage.\n\nA <script>literal</script> thought."),
		"Ambiguous.md":    []byte("# Ambiguous\n\nFrom [[Source]]\n\n> An original passage.\n\nAmbiguous source."),
	})
	for _, test := range []struct {
		path string
		want bool
	}{{"/n/books/source", true}, {"/n/other/source", false}} {
		resp, err := http.Get(ts.URL + test.path) // #nosec G107 -- test-only local URL
		if err != nil {
			t.Fatal(err)
		}
		body := readResponseBody(t, resp)
		if got := strings.Contains(body, `data-quote="An original passage."`); got != test.want {
			t.Fatalf("%s annotation = %v", test.path, got)
		}
		if strings.Contains(body, "A <script>literal</script> thought.") {
			t.Fatal("annotation interpreted HTML")
		}
	}
}
