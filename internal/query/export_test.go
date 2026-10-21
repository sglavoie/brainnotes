package query

import (
	"bytes"
	"testing"

	"github.com/sglavoie/brainnotes/internal/vault"
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
	want := "graph LR\n  say-hello[\"Say &quot;hello&quot;\"]\n"
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

func TestExportDot_DedupesAndSortsForwardEdges(t *testing.T) {
	g := &Graph{
		Forward: map[string][]string{
			"a": {"c", "b", "b"},
		},
		Titles: map[string]struct{}{
			"a": {}, "b": {}, "c": {},
		},
		OriginalTitles: map[string]string{
			"a": "A", "b": "B", "c": "C",
		},
	}

	var buf bytes.Buffer
	if err := g.ExportDot(&buf, allNodes(g)); err != nil {
		t.Fatal(err)
	}
	want := `digraph vault {
  rankdir=LR;
  "A" -> "B";
  "A" -> "C";
}
`
	if buf.String() != want {
		t.Fatalf("ExportDot should sort and dedupe manual Forward edges:\ngot:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestExportMermaid_DedupesAndSortsForwardEdges(t *testing.T) {
	g := &Graph{
		Forward: map[string][]string{
			"a": {"c", "b", "b"},
		},
		Titles: map[string]struct{}{
			"a": {}, "b": {}, "c": {},
		},
		OriginalTitles: map[string]string{
			"a": "A", "b": "B", "c": "C",
		},
	}

	var buf bytes.Buffer
	if err := g.ExportMermaid(&buf, allNodes(g)); err != nil {
		t.Fatal(err)
	}
	want := `graph LR
  a["A"] --> b["B"]
  a --> c["C"]
`
	if buf.String() != want {
		t.Fatalf("ExportMermaid should sort and dedupe manual Forward edges:\ngot:\n%s\nwant:\n%s", buf.String(), want)
	}
}
