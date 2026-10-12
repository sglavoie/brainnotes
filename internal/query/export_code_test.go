package query

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func TestExportCode_Basic(t *testing.T) {
	root := t.TempDir()
	pA := writeNote(t, root, "Alpha.md", "---\ntitle: Alpha\n---\nintro\n[[Beta]]\n")
	pB := writeNote(t, root, "Beta.md", "---\ntitle: Beta\n---\nNo links.\n")

	notes := []*vault.Note{{Path: pA}, {Path: pB}}
	g := BuildGraph(notes)

	var buf bytes.Buffer
	if err := g.ExportCode(&buf, notes, allNodes(g)); err != nil {
		t.Fatal(err)
	}
	// Beta is isolated (no outgoing edges), so it is emitted first.
	want := pB + ":1\n" + pA + ":5: [[Beta]] -> " + pB + ":1\n"
	if buf.String() != want {
		t.Fatalf("ExportCode:\ngot:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestExportCode_FrontmatterEdge(t *testing.T) {
	root := t.TempDir()
	pA := writeNote(t, root, "Alpha.md", "---\ntitle: Alpha\nrelated: \"[[Beta]]\"\n---\nBody.\n")
	pB := writeNote(t, root, "Beta.md", "---\ntitle: Beta\n---\nNo links.\n")

	notes := []*vault.Note{{Path: pA}, {Path: pB}}
	g := BuildGraph(notes)

	var buf bytes.Buffer
	if err := g.ExportCode(&buf, notes, allNodes(g)); err != nil {
		t.Fatal(err)
	}
	want := pB + ":1\n" + pA + ":1: frontmatter[related] [[Beta]] -> " + pB + ":1\n"
	if buf.String() != want {
		t.Fatalf("ExportCode frontmatter:\ngot:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestExportCode_DropsUnresolvedLinks(t *testing.T) {
	root := t.TempDir()
	pA := writeNote(t, root, "Alpha.md", "---\ntitle: Alpha\n---\n[[Nope]]\n")
	notes := []*vault.Note{{Path: pA}}
	g := BuildGraph(notes)

	var buf bytes.Buffer
	if err := g.ExportCode(&buf, notes, allNodes(g)); err != nil {
		t.Fatal(err)
	}
	// Alpha's link is unresolved, so no edge is emitted. Alpha remains
	// isolated.
	want := pA + ":1\n"
	if buf.String() != want {
		t.Fatalf("ExportCode unresolved:\ngot:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestExportCode_DropsSelfEdges(t *testing.T) {
	root := t.TempDir()
	pA := writeNote(t, root, "Alpha.md", "---\ntitle: Alpha\n---\n[[Alpha]]\n")
	notes := []*vault.Note{{Path: pA}}
	g := BuildGraph(notes)

	var buf bytes.Buffer
	if err := g.ExportCode(&buf, notes, allNodes(g)); err != nil {
		t.Fatal(err)
	}
	// Self-edges are dropped; Alpha becomes isolated.
	want := pA + ":1\n"
	if buf.String() != want {
		t.Fatalf("ExportCode self-edge:\ngot:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestExportCode_DropsFilteredTargets(t *testing.T) {
	root := t.TempDir()
	pA := writeNote(t, root, "Alpha.md", "---\ntitle: Alpha\n---\n[[Beta]] [[Gamma]]\n")
	pB := writeNote(t, root, "Beta.md", "---\ntitle: Beta\n---\nBody.\n")
	pC := writeNote(t, root, "Gamma.md", "---\ntitle: Gamma\n---\nBody.\n")
	notes := []*vault.Note{{Path: pA}, {Path: pB}, {Path: pC}}
	g := BuildGraph(notes)

	// Filter: nodeSet excludes Gamma.
	nodes := map[string]struct{}{
		"alpha": {},
		"beta":  {},
	}
	var buf bytes.Buffer
	if err := g.ExportCode(&buf, notes, nodes); err != nil {
		t.Fatal(err)
	}
	// The Gamma edge is dropped; only the Alpha → Beta edge remains.
	// Beta has no outgoing edges, so it is emitted as isolated first.
	want := pB + ":1\n" + pA + ":4: [[Beta]] -> " + pB + ":1\n"
	if buf.String() != want {
		t.Fatalf("ExportCode filtered target:\ngot:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestExportCode_StableOrder(t *testing.T) {
	root := t.TempDir()
	pA := writeNote(t, root, "Alpha.md", "---\ntitle: Alpha\n---\n[[Beta]] and [[Gamma]]\n")
	pB := writeNote(t, root, "Beta.md", "---\ntitle: Beta\n---\n[[Gamma]]\n")
	pC := writeNote(t, root, "Gamma.md", "---\ntitle: Gamma\n---\nBody.\n")

	notes := []*vault.Note{{Path: pA}, {Path: pB}, {Path: pC}}
	g := BuildGraph(notes)

	var first bytes.Buffer
	if err := g.ExportCode(&first, notes, allNodes(g)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		var buf bytes.Buffer
		if err := g.ExportCode(&buf, notes, allNodes(g)); err != nil {
			t.Fatal(err)
		}
		if buf.String() != first.String() {
			t.Fatalf("run %d produced different output:\nfirst:\n%s\ngot:\n%s", i, first.String(), buf.String())
		}
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
