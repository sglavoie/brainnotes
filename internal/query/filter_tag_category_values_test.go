package query

import (
	"testing"
)

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

// TestTagPredicateInt64Element verifies that a tag stored as a YAML number
// large enough to be parsed as int64 (rather than int) still matches a textual
// query. On 32-bit platforms the YAML parser uses int64 for values that don't
// fit in int.
func TestTagPredicateInt64Element(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"big.md": "---\ntags:\n  - 9999999999\n---\nbody\n",
	})
	if !Tag("9999999999")(notes["big.md"]) {
		t.Errorf("Tag(9999999999) should match a large-int tag (int64-typed)")
	}
}

// TestCategoryPredicateUnbracketedScalar verifies that a categories value
// without `[[ ]]` wrappers is still matched. The frontmatter helper passes
// non-wikilink strings through unchanged, so plain `categories: Books` should
// match Category("books") case-insensitively.

// TestCategoryPredicateUnbracketedScalar verifies that a categories value
// without `[[ ]]` wrappers is still matched. The frontmatter helper passes
// non-wikilink strings through unchanged, so plain `categories: Books` should
// match Category("books") case-insensitively.
func TestCategoryPredicateUnbracketedScalar(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ncategories: Books\n---\nbody\n",
		"b.md": "---\ncategories:\n  - Books\n  - People\n---\nbody\n",
	})
	if !Category("books")(notes["a.md"]) {
		t.Errorf("Category(books) should match unbracketed scalar `categories: Books`")
	}
	if !Category("people")(notes["b.md"]) {
		t.Errorf("Category(people) should match unbracketed list entry")
	}
	if Category("missing")(notes["a.md"]) {
		t.Errorf("Category(missing) should not match a different category")
	}
}

// TestCategoryPredicateMatchesWikilinkTargetTitle verifies that Category
// compares against the wikilink target title, not the raw `[[...]]` payload.
// Headings, aliases, folder prefixes, and `.md` suffixes should not stop a
// category query from finding the underlying target note.
func TestCategoryPredicateMatchesWikilinkTargetTitle(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"scalar.md": "---\ncategories: \"[[Library/Books.md#Now|To Read]]\"\n---\nbody\n",
		"list.md":   "---\ncategories:\n  - \"[[People#Team|Core]]\"\n---\nbody\n",
	})
	if !Category("books")(notes["scalar.md"]) {
		t.Errorf("Category(books) should match wikilink target [[Library/Books.md#Now|To Read]]")
	}
	if Category("to read")(notes["scalar.md"]) {
		t.Errorf("Category(to read) should not match the wikilink alias")
	}
	if Category("now")(notes["scalar.md"]) {
		t.Errorf("Category(now) should not match the wikilink heading")
	}
	if !Category("people")(notes["list.md"]) {
		t.Errorf("Category(people) should match wikilink target [[People#Team|Core]]")
	}
}

// TestStatusNullValueDoesNotMatchEmpty verifies that a YAML null `status: ~`
// value does not produce a spurious match for non-empty queries. The
// stringifyTag(nil) fallback returns "", so a programmer-supplied empty query
// would match — but the public flag path filters empties at FromFlags, so
// this pins down the user-facing contract: null status matches no real query.

// TestTagPredicateListContainingOnlyNull pins down the behaviour of a tags list
// whose only entry is YAML null (`tags: [~]`). stringifyTag(nil) returns the
// empty string, so the loop compares "" to the (non-empty) query and returns
// false. A `Tag("")` call (which FromFlags filters out before it reaches the
// predicate) would technically match, but the FromFlags guard makes that
// unreachable through normal use — this test pins the predicate-level
// behaviour to guard against regressions in stringifyTag's nil handling.
func TestTagPredicateListContainingOnlyNull(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags:\n  - ~\n---\nbody\n",
	})
	if Tag("foo")(notes["a.md"]) {
		t.Errorf("Tag(foo) should not match a tags list containing only YAML null")
	}
	if Tag("null")(notes["a.md"]) {
		t.Errorf("Tag(null) should not match a YAML null tag (stringifyTag emits empty string, not \"null\")")
	}
}

// TestTagPredicateListMixedWithNull verifies that a string entry sitting next
// to a null entry in the same list still matches. Without correct handling of
// the nil branch in stringifyTag, the loop could panic or miss the string item.

// TestTagPredicateListMixedWithNull verifies that a string entry sitting next
// to a null entry in the same list still matches. Without correct handling of
// the nil branch in stringifyTag, the loop could panic or miss the string item.
func TestTagPredicateListMixedWithNull(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags:\n  - ~\n  - foo\n---\nbody\n",
	})
	if !Tag("foo")(notes["a.md"]) {
		t.Errorf("Tag(foo) should match a tags list containing [null, foo]")
	}
}

// TestCategoryPredicateNonStringNonListScalar verifies that a `categories`
// frontmatter value that is neither a string nor a list (e.g. an int from a
// typo or mis-coercion) is treated as absent rather than producing a match or
// a panic. GetWikilinkList returns (nil, false) for non-string non-list types;
// the predicate must propagate that as a non-match.

// TestCategoryPredicateNonStringNonListScalar verifies that a `categories`
// frontmatter value that is neither a string nor a list (e.g. an int from a
// typo or mis-coercion) is treated as absent rather than producing a match or
// a panic. GetWikilinkList returns (nil, false) for non-string non-list types;
// the predicate must propagate that as a non-match.
func TestCategoryPredicateNonStringNonListScalar(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"int.md":  "---\ncategories: 5\n---\nbody\n",
		"bool.md": "---\ncategories: true\n---\nbody\n",
	})
	if Category("5")(notes["int.md"]) {
		t.Errorf("Category(5) should not match a numeric scalar categories value")
	}
	if Category("true")(notes["bool.md"]) {
		t.Errorf("Category(true) should not match a boolean scalar categories value")
	}
}

// TestFolderPredicateRootNoteWithFolderFilter verifies that a note sitting at
// the vault root (Folder() returns "") is correctly rejected when the folder
// filter is non-empty. Before the fix to this audit's predecessors, an empty
// `nf` could match a non-empty `want` because of a bad HasPrefix call.

// TestStringifyTagRFC3339Timestamp pins down the RFC3339 path of stringifyTag
// (non-bare YAML timestamps with a time-of-day component). A note whose tags
// list includes a fully-qualified timestamp should be queryable via the same
// RFC3339 string the user would see if they dumped the frontmatter.
func TestStringifyTagRFC3339Timestamp(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags:\n  - 2026-05-19T10:00:00Z\n---\nbody\n",
	})
	if !Tag("2026-05-19T10:00:00Z")(notes["a.md"]) {
		t.Errorf("Tag should match a YAML timestamp tag via its RFC3339 form")
	}
	// The bare-date form should NOT match a timestamp tag because the
	// rendering paths diverge (RFC3339 vs date-only).
	if Tag("2026-05-19")(notes["a.md"]) {
		t.Errorf("Tag with bare-date form should not match a YAML timestamp tag")
	}
}

// TestParseAgeRejectsEmptyValue verifies that an age expression with no value
// (just an operator and a unit, e.g. ">d") is rejected. Without this guard,
// strconv.Atoi("") would surface a confusing error to the user.
