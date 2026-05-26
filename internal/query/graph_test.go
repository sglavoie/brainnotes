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

func TestBuildGraph_MarkdownExtensionWikilinksResolve(t *testing.T) {
	root := t.TempDir()
	pA := writeNote(t, root, "A.md", "---\ntitle: A\nrelated: \"[[D.md]]\"\n---\n[[B.md]] [[Sub/C.MD]] [[A.md]]\n")
	writeNote(t, root, "B.md", "---\ntitle: B\n---\nbody\n")
	writeNote(t, root, "Sub/C.md", "---\ntitle: C\n---\nbody\n")
	writeNote(t, root, "D.md", "---\ntitle: D\n---\nbody\n")

	notes := []*vault.Note{
		{Path: pA},
		{Path: filepath.Join(root, "B.md")},
		{Path: filepath.Join(root, "Sub", "C.md")},
		{Path: filepath.Join(root, "D.md")},
	}
	g := BuildGraph(notes)

	if got := g.ForwardLinks("A"); !strEq(got, []string{"b", "d", "sub/c"}) {
		t.Fatalf("Forward[a] = %v, want [b d sub/c]", got)
	}
	if len(g.BackLinks("b")) != 1 {
		t.Fatalf("[[B.md]] should record one backlink for B, got %v", g.BackLinks("b"))
	}
	if len(g.BackLinks("c")) != 1 {
		t.Fatalf("[[Sub/C.MD]] should record one backlink for C, got %v", g.BackLinks("c"))
	}
	if len(g.BackLinks("d")) != 1 {
		t.Fatalf("frontmatter [[D.md]] should record one backlink for D, got %v", g.BackLinks("d"))
	}
	if len(g.BackLinks("a")) != 0 {
		t.Fatalf("[[A.md]] should be treated as a self-link and excluded, got %v", g.BackLinks("a"))
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

// TestBuildGraph_NonLowercaseMDExtension verifies that notes whose filename
// uses an upper- or mixed-case `.MD` extension are registered correctly in the
// graph. Before the fix, BuildGraph used strings.TrimSuffix(base, ".md") which
// is case-sensitive — a file like "Note.MD" was registered under the key
// "note.md" instead of "note", breaking all graph lookups (forward links,
// backlinks, orphan detection, etc.) for that note.
func TestBuildGraph_NonLowercaseMDExtension(t *testing.T) {
	root := t.TempDir()
	pA := writeNote(t, root, "Alpha.MD", "---\ntitle: Alpha\n---\nLinks to [[Beta]].\n")
	pB := writeNote(t, root, "Beta.Md", "---\ntitle: Beta\n---\nLinks to [[Alpha]].\n")

	notes := []*vault.Note{{Path: pA}, {Path: pB}}
	g := BuildGraph(notes)

	// Titles should be registered under lowercase stems without extension.
	if _, ok := g.Titles["alpha"]; !ok {
		t.Errorf("expected 'alpha' in Titles, got keys: %v", g.Titles)
	}
	if _, ok := g.Titles["beta"]; !ok {
		t.Errorf("expected 'beta' in Titles, got keys: %v", g.Titles)
	}

	// Forward links should resolve correctly.
	fwdA := g.ForwardLinks("Alpha")
	if !strEq(fwdA, []string{"beta"}) {
		t.Errorf("Forward[alpha] = %v, want [beta]", fwdA)
	}
	fwdB := g.ForwardLinks("Beta")
	if !strEq(fwdB, []string{"alpha"}) {
		t.Errorf("Forward[beta] = %v, want [alpha]", fwdB)
	}

	// InDegree should count correctly.
	if g.InDegree("Alpha") != 1 {
		t.Errorf("InDegree(Alpha) = %d, want 1", g.InDegree("Alpha"))
	}
	if g.InDegree("Beta") != 1 {
		t.Errorf("InDegree(Beta) = %d, want 1", g.InDegree("Beta"))
	}

	// Self-link exclusion should still work.
	pSelf := writeNote(t, root, "Self.MD", "---\ntitle: Self\n---\n[[Self]] only.\n")
	gSelf := BuildGraph([]*vault.Note{{Path: pSelf}})
	if len(gSelf.ForwardLinks("Self")) != 0 {
		t.Errorf("self-link should be excluded even with .MD extension")
	}
}

// TestBuildGraph_BacklinkDeduplication verifies that a note linking to the same
// target multiple times (in body, frontmatter, or both) produces only one
// backward entry per source note. Before the fix, duplicate paths accumulated
// in the Backward map, inflating InDegree results (though InDegree itself
// deduplicates, BackLinks returned duplicates to callers).
func TestBuildGraph_BacklinkDeduplication(t *testing.T) {
	root := t.TempDir()
	// A.md links to B three times: twice in body, once in frontmatter.
	pA := writeNote(t, root, "A.md", "---\ntitle: A\ncategory: \"[[B]]\"\n---\n[[B]] and [[B|alias]].\n")
	writeNote(t, root, "B.md", "---\ntitle: B\n---\nbody\n")

	notes := []*vault.Note{
		{Path: pA},
		{Path: filepath.Join(root, "B.md")},
	}
	g := BuildGraph(notes)

	back := g.BackLinks("b")
	if len(back) != 1 {
		t.Errorf("BackLinks(b) = %v (len %d), want exactly 1 entry (deduplicated)", back, len(back))
	}
}

// TestBuildGraph_BacklinkDeduplicationMultipleSources verifies that when
// multiple notes link to the same target, each source appears exactly once.
func TestBuildGraph_BacklinkDeduplicationMultipleSources(t *testing.T) {
	root := t.TempDir()
	pA := writeNote(t, root, "A.md", "---\ntitle: A\n---\n[[C]] and [[C]].\n")
	pB := writeNote(t, root, "B.md", "---\ntitle: B\ncategory: \"[[C]]\"\n---\n[[C]].\n")
	writeNote(t, root, "C.md", "---\ntitle: C\n---\nbody\n")

	notes := []*vault.Note{
		{Path: pA},
		{Path: pB},
		{Path: filepath.Join(root, "C.md")},
	}
	g := BuildGraph(notes)

	back := g.BackLinks("c")
	if len(back) != 2 {
		t.Errorf("BackLinks(c) = %v (len %d), want exactly 2 entries (one per source)", back, len(back))
	}
}

func TestBuildGraph_FolderQualifiedDuplicateTitleResolvesExactPath(t *testing.T) {
	root := t.TempDir()
	pSource := writeNote(t, root, "Source.md", "---\ntitle: Source\n---\n[[Areas/Foo]]\n")
	pRootFoo := writeNote(t, root, "Foo.md", "---\ntitle: Foo\n---\nbody\n")
	pAreaFoo := writeNote(t, root, "Areas/Foo.md", "---\ntitle: Foo\n---\nbody\n")

	notes := []*vault.Note{
		{Path: pSource},
		{Path: pRootFoo},
		{Path: pAreaFoo},
	}
	g := BuildGraph(notes)

	if got := g.ForwardLinks("Source"); !strEq(got, []string{"areas/foo"}) {
		t.Fatalf("Forward[source] = %v, want [areas/foo]", got)
	}
	if got := g.BackLinks("areas/foo"); len(got) != 1 || got[0] != pSource {
		t.Fatalf("BackLinks(areas/foo) = %v, want [%s]", got, pSource)
	}
	if got := g.BackLinksForNote(&vault.Note{Path: pRootFoo}); len(got) != 0 {
		t.Fatalf("root Foo should not receive folder-qualified backlink, got %v", got)
	}
}

func TestBuildGraph_FolderQualifiedUniqueTitleFallsBackToTitleMatch(t *testing.T) {
	root := t.TempDir()
	pSource := writeNote(t, root, "Source.md", "---\ntitle: Source\n---\n[[Folder/Foo]]\n")
	pFoo := writeNote(t, root, "Foo.md", "---\ntitle: Foo\n---\nbody\n")

	notes := []*vault.Note{
		{Path: pSource},
		{Path: pFoo},
	}
	g := BuildGraph(notes)

	if got := g.ForwardLinks("Source"); !strEq(got, []string{"foo"}) {
		t.Fatalf("Forward[source] = %v, want [foo]", got)
	}
	if got := g.BackLinks("foo"); len(got) != 1 || got[0] != pSource {
		t.Fatalf("BackLinks(foo) = %v, want [%s]", got, pSource)
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
