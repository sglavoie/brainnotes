package query

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func TestSlugify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Note A", "note-a"},
		{"hello world", "hello-world"},
		{"  spaces  ", "spaces"},
		{"UPPER", "upper"},
		{"a/b/c", "a-b-c"},
	}
	for _, tc := range cases {
		if got := slugify(tc.in); got != tc.want {
			t.Errorf("slugify(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMermaidEscape(t *testing.T) {
	if got := mermaidEscape(`say "hi"`); got != `say &quot;hi&quot;` {
		t.Errorf("mermaidEscape: got %q", got)
	}
}

// TestExportMermaid_SlugCollision verifies that two notes whose titles produce
// the same slug get distinct Mermaid node identifiers. Before the fix, both
// would share the same slug ID, causing Mermaid to merge them into one node.
func TestExportMermaid_SlugCollision(t *testing.T) {
	root := t.TempDir()
	// "A-B" and "A B" both slugify to "a-b".
	pAB := writeNote(t, root, "A-B.md", "---\ntitle: A-B\n---\nBody.\n")
	pAB2 := writeNote(t, root, "A B.md", "---\ntitle: A B\n---\nBody.\n")

	notes := []*vault.Note{{Path: pAB}, {Path: pAB2}}
	g := BuildGraph(notes)

	var buf bytes.Buffer
	if err := g.ExportMermaid(&buf, allNodes(g)); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	// The two nodes must have different IDs. The first gets "a-b", the
	// second gets "a-b-1" (or vice versa depending on sort order).
	if !strings.Contains(got, "a-b-1") {
		t.Fatalf("slug collision not resolved:\n%s", got)
	}
	// Both labels must appear.
	if !strings.Contains(got, `"A-B"`) || !strings.Contains(got, `"A B"`) {
		t.Fatalf("both labels should appear:\n%s", got)
	}
}

// TestExportMermaid_SlugCollisionEdges verifies that edges between notes with
// colliding slugs use the correct disambiguated node IDs.
func TestExportMermaid_SlugCollisionEdges(t *testing.T) {
	root := t.TempDir()
	// "A_B" and "A B" both slugify to "a-b". They link to each other.
	pAB1 := writeNote(t, root, "A_B.md", "---\ntitle: A_B\n---\n[[A B]]\n")
	pAB2 := writeNote(t, root, "A B.md", "---\ntitle: A B\n---\n[[A_B]]\n")

	notes := []*vault.Note{{Path: pAB1}, {Path: pAB2}}
	g := BuildGraph(notes)

	var buf bytes.Buffer
	if err := g.ExportMermaid(&buf, allNodes(g)); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	// Both nodes must have distinct IDs.
	if !strings.Contains(got, "a-b-1") {
		t.Fatalf("slug collision in edges not resolved:\n%s", got)
	}
	// Must contain an edge line with "-->"
	if !strings.Contains(got, "-->") {
		t.Fatalf("expected edges:\n%s", got)
	}
}

// TestExportMermaid_SlugSuffixCollidesWithNaturalSlug verifies that when the
// disambiguation suffix for a duplicate slug would itself collide with another
// title's natural slug, the algorithm keeps incrementing until it finds a
// unique ID. Before the fix, "A-B" and "A B" would both produce slug "a-b",
// and the disambiguation would assign "a-b-1" to the second — but if a third
// title "A B-1" naturally slugifies to "a-b-1", two nodes would share that ID.
func TestExportMermaid_SlugSuffixCollidesWithNaturalSlug(t *testing.T) {
	root := t.TempDir()
	// "A B" and "A-B" both slugify to "a-b".
	// "A B 1" slugifies to "a-b-1", which collides with the naive suffix.
	pAB := writeNote(t, root, "A B.md", "---\ntitle: A B\n---\nBody.\n")
	pABdash := writeNote(t, root, "A-B.md", "---\ntitle: A-B\n---\nBody.\n")
	pAB1 := writeNote(t, root, "A B 1.md", "---\ntitle: A B 1\n---\nBody.\n")

	notes := []*vault.Note{{Path: pAB}, {Path: pABdash}, {Path: pAB1}}
	g := BuildGraph(notes)

	var buf bytes.Buffer
	if err := g.ExportMermaid(&buf, allNodes(g)); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	// Count unique node IDs. Each [" label "] definition should have a unique slug.
	// The three nodes need three distinct IDs. One gets "a-b", one gets "a-b-1",
	// and the last one must NOT also be "a-b-1" — it should get "a-b-2".
	if !strings.Contains(got, "a-b-2") {
		t.Fatalf("expected 'a-b-2' to avoid suffix collision with natural slug 'a-b-1':\n%s", got)
	}
}
