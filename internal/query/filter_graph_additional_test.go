package query

import (
	"github.com/sglavoie/brainnotes/internal/vault"
	"testing"
)

// TestBrokenLinksPredicateCleanNote verifies that BrokenLinks returns false
// when every link in the note resolves. The predicate exists primarily as the
// match-side of UnresolvedLinks; this pins down the "no broken links" branch.
func TestBrokenLinksPredicateCleanNote(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Alpha.md": "---\ntitle: Alpha\n---\nSee [[Beta]] and [[Gamma|alias]].\n",
		"Beta.md":  "---\ntitle: Beta\n---\nbody\n",
		"Gamma.md": "---\ntitle: Gamma\n---\nbody\n",
	})
	all := []*vault.Note{notes["Alpha.md"], notes["Beta.md"], notes["Gamma.md"]}
	g := BuildGraph(all)
	if BrokenLinks(g)(notes["Alpha.md"]) {
		t.Errorf("BrokenLinks should be false for a note whose links all resolve")
	}
	// Beta has no links, so BrokenLinks should be false.
	if BrokenLinks(g)(notes["Beta.md"]) {
		t.Errorf("BrokenLinks should be false for a link-free note")
	}
}

// TestFolderInteriorDotSegments verifies that interior `./` and `a/./b`
// segments in a folder argument are normalized via path.Clean.

// TestOrphanPredicateGracefulWithEmptyGraph verifies that Orphan against an
// empty graph (no notes registered) classifies any note as an orphan: with no
// backlinks recorded, there's nothing to disqualify it.
func TestOrphanPredicateGracefulWithEmptyGraph(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntitle: a\n---\n[[Nowhere]]\n",
	})
	g := BuildGraph(nil)
	if !Orphan(g)(notes["a.md"]) {
		t.Errorf("Orphan should be true against an empty graph (no inbound links possible)")
	}
}

// TestMissingTrueForUnreadableNote pins down the documented contract that
// Missing returns true when frontmatter fails to load (e.g. file disappeared).
// Pair this with TestMissingIsStrictComplementOfHasOnParseError: that test
// covers parse errors, this one covers file-read errors.

// TestOrphanPredicateGraphMissingNoteEntry verifies that Orphan returns true
// when the note's title key has no Backward entries (the typical orphan
// scenario). This is the positive case for Orphan and pins down that an empty
// BackLinks list correctly produces "orphan = true".
func TestOrphanPredicateGraphMissingNoteEntry(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Alone.md": "---\n---\nNo links anywhere.\n",
	})
	all := []*vault.Note{notes["Alone.md"]}
	g := BuildGraph(all)
	if !Orphan(g)(notes["Alone.md"]) {
		t.Errorf("Orphan should match a note with no inbound links")
	}
}

// TestStringifyTagRFC3339Timestamp pins down the RFC3339 path of stringifyTag
// (non-bare YAML timestamps with a time-of-day component). A note whose tags
// list includes a fully-qualified timestamp should be queryable via the same
// RFC3339 string the user would see if they dumped the frontmatter.

func TestBuildGraphDuplicateTitlesKeepDistinctPathKeys(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Foo.md":     "---\n---\nbody\n",
		"sub/foo.md": "---\n---\nbody\n",
	})
	all := []*vault.Note{notes["Foo.md"], notes["sub/foo.md"]}
	g := BuildGraph(all)
	if _, ok := g.Titles["foo"]; !ok {
		t.Errorf("expected root note key 'foo' in Titles")
	}
	if _, ok := g.Titles["sub/foo"]; !ok {
		t.Errorf("expected folder-qualified note key 'sub/foo' in Titles")
	}
	if p := g.PathsByTitle["foo"]; p != notes["Foo.md"].Path {
		t.Errorf("PathsByTitle[foo] = %q, want %q", p, notes["Foo.md"].Path)
	}
	if p := g.PathsByTitle["sub/foo"]; p != notes["sub/foo.md"].Path {
		t.Errorf("PathsByTitle[sub/foo] = %q, want %q", p, notes["sub/foo.md"].Path)
	}
}
