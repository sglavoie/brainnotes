package query

import (
	"testing"

	"github.com/sglavoie/brainnotes/internal/vault"
)

func TestGraphResolveTarget(t *testing.T) {
	root := t.TempDir()
	pRootFoo := writeNote(t, root, "Foo.md", "---\ntitle: Foo\n---\n")
	pAreaFoo := writeNote(t, root, "Areas/Foo.md", "---\ntitle: Foo\n---\n")
	pBeta := writeNote(t, root, "Beta.md", "---\ntitle: Beta\n---\n")
	pGamma := writeNote(t, root, "Sub/Gamma.md", "---\ntitle: Gamma\n---\n")

	g := BuildGraph([]*vault.Note{
		{Path: pRootFoo},
		{Path: pAreaFoo},
		{Path: pBeta},
		{Path: pGamma},
	})

	if got := g.ResolveTarget("Areas/Foo"); !strEq(got, []string{"areas/foo"}) {
		t.Fatalf("ResolveTarget(Areas/Foo) = %v, want [areas/foo]", got)
	}
	if got := g.ResolveTarget("Folder/Beta"); !strEq(got, []string{"beta"}) {
		t.Fatalf("ResolveTarget(Folder/Beta) = %v, want [beta] (unique-title fallback)", got)
	}
	if got := g.ResolveTarget("Folder/Foo"); len(got) != 0 {
		t.Fatalf("ResolveTarget(Folder/Foo) = %v, want no match for duplicate-title fallback", got)
	}
	if got := g.ResolveTarget("Beta.md"); !strEq(got, []string{"beta"}) {
		t.Fatalf("ResolveTarget(Beta.md) = %v, want [beta]", got)
	}
	if got := g.ResolveTarget("Sub/Gamma.MD"); !strEq(got, []string{"sub/gamma"}) {
		t.Fatalf("ResolveTarget(Sub/Gamma.MD) = %v, want [sub/gamma]", got)
	}
	if got := g.ResolveTarget("../Beta"); len(got) != 0 {
		t.Fatalf("ResolveTarget(../Beta) = %v, want nil for parent traversal", got)
	}
	if got := g.ResolveTarget("Folder/../Beta"); len(got) != 0 {
		t.Fatalf("ResolveTarget(Folder/../Beta) = %v, want nil for parent traversal", got)
	}
}

func TestGraphResolveTarget_NilReceiver(t *testing.T) {
	var g *Graph
	if got := g.ResolveTarget("Beta"); got != nil {
		t.Fatalf("ResolveTarget on nil graph = %v, want nil", got)
	}
}

func TestGraphUnresolvedTargetKey(t *testing.T) {
	g := BuildGraph(nil)

	if got := g.UnresolvedTargetKey("Areas/Foo.md"); got != "areas/foo" {
		t.Fatalf("UnresolvedTargetKey(Areas/Foo.md) = %q, want %q", got, "areas/foo")
	}
	if got := g.UnresolvedTargetKey("Foo.md"); got != "foo" {
		t.Fatalf("UnresolvedTargetKey(Foo.md) = %q, want %q", got, "foo")
	}
	if got := g.UnresolvedTargetKey("../Foo"); got != "" {
		t.Fatalf("UnresolvedTargetKey(../Foo) = %q, want empty string", got)
	}
	if got := g.UnresolvedTargetKey("Folder/"); got != "" {
		t.Fatalf("UnresolvedTargetKey(Folder/) = %q, want empty string", got)
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

// Nil-receiver safety: all public Graph methods must return zero values rather
// than panicking when called on a nil *Graph.
func TestGraph_NilReceiver(t *testing.T) {
	var g *Graph

	if got := g.ForwardLinks("x"); got != nil {
		t.Errorf("ForwardLinks on nil = %v, want nil", got)
	}
	if got := g.BackLinks("x"); got != nil {
		t.Errorf("BackLinks on nil = %v, want nil", got)
	}
	if got := g.OutDegree("x"); got != 0 {
		t.Errorf("OutDegree on nil = %d, want 0", got)
	}
	if got := g.InDegree("x"); got != 0 {
		t.Errorf("InDegree on nil = %d, want 0", got)
	}

	note := &vault.Note{Path: "/vault/A.md"}
	if got := g.KeyForNote(note); got != "" {
		t.Errorf("KeyForNote on nil = %q, want %q", got, "")
	}
	if got := g.ForwardLinksForNote(note); got != nil {
		t.Errorf("ForwardLinksForNote on nil = %v, want nil", got)
	}
	if got := g.BackLinksForNote(note); got != nil {
		t.Errorf("BackLinksForNote on nil = %v, want nil", got)
	}
	if got := g.OutDegreeForNote(note); got != 0 {
		t.Errorf("OutDegreeForNote on nil = %d, want 0", got)
	}
	if got := g.InDegreeForNote(note); got != 0 {
		t.Errorf("InDegreeForNote on nil = %d, want 0", got)
	}
}
