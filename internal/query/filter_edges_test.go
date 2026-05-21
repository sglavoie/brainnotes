package query

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
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
func TestPredicatesAbsentKey(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"empty.md": "no frontmatter at all\n",
		"other.md": "---\ntitle: o\n---\nbody\n",
	})
	for _, name := range []string{"empty.md", "other.md"} {
		n := notes[name]
		if Tag("foo")(n) {
			t.Errorf("Tag(foo) should not match %s (no tags)", name)
		}
		if Category("books")(n) {
			t.Errorf("Category(books) should not match %s (no categories)", name)
		}
		if Status("reading")(n) {
			t.Errorf("Status(reading) should not match %s (no status)", name)
		}
		if Type("goal")(n) {
			t.Errorf("Type(goal) should not match %s (no type/kind)", name)
		}
		if Has("status")(n) {
			t.Errorf("Has(status) should not match %s", name)
		}
		if !Missing("status")(n) {
			t.Errorf("Missing(status) should match %s", name)
		}
		if Age(">", 24*time.Hour, "created")(n) {
			t.Errorf("Age should not match %s (no date)", name)
		}
	}
}

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
func TestCategoryScalarWikilink(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ncategories: \"[[Books]]\"\n---\nbody\n",
		"b.md": "---\ncategories: \"[[People]]\"\n---\nbody\n",
	})
	if !Category("books")(notes["a.md"]) {
		t.Errorf("Category(books) should match a scalar `categories: [[Books]]`")
	}
	if Category("books")(notes["b.md"]) {
		t.Errorf("Category(books) should not match a scalar `categories: [[People]]`")
	}
}

// TestAgeFutureDate documents how the predicate handles a date in the future
// (e.g. a note dated tomorrow). `time.Since` returns a negative duration for
// future timestamps, so `>30d` rejects it (a future note is not "older than
// 30 days") while `<30d` accepts it (it is trivially "younger than 30 days").
// `=0d` rejects unless today.
func TestAgeFutureDate(t *testing.T) {
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	_, notes := newVault(t, map[string]string{
		"future.md": "---\ncreated: " + tomorrow + "\n---\n",
	})
	if Age(">", 30*24*time.Hour, "created")(notes["future.md"]) {
		t.Errorf(">30d should not match a future note")
	}
	if !Age("<", 30*24*time.Hour, "created")(notes["future.md"]) {
		t.Errorf("<30d should match a future note (negative since)")
	}
	if Age("=", 0, "created")(notes["future.md"]) {
		t.Errorf("=0d should not match tomorrow's note")
	}
}

// TestAgeBogusOp pins down the safety net for an unknown comparison operator
// passed directly to Age (bypassing ParseAge). The predicate returns false
// rather than panicking on a typo or programmer error.
func TestAgeBogusOp(t *testing.T) {
	today := time.Now().Format("2006-01-02")
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ncreated: " + today + "\n---\n",
	})
	if Age("!?", 0, "created")(notes["a.md"]) {
		t.Errorf("Age with unknown op should return false")
	}
}

// TestHasAllowsNullValue documents that Has reports presence even when the
// value is YAML null. This matches the broader frontmatter model: nil-valued
// keys are present but empty, and `bn unset` is the only operation that
// removes a key entirely. Has and Missing must therefore be exact inverses on
// every note that loads.
func TestHasAllowsNullValue(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags: ~\n---\nbody\n",
		"b.md": "---\ntags: []\n---\nbody\n",
	})
	for _, name := range []string{"a.md", "b.md"} {
		n := notes[name]
		if !Has("tags")(n) {
			t.Errorf("Has(tags) should be true for %s (key is present, value is null/empty)", name)
		}
		if Missing("tags")(n) {
			t.Errorf("Missing(tags) should be false for %s", name)
		}
	}
}

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
func TestTagPredicateRejectsNilNote(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Tag should not panic on nil note, got: %v", r)
		}
	}()
	if Tag("foo")(nil) {
		t.Errorf("Tag(foo)(nil) = true, want false")
	}
	if Category("books")(nil) {
		t.Errorf("Category(books)(nil) = true, want false")
	}
	if Status("reading")(nil) {
		t.Errorf("Status(reading)(nil) = true, want false")
	}
	if Type("goal")(nil) {
		t.Errorf("Type(goal)(nil) = true, want false")
	}
	if Has("status")(nil) {
		t.Errorf("Has(status)(nil) = true, want false")
	}
	if Missing("status")(nil) {
		t.Errorf("Missing(status)(nil) = true, want false")
	}
	if Age(">", 24*time.Hour, "created")(nil) {
		t.Errorf("Age(nil) = true, want false")
	}
}

// TestTagPredicateEmptyList asserts that an empty tags list (`tags: []`) never
// matches any tag query. Before the fix this was already correct; the test
// pins down the behaviour against regressions where an empty list might be
// confused for "matches anything".
func TestTagPredicateEmptyList(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags: []\n---\nbody\n",
	})
	if Tag("foo")(notes["a.md"]) {
		t.Errorf("Tag(foo) should not match a note with empty tags list")
	}
	if Tag("")(notes["a.md"]) {
		t.Errorf("Tag(empty) should not match a note with empty tags list")
	}
}

// TestTagPredicateNullValue asserts that `tags: null` (a YAML null value) does
// not match any tag query, including the empty string. The key is present but
// has no value to match against.
func TestTagPredicateNullValue(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags: ~\n---\nbody\n",
	})
	if Tag("foo")(notes["a.md"]) {
		t.Errorf("Tag(foo) should not match `tags: null`")
	}
}

// TestCategoryPredicateEmptyList asserts that empty `categories: []` matches no
// category query, mirroring TestTagPredicateEmptyList.
func TestCategoryPredicateEmptyList(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ncategories: []\n---\nbody\n",
	})
	if Category("books")(notes["a.md"]) {
		t.Errorf("Category(books) should not match an empty categories list")
	}
}

// TestCategoryPredicateNullValue asserts that `categories: null` is handled
// gracefully and does not match any category. GetWikilinkList returns an empty
// slice (not nil) for a null value, so the empty-loop returns false correctly.
func TestCategoryPredicateNullValue(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ncategories: ~\n---\nbody\n",
	})
	if Category("books")(notes["a.md"]) {
		t.Errorf("Category(books) should not match `categories: null`")
	}
}

// TestTypeMatchesEitherTypeOrKind verifies that when a note has both `type`
// and `kind` set, a query for either value matches. The doc comment explicitly
// states "accepts either so queries don't need to know the difference" — this
// pins that OR semantics down.
func TestTypeMatchesEitherTypeOrKind(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"both.md": "---\ntype: Article\nkind: Project\n---\nbody\n",
	})
	if !Type("article")(notes["both.md"]) {
		t.Errorf("Type(article) should match when `type: Article` is set")
	}
	if !Type("project")(notes["both.md"]) {
		t.Errorf("Type(project) should match when `kind: Project` is set (either-or semantics)")
	}
	if Type("other")(notes["both.md"]) {
		t.Errorf("Type(other) should not match")
	}
}

// TestStatusAndTypeMatchNonStringValues restores regression coverage for
// auto-coerced non-string status/type/kind values so the same textual query
// still round-trips after `bn set` writes an int or bool.
func TestStatusAndTypeMatchNonStringValues(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"int.md":   "---\nstatus: 5\ntype: 7\n---\nbody\n",
		"bool.md":  "---\nstatus: true\nkind: false\n---\nbody\n",
		"plain.md": "---\nstatus: Reading\n---\nbody\n",
	})
	if !Status("5")(notes["int.md"]) {
		t.Errorf("Status(5) should match an int-valued status")
	}
	if !Type("7")(notes["int.md"]) {
		t.Errorf("Type(7) should match an int-valued type")
	}
	if !Status("true")(notes["bool.md"]) {
		t.Errorf("Status(true) should match a bool-valued status")
	}
	if !Type("false")(notes["bool.md"]) {
		t.Errorf("Type(false) should match a bool-valued kind fallback")
	}
	if !Status("reading")(notes["plain.md"]) {
		t.Errorf("Status(reading) should still match a plain string status")
	}
}

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
func TestMissingIsStrictComplementOfHasOnParseError(t *testing.T) {
	// Write a file whose frontmatter is a YAML sequence at the document root,
	// which the frontmatter parser rejects ("top-level must be a mapping").
	// Use a bare *vault.Note so we can bypass Vault.Notes (which fails fast on
	// parse errors) and exercise the predicate's own error path.
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.md")
	if err := os.WriteFile(path, []byte("---\n  - x\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	n := &vault.Note{Path: path}
	if Has("anything")(n) {
		t.Errorf("Has(anything) should be false when frontmatter fails to parse")
	}
	if !Missing("anything")(n) {
		t.Errorf("Missing(anything) should be true when frontmatter fails to parse (strict complement of Has)")
	}
}

// TestMissingRejectsNilNote verifies that Missing rejects a nil note rather
// than treating it as "frontmatter failed to load, so the key is absent". A
// nil note isn't a real note and should match no predicate.
func TestMissingRejectsNilNote(t *testing.T) {
	if Missing("x")(nil) {
		t.Errorf("Missing(x)(nil) = true, want false")
	}
	if Has("x")(nil) {
		t.Errorf("Has(x)(nil) = true, want false")
	}
}

// TestParseAgeRoundTripsAllUnits verifies every documented unit (s, m, h, d)
// is accepted with a non-zero value.
func TestParseAgeRoundTripsAllUnits(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{">10s", 10 * time.Second},
		{"<3m", 3 * time.Minute},
		{"=4h", 4 * time.Hour},
		{">2d", 2 * 24 * time.Hour},
	}
	for _, c := range cases {
		_, dur, err := ParseAge(c.in)
		if err != nil {
			t.Errorf("ParseAge(%q) unexpected err: %v", c.in, err)
		}
		if dur != c.want {
			t.Errorf("ParseAge(%q) dur=%v, want %v", c.in, dur, c.want)
		}
	}
}

// TestStatusPredicateFloatStringification asserts that a YAML-typed float value
// in the frontmatter status field is queryable using the same textual form the
// user would type. Previously stringifyTag used "%v" for float64, which drops
// the trailing ".0" on whole-number floats — so `bn find --status 3.0` against
// a note whose frontmatter held `status: 3.0` (a YAML float scalar) silently
// returned no results. The fix renders floats with their decimal marker
// preserved so `3.0` round-trips through query stringification.
func TestStatusPredicateFloatStringification(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"whole.md":  "---\nstatus: 3.0\n---\nbody\n",
		"frac.md":   "---\nstatus: 1.5\n---\nbody\n",
		"strnum.md": "---\nstatus: \"3.0\"\n---\nbody\n",
	})
	if !Status("3.0")(notes["whole.md"]) {
		t.Errorf("Status(3.0) should match a note whose frontmatter holds the float 3.0")
	}
	if !Status("1.5")(notes["frac.md"]) {
		t.Errorf("Status(1.5) should match a note whose frontmatter holds the float 1.5")
	}
	if !Status("3.0")(notes["strnum.md"]) {
		t.Errorf("Status(3.0) should still match a string-typed \"3.0\" status (backcompat)")
	}
}

// TestTagPredicateFloatStringification mirrors the float-stringification fix
// for the tag predicate so that a tags list containing a YAML float scalar
// (e.g. `tags: [3.0]`) is queryable as `--tag 3.0`. The previous "%v" path
// would format the float as "3" and the textual `3.0` query would silently
// miss it.
func TestTagPredicateFloatStringification(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"list.md":   "---\ntags:\n  - 3.0\n---\nbody\n",
		"scalar.md": "---\ntags: 3.0\n---\nbody\n",
	})
	if !Tag("3.0")(notes["list.md"]) {
		t.Errorf("Tag(3.0) should match a tags list holding the float 3.0")
	}
	if !Tag("3.0")(notes["scalar.md"]) {
		t.Errorf("Tag(3.0) should match a scalar float tag value 3.0")
	}
}
