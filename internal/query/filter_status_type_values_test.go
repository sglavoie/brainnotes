package query

import (
	"testing"
)

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

// TestStatusNullValueDoesNotMatchEmpty verifies that a YAML null `status: ~`
// value does not produce a spurious match for non-empty queries. The
// stringifyTag(nil) fallback returns "", so a programmer-supplied empty query
// would match — but the public flag path filters empties at FromFlags, so
// this pins down the user-facing contract: null status matches no real query.
func TestStatusNullValueDoesNotMatchEmpty(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\nstatus: ~\n---\nbody\n",
	})
	if Status("reading")(notes["a.md"]) {
		t.Errorf("Status(reading) should not match null status")
	}
	if Status("anything")(notes["a.md"]) {
		t.Errorf("Status(anything) should not match null status")
	}
}

// TestTypeNullValueDoesNotMatchEmpty mirrors the null-status check for type.

// TestTypeNullValueDoesNotMatchEmpty mirrors the null-status check for type.
func TestTypeNullValueDoesNotMatchEmpty(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntype: ~\nkind: ~\n---\nbody\n",
	})
	if Type("goal")(notes["a.md"]) {
		t.Errorf("Type(goal) should not match null type/kind")
	}
}

// TestBrokenLinksPredicateCleanNote verifies that BrokenLinks returns false
// when every link in the note resolves. The predicate exists primarily as the
// match-side of UnresolvedLinks; this pins down the "no broken links" branch.
