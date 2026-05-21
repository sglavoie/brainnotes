package query

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func buildExportGraph(t *testing.T) *Graph {
	t.Helper()
	root := t.TempDir()
	pA := writeNote(t, root, "Note A.md", "---\ntitle: Note A\n---\n[[Note B]] and [[Note C]].\n")
	pB := writeNote(t, root, "Note B.md", "---\ntitle: Note B\n---\n[[Note C]].\n")
	pC := writeNote(t, root, "Note C.md", "---\ntitle: Note C\n---\nNo links.\n")

	notes := []*vault.Note{{Path: pA}, {Path: pB}, {Path: pC}}
	return BuildGraph(notes)
}

func allNodes(g *Graph) map[string]struct{} {
	nodes := make(map[string]struct{}, len(g.Titles))
	for k := range g.Titles {
		nodes[k] = struct{}{}
	}
	return nodes
}

func TestExportDot_Basic(t *testing.T) {
	g := buildExportGraph(t)
	var buf bytes.Buffer
	if err := g.ExportDot(&buf, allNodes(g)); err != nil {
		t.Fatal(err)
	}
	want := `digraph vault {
  rankdir=LR;
  "Note A" -> "Note B";
  "Note A" -> "Note C";
  "Note B" -> "Note C";
}
`
	if buf.String() != want {
		t.Fatalf("ExportDot:\ngot:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestExportMermaid_Basic(t *testing.T) {
	g := buildExportGraph(t)
	var buf bytes.Buffer
	if err := g.ExportMermaid(&buf, allNodes(g)); err != nil {
		t.Fatal(err)
	}
	want := `graph LR
  note-a["Note A"] --> note-b["Note B"]
  note-a --> note-c["Note C"]
  note-b --> note-c
`
	if buf.String() != want {
		t.Fatalf("ExportMermaid:\ngot:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestExportDot_FilteredNodes(t *testing.T) {
	g := buildExportGraph(t)
	nodes := map[string]struct{}{
		"note a": {},
		"note b": {},
	}
	var buf bytes.Buffer
	if err := g.ExportDot(&buf, nodes); err != nil {
		t.Fatal(err)
	}
	want := `digraph vault {
  rankdir=LR;
  "Note A" -> "Note B";
}
`
	if buf.String() != want {
		t.Fatalf("ExportDot filtered:\ngot:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestExportDot_IsolatedNode(t *testing.T) {
	root := t.TempDir()
	pA := writeNote(t, root, "A.md", "---\ntitle: A\n---\nNo links.\n")
	notes := []*vault.Note{{Path: pA}}
	g := BuildGraph(notes)

	var buf bytes.Buffer
	if err := g.ExportDot(&buf, allNodes(g)); err != nil {
		t.Fatal(err)
	}
	want := `digraph vault {
  rankdir=LR;
  "A";
}
`
	if buf.String() != want {
		t.Fatalf("ExportDot isolated:\ngot:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestExportDot_EscapesQuotes(t *testing.T) {
	root := t.TempDir()
	pA := writeNote(t, root, `He said "hi".md`, "---\ntitle: He said \"hi\"\n---\nBody.\n")
	notes := []*vault.Note{{Path: pA}}
	g := BuildGraph(notes)

	var buf bytes.Buffer
	if err := g.ExportDot(&buf, allNodes(g)); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if got != "digraph vault {\n  rankdir=LR;\n  \"He said \\\"hi\\\"\";\n}\n" {
		t.Fatalf("ExportDot escapes:\ngot:\n%s", got)
	}
}

func TestExportMermaid_SpecialChars(t *testing.T) {
	root := t.TempDir()
	pA := writeNote(t, root, `Say "hello".md`, "---\ntitle: Say \"hello\"\n---\nBody.\n")
	notes := []*vault.Note{{Path: pA}}
	g := BuildGraph(notes)

	var buf bytes.Buffer
	if err := g.ExportMermaid(&buf, allNodes(g)); err != nil {
		t.Fatal(err)
	}
	want := "graph LR\n  say-hello[\"Say #quot;hello#quot;\"]\n"
	if buf.String() != want {
		t.Fatalf("ExportMermaid special chars:\ngot:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestExportDot_EmptyGraph(t *testing.T) {
	g := BuildGraph(nil)
	var buf bytes.Buffer
	if err := g.ExportDot(&buf, allNodes(g)); err != nil {
		t.Fatal(err)
	}
	want := "digraph vault {\n  rankdir=LR;\n}\n"
	if buf.String() != want {
		t.Fatalf("ExportDot empty:\ngot:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestExportDot_StableOrder(t *testing.T) {
	g := buildExportGraph(t)
	nodes := allNodes(g)

	var first bytes.Buffer
	if err := g.ExportDot(&first, nodes); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 10; i++ {
		var buf bytes.Buffer
		if err := g.ExportDot(&buf, nodes); err != nil {
			t.Fatal(err)
		}
		if buf.String() != first.String() {
			t.Fatalf("run %d produced different output", i)
		}
	}
}

func TestExportMermaid_StableOrder(t *testing.T) {
	g := buildExportGraph(t)
	nodes := allNodes(g)

	var first bytes.Buffer
	if err := g.ExportMermaid(&first, nodes); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 10; i++ {
		var buf bytes.Buffer
		if err := g.ExportMermaid(&buf, nodes); err != nil {
			t.Fatal(err)
		}
		if buf.String() != first.String() {
			t.Fatalf("run %d produced different output", i)
		}
	}
}

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
	if got := mermaidEscape(`say "hi"`); got != `say #quot;hi#quot;` {
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

// buildExportGraph uses writeNote from graph_test.go — make sure it's accessible.
func TestExportDot_UsesOriginalTitles(t *testing.T) {
	root := t.TempDir()
	pA := writeNote(t, root, "My Note.md", "---\ntitle: My Note\n---\nBody.\n")

	notes := []*vault.Note{{Path: pA}}
	g := BuildGraph(notes)

	var buf bytes.Buffer
	if err := g.ExportDot(&buf, allNodes(g)); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if got != "digraph vault {\n  rankdir=LR;\n  \"My Note\";\n}\n" {
		t.Fatalf("should use original title case:\ngot:\n%s", got)
	}

	_ = filepath.Join(root, "dummy")
}
