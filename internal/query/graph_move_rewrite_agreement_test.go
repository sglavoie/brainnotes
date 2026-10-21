package query

import (
	"slices"
	"testing"

	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/wikilink"
)

// TestRewriteMoveAgreesWithGraphResolution pins the contract between the link
// graph and wikilink.RewriteMove: a folder-qualified link is rewritten by a
// move exactly when the graph resolves it to the moved note by path. Two notes
// share the title so the graph's unique-title fallback cannot resolve a
// spelling the path key refused.
func TestRewriteMoveAgreesWithGraphResolution(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"A/Foo.md": "---\ntitle: Foo\n---\nbody\n",
		"C/Foo.md": "---\ntitle: Foo\n---\nbody\n",
	})
	g := BuildGraph([]*vault.Note{notes["A/Foo.md"], notes["C/Foo.md"]})
	const movedKey = "a/foo"
	// Guards against a vacuous pass where neither side matches anything.
	if !slices.Contains(g.ResolveTarget("A//Foo"), movedKey) {
		t.Fatalf("graph does not resolve [[A//Foo]] to %s; the fixture proves nothing", movedKey)
	}
	targets := []string{
		"A/Foo", "/A/Foo", "A//Foo", "/A//Foo.md", "a/FOO.md", `A\Foo`,
		"./A/Foo", "A/./Foo", "X/../A/Foo", "C/Foo", "B/Foo",
	}
	for _, target := range targets {
		resolved := slices.Contains(g.ResolveTarget(target), movedKey)
		in := "[[" + target + "]]"
		rewritten := wikilink.RewriteMove(in, "A/Foo.md", "B/Foo.md") != in
		if resolved != rewritten {
			t.Errorf("[[%s]]: graph resolves to moved note = %v, RewriteMove rewrites = %v", target, resolved, rewritten)
		}
	}
}
