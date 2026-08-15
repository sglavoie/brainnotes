package query

import (
	"path/filepath"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func TestBuildGraphBacklinks(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Apple.md":  "Body links to [[Banana]] and [[Cherry|Cherries]].\n",
		"Banana.md": "---\ncategories:\n  - \"[[Apple]]\"\n---\nBody.\n",
		"Cherry.md": "---\ntitle: c\n---\nNo links here.\n",
		"Lonely.md": "---\ntitle: l\n---\nNothing references me.\n",
	})

	all := []*vault.Note{notes["Apple.md"], notes["Banana.md"], notes["Cherry.md"], notes["Lonely.md"]}
	g := BuildGraph(all)

	if len(g.BackLinks("banana")) != 1 {
		t.Errorf("expected one referrer for Banana, got %v", g.BackLinks("banana"))
	}
	if len(g.BackLinks("cherry")) != 1 {
		t.Errorf("expected one referrer for Cherry, got %v", g.BackLinks("cherry"))
	}
	if len(g.BackLinks("apple")) != 1 {
		t.Errorf("expected one referrer for Apple (from Banana frontmatter), got %v", g.BackLinks("apple"))
	}
	if len(g.BackLinks("lonely")) != 0 {
		t.Errorf("Lonely should have no backlinks, got %v", g.BackLinks("lonely"))
	}

	orphan := Orphan(g)
	if orphan(notes["Apple.md"]) {
		t.Errorf("Apple has an incoming link; not orphan")
	}
	if !orphan(notes["Lonely.md"]) {
		t.Errorf("Lonely should be orphan")
	}
}

// TestOrphanIgnoresSelfReferences verifies that a note whose only "inbound"
// wikilink is to itself still counts as an orphan. Before the fix, Orphan
// matched any non-empty entry in idx[title], so a self-link silently kept the
// note out of the orphan set. `bn backlinks` already filters self-references;
// `--orphan` should behave consistently.
func TestOrphanIgnoresSelfReferences(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"SelfOnly.md":   "---\n---\nI link to [[SelfOnly]] only.\n",
		"Referenced.md": "---\n---\nBody.\n",
		"Referrer.md":   "---\n---\nLinks to [[Referenced]].\n",
	})
	all := []*vault.Note{notes["SelfOnly.md"], notes["Referenced.md"], notes["Referrer.md"]}
	g := BuildGraph(all)
	o := Orphan(g)

	if !o(notes["SelfOnly.md"]) {
		t.Errorf("SelfOnly should be an orphan: its only inbound link is from itself")
	}
	if o(notes["Referenced.md"]) {
		t.Errorf("Referenced has an external inbound link; not orphan")
	}
	if !o(notes["Referrer.md"]) {
		t.Errorf("Referrer has no inbound link; orphan")
	}
}

// Smoke check that the path of a referrer is recorded correctly.
func TestBacklinkPathsRecorded(t *testing.T) {
	v, notes := newVault(t, map[string]string{
		"Source.md": "Reference to [[Target]].\n",
		"Target.md": "# t\n",
	})
	g := BuildGraph([]*vault.Note{notes["Source.md"], notes["Target.md"]})
	refs := g.BackLinks("target")
	if len(refs) != 1 {
		t.Fatalf("want 1 ref, got %d", len(refs))
	}
	rel, _ := filepath.Rel(v.Root, refs[0])
	if filepath.ToSlash(rel) != "Source.md" {
		t.Errorf("ref path = %s, want Source.md", rel)
	}

}

// TestOrphanRejectsNilNote pins down the defensive nil-note guard. Other
// predicates rely on the And/Or wrapper to reject nil before they ever run,
// but Orphan does not call Front() and previously dereferenced n.Path through
// n.Title() — which panics on a nil *Note. The guard makes Orphan consistent
// with And/Or (false on nil) so callers can compose it safely.
func TestOrphanRejectsNilNote(t *testing.T) {
	g := BuildGraph(nil)
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Orphan should not panic on nil note, got panic: %v", r)
		}
	}()
	if Orphan(g)(nil) {
		t.Errorf("Orphan(g)(nil) = true, want false")
	}
}

// TestOrphanLinkedOnlyByAlias verifies that a link of the form
// `[[Target|some alias]]` correctly marks Target as non-orphan. The alias is a
// display label only — Title() must extract Target, not Alias. A bug in
// wikilink.Parse that mixed those up would cause Orphan to misclassify aliased
// notes as orphans.
func TestOrphanLinkedOnlyByAlias(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Target.md":   "---\n---\nbody\n",
		"Referrer.md": "---\n---\nSee [[Target|the target]] for details.\n",
	})
	all := []*vault.Note{notes["Target.md"], notes["Referrer.md"]}
	g := BuildGraph(all)
	if Orphan(g)(notes["Target.md"]) {
		t.Errorf("Target should not be an orphan: Referrer links it via an alias")
	}
	if len(g.BackLinks("the target")) != 0 {
		t.Errorf("alias text should not produce a backlink entry")
	}
}

// TestOrphanLinkedByFolderPrefixedWikilink verifies that a link of the form
// `[[Folder/Target]]` correctly marks Target as non-orphan. Orphan looks up by
// the note's lowercased title (filename stem), and Link.Title() must strip the
// folder prefix, otherwise a folder-prefixed link would never resolve.
func TestOrphanLinkedByFolderPrefixedWikilink(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Books/Target.md": "---\n---\nbody\n",
		"Referrer.md":     "---\n---\nSee [[Books/Target]].\n",
	})
	all := []*vault.Note{notes["Books/Target.md"], notes["Referrer.md"]}
	g := BuildGraph(all)
	if Orphan(g)(notes["Books/Target.md"]) {
		t.Errorf("Books/Target should not be an orphan: Referrer links it with a folder prefix")
	}
}

func TestOrphanDuplicateTitlesUseExactFolderQualifiedBacklinks(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Foo.md":       "---\n---\nbody\n",
		"Areas/Foo.md": "---\n---\nbody\n",
		"Referrer.md":  "---\n---\nSee [[Areas/Foo]].\n",
	})
	all := []*vault.Note{notes["Foo.md"], notes["Areas/Foo.md"], notes["Referrer.md"]}
	g := BuildGraph(all)

	if !Orphan(g)(notes["Foo.md"]) {
		t.Errorf("root Foo should remain orphan: only Areas/Foo is linked")
	}
	if Orphan(g)(notes["Areas/Foo.md"]) {
		t.Errorf("Areas/Foo should not be orphan: Referrer links its exact folder-qualified path")
	}
}

func TestMinBacklinks(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Hub.md":    "---\n---\nbody\n",
		"A.md":      "---\n---\n[[Hub]] link.\n",
		"B.md":      "---\n---\n[[Hub]] link.\n",
		"C.md":      "---\n---\n[[Hub]] link.\n",
		"Lonely.md": "---\n---\nNo links.\n",
	})
	all := []*vault.Note{notes["Hub.md"], notes["A.md"], notes["B.md"], notes["C.md"], notes["Lonely.md"]}
	g := BuildGraph(all)

	if !MinBacklinks(g, 2)(notes["Hub.md"]) {
		t.Errorf("Hub has 3 inbound; MinBacklinks(2) should match")
	}
	if MinBacklinks(g, 4)(notes["Hub.md"]) {
		t.Errorf("Hub has 3 inbound; MinBacklinks(4) should not match")
	}
	if MinBacklinks(g, 1)(notes["Lonely.md"]) {
		t.Errorf("Lonely has 0 inbound; MinBacklinks(1) should not match")
	}
}

func TestMaxBacklinks(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Target.md":   "---\n---\nbody\n",
		"Referrer.md": "---\n---\n[[Target]] link.\n",
		"Lonely.md":   "---\n---\nNo links.\n",
	})
	all := []*vault.Note{notes["Target.md"], notes["Referrer.md"], notes["Lonely.md"]}
	g := BuildGraph(all)

	if !MaxBacklinks(g, 2)(notes["Target.md"]) {
		t.Errorf("Target has 1 inbound; MaxBacklinks(2) should match")
	}
	if MaxBacklinks(g, 0)(notes["Target.md"]) {
		t.Errorf("Target has 1 inbound; MaxBacklinks(0) should not match")
	}
	if !MaxBacklinks(g, 0)(notes["Lonely.md"]) {
		t.Errorf("Lonely has 0 inbound; MaxBacklinks(0) should match")
	}
}

func TestMinLinks(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Hub.md": "---\n---\n[[A]] [[B]] [[C]] [[D]] [[E]]\n",
		"A.md":   "---\n---\nbody\n",
		"B.md":   "---\n---\nbody\n",
		"C.md":   "---\n---\nbody\n",
		"D.md":   "---\n---\nbody\n",
		"E.md":   "---\n---\nbody\n",
	})
	all := []*vault.Note{notes["Hub.md"], notes["A.md"], notes["B.md"], notes["C.md"], notes["D.md"], notes["E.md"]}
	g := BuildGraph(all)

	if !MinLinks(g, 3)(notes["Hub.md"]) {
		t.Errorf("Hub has 5 outgoing; MinLinks(3) should match")
	}
	if MinLinks(g, 1)(notes["A.md"]) {
		t.Errorf("A has 0 outgoing; MinLinks(1) should not match")
	}
}

func TestMaxLinks(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Hub.md":  "---\n---\n[[A]] [[B]]\n",
		"A.md":    "---\n---\nbody\n",
		"B.md":    "---\n---\nbody\n",
		"Dead.md": "---\n---\nNo outgoing links.\n",
	})
	all := []*vault.Note{notes["Hub.md"], notes["A.md"], notes["B.md"], notes["Dead.md"]}
	g := BuildGraph(all)

	if !MaxLinks(g, 0)(notes["Dead.md"]) {
		t.Errorf("Dead has 0 outgoing; MaxLinks(0) should match")
	}
	if MaxLinks(g, 0)(notes["Hub.md"]) {
		t.Errorf("Hub has 2 outgoing; MaxLinks(0) should not match")
	}
}

func TestDeadEnd(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Linker.md":      "---\n---\n[[Target]]\n",
		"Target.md":      "---\n---\nbody\n",
		"Island.md":      "---\n---\nNo links at all.\n",
		"True.md":        "---\ndead_end: true\n---\nNo links.\n",
		"Exempt.md":      "---\ndead_end: false\n---\nNo links.\n",
		"Null.md":        "---\ndead_end: null\n---\nNo links.\n",
		"StringFalse.md": "---\ndead_end: \"false\"\n---\nNo links.\n",
		"Number.md":      "---\ndead_end: 0\n---\nNo links.\n",
	})
	all := make([]*vault.Note, 0, len(notes))
	for _, note := range notes {
		all = append(all, note)
	}
	g := BuildGraph(all)
	deadEnd := DeadEnd(g)

	if deadEnd(notes["Linker.md"]) {
		t.Errorf("Linker has outgoing links; should not be dead end")
	}
	if !deadEnd(notes["Target.md"]) {
		t.Errorf("Target has no outgoing links; should be dead end")
	}
	if !deadEnd(notes["Island.md"]) {
		t.Errorf("Island has no outgoing links; should be dead end")
	}
	if deadEnd(notes["Exempt.md"]) {
		t.Errorf("boolean dead_end: false should exempt a note")
	}
	for _, name := range []string{"True.md", "Null.md", "StringFalse.md", "Number.md"} {
		if !deadEnd(notes[name]) {
			t.Errorf("%s should remain a dead end because only boolean false exempts", name)
		}
	}
}

func TestDeadEndExemptionDoesNotChangeRawLinkCount(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Exempt.md": "---\ndead_end: false\n---\nNo links.\n",
	})
	all := []*vault.Note{notes["Exempt.md"]}
	g := BuildGraph(all)

	if DeadEnd(g)(notes["Exempt.md"]) {
		t.Error("exempt note should not be classified as a dead end")
	}
	if !MaxLinks(g, 0)(notes["Exempt.md"]) {
		t.Error("exempt note should still match MaxLinks(0)")
	}
	if got := g.OutDegreeForNote(notes["Exempt.md"]); got != 0 {
		t.Errorf("exempt note out-degree = %d, want 0", got)
	}
}

func TestLinkPredicatesSelfLinkExcluded(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Self.md":  "---\n---\n[[Self]] only links to itself.\n",
		"Other.md": "---\n---\n[[Self]]\n",
	})
	all := []*vault.Note{notes["Self.md"], notes["Other.md"]}
	g := BuildGraph(all)

	if g.InDegree("self") != 1 {
		t.Errorf("Self should have 1 inbound (from Other), got %d", g.InDegree("self"))
	}
	if g.OutDegree("self") != 0 {
		t.Errorf("Self should have 0 outgoing (self-link excluded), got %d", g.OutDegree("self"))
	}
	if !DeadEnd(g)(notes["Self.md"]) {
		t.Errorf("Self-link only: should be a dead end")
	}
}

func TestLinkPredicatesRejectNil(t *testing.T) {
	g := BuildGraph(nil)
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("link predicates should not panic on nil note, got: %v", r)
		}
	}()
	if MinBacklinks(g, 0)(nil) {
		t.Errorf("MinBacklinks should reject nil")
	}
	if MaxBacklinks(g, 0)(nil) {
		t.Errorf("MaxBacklinks should reject nil")
	}
	if MinLinks(g, 0)(nil) {
		t.Errorf("MinLinks should reject nil")
	}
	if MaxLinks(g, 0)(nil) {
		t.Errorf("MaxLinks should reject nil")
	}
	if DeadEnd(g)(nil) {
		t.Errorf("DeadEnd should reject nil")
	}
}

// TestOrphanPredicateGraphLookupCaseInsensitive verifies that Orphan correctly
// finds a backlink even when the note title's case differs from the link text's
// case. The lookup must be case-folded both directions.
func TestOrphanPredicateGraphLookupCaseInsensitive(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Target.md":   "---\n---\nbody\n",
		"Referrer.md": "---\n---\nSee [[target]] (lowercase link text).\n",
	})
	all := []*vault.Note{notes["Target.md"], notes["Referrer.md"]}
	g := BuildGraph(all)
	if Orphan(g)(notes["Target.md"]) {
		t.Errorf("Target should not be an orphan: Referrer links it (case-insensitive)")
	}
}
