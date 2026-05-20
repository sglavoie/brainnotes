package query

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func writeNote(t *testing.T, root, rel, content string) string {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return full
}

func TestBuildGraph_Basic(t *testing.T) {
	root := t.TempDir()
	pA := writeNote(t, root, "A.md", "---\ntitle: A\n---\nLinks to [[B]] and [[C]].\n")
	pB := writeNote(t, root, "B.md", "---\ntitle: B\n---\nLinks to [[C]].\n")
	writeNote(t, root, "C.md", "---\ntitle: C\n---\nNo links here.\n")

	notes := []*vault.Note{{Path: pA}, {Path: pB}, {Path: filepath.Join(root, "C.md")}}
	g := BuildGraph(notes)

	wantFwd := []string{"b", "c"}
	if got := g.ForwardLinks("A"); !strEq(got, wantFwd) {
		t.Fatalf("Forward[a] = %v, want %v", got, wantFwd)
	}
	if got := g.ForwardLinks("B"); !strEq(got, []string{"c"}) {
		t.Fatalf("Forward[b] = %v, want [c]", got)
	}
	if got := g.ForwardLinks("C"); len(got) != 0 {
		t.Fatalf("Forward[c] = %v, want empty", got)
	}

	backC := g.BackLinks("c")
	sort.Strings(backC)
	if len(backC) != 2 {
		t.Fatalf("Backward[c] = %v, want 2 entries", backC)
	}

	if len(g.Titles) != 3 {
		t.Fatalf("Titles = %d, want 3", len(g.Titles))
	}
}

func TestBuildGraph_Deduplication(t *testing.T) {
	root := t.TempDir()
	pA := writeNote(t, root, "A.md", "---\ntitle: A\n---\n[[Foo]] and [[Foo|alias]] and [[Folder/Foo]].\n")
	writeNote(t, root, "Foo.md", "---\ntitle: Foo\n---\nbody\n")

	notes := []*vault.Note{{Path: pA}, {Path: filepath.Join(root, "Foo.md")}}
	g := BuildGraph(notes)

	fwd := g.ForwardLinks("A")
	if len(fwd) != 1 || fwd[0] != "foo" {
		t.Fatalf("Forward[a] = %v, want [foo]", fwd)
	}
}

func TestBuildGraph_SelfLinkExcluded(t *testing.T) {
	root := t.TempDir()
	pA := writeNote(t, root, "A.md", "---\ntitle: A\n---\n[[A]] self-reference.\n")

	notes := []*vault.Note{{Path: pA}}
	g := BuildGraph(notes)

	if len(g.ForwardLinks("A")) != 0 {
		t.Fatalf("self-link should be excluded from Forward")
	}
	if len(g.BackLinks("a")) != 0 {
		t.Fatalf("self-link should be excluded from Backward")
	}
}

func TestBuildGraph_FrontmatterLinks(t *testing.T) {
	root := t.TempDir()
	pA := writeNote(t, root, "A.md", "---\ntitle: A\ncategory: \"[[B]]\"\ntags:\n  - \"[[C]]\"\n---\nbody\n")
	writeNote(t, root, "B.md", "---\ntitle: B\n---\nbody\n")
	writeNote(t, root, "C.md", "---\ntitle: C\n---\nbody\n")

	notes := []*vault.Note{
		{Path: pA},
		{Path: filepath.Join(root, "B.md")},
		{Path: filepath.Join(root, "C.md")},
	}
	g := BuildGraph(notes)

	fwd := g.ForwardLinks("A")
	sort.Strings(fwd)
	if !strEq(fwd, []string{"b", "c"}) {
		t.Fatalf("Forward[a] = %v, want [b c]", fwd)
	}
}

func TestBuildGraph_CodeFenceExcluded(t *testing.T) {
	root := t.TempDir()
	pA := writeNote(t, root, "A.md", "---\ntitle: A\n---\n```\n[[B]]\n```\n")
	writeNote(t, root, "B.md", "---\ntitle: B\n---\nbody\n")

	notes := []*vault.Note{{Path: pA}, {Path: filepath.Join(root, "B.md")}}
	g := BuildGraph(notes)

	if len(g.ForwardLinks("A")) != 0 {
		t.Fatalf("link inside code fence should be excluded")
	}
}

func TestBuildGraph_BrokenLinksExcludedFromForward(t *testing.T) {
	root := t.TempDir()
	pA := writeNote(t, root, "A.md", "---\ntitle: A\n---\n[[NonExistent]] link.\n")

	notes := []*vault.Note{{Path: pA}}
	g := BuildGraph(notes)

	if len(g.ForwardLinks("A")) != 0 {
		t.Fatalf("broken link should be excluded from Forward")
	}
	if len(g.BackLinks("nonexistent")) != 1 {
		t.Fatalf("broken link should still appear in Backward")
	}
}

func TestBuildGraph_OutDegreeInDegree(t *testing.T) {
	root := t.TempDir()
	pA := writeNote(t, root, "A.md", "---\ntitle: A\n---\n[[B]] and [[C]].\n")
	writeNote(t, root, "B.md", "---\ntitle: B\n---\n[[A]].\n")
	writeNote(t, root, "C.md", "---\ntitle: C\n---\nbody\n")

	notes := []*vault.Note{
		{Path: pA},
		{Path: filepath.Join(root, "B.md")},
		{Path: filepath.Join(root, "C.md")},
	}
	g := BuildGraph(notes)

	if g.OutDegree("A") != 2 {
		t.Fatalf("OutDegree(A) = %d, want 2", g.OutDegree("A"))
	}
	if g.InDegree("A") != 1 {
		t.Fatalf("InDegree(A) = %d, want 1", g.InDegree("A"))
	}
	if g.InDegree("C") != 1 {
		t.Fatalf("InDegree(C) = %d, want 1", g.InDegree("C"))
	}
}

func TestBuildGraph_EmptyVault(t *testing.T) {
	g := BuildGraph(nil)
	if g == nil {
		t.Fatal("BuildGraph(nil) returned nil")
	}
	if len(g.Titles) != 0 {
		t.Fatalf("expected empty Titles, got %d", len(g.Titles))
	}
}

func TestBuildGraph_NilNotesSkipped(t *testing.T) {
	root := t.TempDir()
	pA := writeNote(t, root, "A.md", "---\ntitle: A\n---\nbody\n")

	notes := []*vault.Note{nil, {Path: pA}, nil}
	g := BuildGraph(notes)

	if len(g.Titles) != 1 {
		t.Fatalf("expected 1 title, got %d", len(g.Titles))
	}
}

func strEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
