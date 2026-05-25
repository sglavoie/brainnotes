package query

import (
	"testing"
)

// TestFolderPredicateNoFalsePrefix verifies that the Folder predicate does not
// match notes in sibling folders whose name shares a prefix with the requested
// folder. Without the trailing "/" in the prefix check, `--folder Refs` would
// erroneously match `Refs2/a.md`.
func TestFolderPredicateNoFalsePrefix(t *testing.T) {
	v, notes := newVault(t, map[string]string{
		"Refs/a.md":  "# a\n",
		"Refs2/b.md": "# b\n",
		"Refsy/c.md": "# c\n",
	})
	p := Folder("Refs", v.Root)
	if !p(notes["Refs/a.md"]) {
		t.Errorf("Folder(Refs) should match Refs/a.md")
	}
	if p(notes["Refs2/b.md"]) {
		t.Errorf("Folder(Refs) should not match Refs2/b.md (sibling with shared prefix)")
	}
	if p(notes["Refsy/c.md"]) {
		t.Errorf("Folder(Refs) should not match Refsy/c.md (sibling with shared prefix)")
	}
}

// TestPredicatesRejectErrorFront verifies that predicates which read frontmatter
// degrade gracefully when Front() returns an error or the requested key is
// absent. We use notes that have neither frontmatter nor the target key.

// TestFolderTrimsWhitespace verifies that a folder argument padded with spaces
// (e.g. from a shell that quoted the value with stray padding) is normalized.
// Before the fix, `--folder "  References  "` retained the surrounding spaces
// after path.Clean (which only collapses path syntax, not whitespace), so the
// predicate matched a literal folder named "  References  " and never matched
// the real "References" folder.
func TestFolderTrimsWhitespace(t *testing.T) {
	v, notes := newVault(t, map[string]string{
		"References/a.md": "# a\n",
		"Other/b.md":      "# b\n",
	})
	for _, raw := range []string{"  References  ", "\tReferences\t", " References/"} {
		p := Folder(raw, v.Root)
		if !p(notes["References/a.md"]) {
			t.Errorf("Folder(%q) should match References/a.md", raw)
		}
		if p(notes["Other/b.md"]) {
			t.Errorf("Folder(%q) should not match Other/b.md", raw)
		}
	}
	// All-whitespace folder normalizes to empty -> match everything.
	for _, raw := range []string{"   ", "\t", " / "} {
		p := Folder(raw, v.Root)
		if !p(notes["References/a.md"]) || !p(notes["Other/b.md"]) {
			t.Errorf("Folder(%q) should match every note (normalizes to empty)", raw)
		}
	}
}

// TestCategoryScalarWikilink verifies that a scalar (non-list) categories
// value like `categories: "[[Books]]"` matches Category("books"). The
// frontmatter helper supports this, but a regression in either layer would
// break the convenient single-category notation.

// TestFolderPredicateRejectsNilNote verifies that the Folder predicate returns
// false for nil notes, including when the folder is empty (matches everything
// for non-nil notes). Before the fix, `Folder("", root)(nil)` returned true
// because the early-return for the empty-folder case did not guard against nil.
func TestFolderPredicateRejectsNilNote(t *testing.T) {
	v, _ := newVault(t, map[string]string{
		"a.md": "# a\n",
	})
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Folder predicate should not panic on nil note, got: %v", r)
		}
	}()
	// Non-empty folder with nil note.
	if Folder("People", v.Root)(nil) {
		t.Errorf("Folder(People)(nil) = true, want false")
	}
	// Empty folder (match-all) with nil note — this was the bug.
	if Folder("", v.Root)(nil) {
		t.Errorf("Folder(\"\")(nil) = true, want false")
	}
	// Whitespace-only folder normalizes to empty — same bug path.
	if Folder("   ", v.Root)(nil) {
		t.Errorf("Folder(\"   \")(nil) = true, want false")
	}
	// Dot folder normalizes to empty.
	if Folder(".", v.Root)(nil) {
		t.Errorf("Folder(\".\")(nil) = true, want false")
	}
}

// TestTagPredicateRejectsNilNote verifies that the Tag predicate returns false
// for a nil note rather than panicking. The frontmatter-reading predicates rely
// on Note.Load() to return an error for a nil note; this asserts that contract.

// TestFolderNestedDescendantMatch verifies the descendant-match contract: a
// folder filter matches notes in any subfolder, however deeply nested.
func TestFolderNestedDescendantMatch(t *testing.T) {
	v, notes := newVault(t, map[string]string{
		"References/a.md":                 "# a\n",
		"References/Books/b.md":           "# b\n",
		"References/Books/SciFi/c.md":     "# c\n",
		"References/Books/SciFi/d/e.md":   "# e\n",
		"References/Books/SciFi/d/f/g.md": "# g\n",
	})
	p := Folder("References", v.Root)
	for _, path := range []string{
		"References/a.md",
		"References/Books/b.md",
		"References/Books/SciFi/c.md",
		"References/Books/SciFi/d/e.md",
		"References/Books/SciFi/d/f/g.md",
	} {
		if !p(notes[path]) {
			t.Errorf("Folder(References) should match deeply-nested %s", path)
		}
	}
}

// TestFolderPredicateRejectsParentEscape verifies that `..` segments in a
// folder query are normalized but do not allow escaping the vault root: a query
// that path.Cleans to a `..` prefix matches nothing real.

// TestFolderPredicateRejectsParentEscape verifies that `..` segments in a
// folder query are normalized but do not allow escaping the vault root: a query
// that path.Cleans to a `..` prefix matches nothing real.
func TestFolderPredicateRejectsParentEscape(t *testing.T) {
	v, notes := newVault(t, map[string]string{
		"People/a.md": "# a\n",
		"Other/b.md":  "# b\n",
	})
	// "People/.." -> "."  -> "" -> match all.
	if !Folder("People/..", v.Root)(notes["People/a.md"]) {
		t.Errorf("Folder(People/..) normalizes to empty and should match all")
	}
	// "../Other" stays as "../Other" after Clean — should match nothing.
	if Folder("../Other", v.Root)(notes["Other/b.md"]) {
		t.Errorf("Folder(../Other) should not match a real folder (parent escape)")
	}
}

// TestMissingIsStrictComplementOfHasOnParseError verifies that Missing returns
// true when the note's frontmatter cannot be parsed, so that Has and Missing
// partition every loadable note exactly. Before the fix both returned false on
// a YAML parse error, leaving the note in neither set.

// TestFolderInteriorDotSegments verifies that interior `./` and `a/./b`
// segments in a folder argument are normalized via path.Clean.
func TestFolderInteriorDotSegments(t *testing.T) {
	v, notes := newVault(t, map[string]string{
		"a/b/c.md": "# c\n",
		"a/d.md":   "# d\n",
	})
	for _, raw := range []string{"a/./b", "a//b", "a/b/.", "./a/./b/."} {
		p := Folder(raw, v.Root)
		if !p(notes["a/b/c.md"]) {
			t.Errorf("Folder(%q) should match a/b/c.md", raw)
		}
		if p(notes["a/d.md"]) {
			t.Errorf("Folder(%q) should not match a/d.md (different subfolder)", raw)
		}
	}
}

// TestAndOrDeeplyNested pins down that combinators compose to arbitrary depth
// without losing short-circuit behaviour. The previous combinator tests cover
// one level of nesting; this exercises three levels.

// TestFolderPredicateRootNoteWithFolderFilter verifies that a note sitting at
// the vault root (Folder() returns "") is correctly rejected when the folder
// filter is non-empty. Before the fix to this audit's predecessors, an empty
// `nf` could match a non-empty `want` because of a bad HasPrefix call.
func TestFolderPredicateRootNoteWithFolderFilter(t *testing.T) {
	v, notes := newVault(t, map[string]string{
		"root.md":     "# r\n",
		"People/a.md": "# a\n",
	})
	if Folder("People", v.Root)(notes["root.md"]) {
		t.Errorf("Folder(People) should not match a note at the vault root")
	}
	// And the inverse: an empty-folder filter matches the root note.
	if !Folder("", v.Root)(notes["root.md"]) {
		t.Errorf("Folder(\"\") should match the root note (vacuous filter)")
	}
}

// TestAgeEqualSubDayDuration pins down the documented day-granularity of the
// `=` operator: a sub-day duration like `=12h` truncates to 0 days, so it
// behaves as `=0d` (matches today). The doc comment says "note's age in days
// equals dur in days" and gives `=0d → today` as the canonical example; this
// test asserts the truncation direction.
