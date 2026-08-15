package frontmatter

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseUnquotedWikilinksFromSource(t *testing.T) {
	src := []byte("---\n" +
		"scalar: [[People]] # source comment\n" +
		"list:\n" +
		"  - [[Podcasts]]\n" +
		"  - [[Folder/Note.md#Heading|Alias]]\n" +
		"inline: [[[One]], [[Two, Inc.]], plain]\n" +
		"nested:\n" +
		"  owner: [[Café 東京]]\n" +
		"clé: [[東京]]\n" +
		"quoted: \"[[Quoted]]\"\n" +
		"embedded: \"see [[Embedded]] now\"\n" +
		"spaced: [ [People] ]\n" +
		"ordinary: [[People], [Other]]\n" +
		"plain: People\n" +
		"---\nbody\n")

	doc, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	assertFrontmatterValue(t, &doc.Front, "scalar", "[[People]]")
	assertFrontmatterValue(t, &doc.Front, "list", []any{"[[Podcasts]]", "[[Folder/Note.md#Heading|Alias]]"})
	assertFrontmatterValue(t, &doc.Front, "inline", []any{"[[One]]", "[[Two, Inc.]]", "plain"})
	assertFrontmatterValue(t, &doc.Front, "nested", map[string]any{"owner": "[[Café 東京]]"})
	assertFrontmatterValue(t, &doc.Front, "clé", "[[東京]]")
	assertFrontmatterValue(t, &doc.Front, "quoted", "[[Quoted]]")
	assertFrontmatterValue(t, &doc.Front, "embedded", "see [[Embedded]] now")
	assertFrontmatterValue(t, &doc.Front, "spaced", []any{[]any{"People"}})
	assertFrontmatterValue(t, &doc.Front, "ordinary", []any{[]any{"People"}, []any{"Other"}})
	assertFrontmatterValue(t, &doc.Front, "plain", "People")
}

func TestUnquotedWikilinksMarshalAndParseRoundTrip(t *testing.T) {
	src := []byte("---\r\nlinks:\r\n  - [[Folder/Note.md#Heading|Alias]] # keep semantics\r\n  - [[Two, Inc.]]\r\nnested:\r\n  value: [[Café 東京]]\r\n---\r\nbody\r\n")
	doc, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	out, err := Marshal(doc, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(out), "\n") && strings.Contains(strings.ReplaceAll(string(out), "\r\n", ""), "\n") {
		t.Fatalf("Marshal introduced bare LF into CRLF document: %q", out)
	}
	for _, link := range []string{"[[Folder/Note.md#Heading|Alias]]", "[[Two, Inc.]]", "[[Café 東京]]"} {
		if !strings.Contains(string(out), link) {
			t.Fatalf("Marshal dropped %q: %s", link, out)
		}
	}

	roundTrip, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse round trip: %v", err)
	}
	assertFrontmatterValue(t, &roundTrip.Front, "links", []any{"[[Folder/Note.md#Heading|Alias]]", "[[Two, Inc.]]"})
	assertFrontmatterValue(t, &roundTrip.Front, "nested", map[string]any{"value": "[[Café 東京]]"})
}

func assertFrontmatterValue(t *testing.T, front *Map, key string, want any) {
	t.Helper()
	got, ok := front.Get(key)
	if !ok {
		t.Fatalf("missing frontmatter key %q", key)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("frontmatter[%s] = %#v, want %#v", key, got, want)
	}
}
