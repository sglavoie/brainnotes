package query

import (
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// TestAgeEqualSubDayDurationTruncatesToDays pins down the documented behaviour
// that the `=` operator works on calendar-day granularity. Sub-day durations
// (e.g. `=23h`, `=45m`, `=10s`) are truncated via integer division, so a query
// like `=23h` is equivalent to `=0d` (matches today). This protects against a
// regression where a future change might switch `=` to a duration-based
// comparison and break the documented day semantics.
func TestAgeEqualSubDayDurationTruncatesToDays(t *testing.T) {
	now := time.Now()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	_, notes := newVault(t, map[string]string{
		"today.md":     "---\ncreated: " + today + "\n---\n",
		"yesterday.md": "---\ncreated: " + yesterday + "\n---\n",
	})

	cases := []struct {
		dur  time.Duration
		want bool // expected for today.md
	}{
		{0, true},
		{23 * time.Hour, true},   // truncates to 0d
		{1 * time.Second, true},  // truncates to 0d
		{59 * time.Minute, true}, // truncates to 0d
		{24 * time.Hour, false},  // exactly 1d -> not today
		{36 * time.Hour, false},  // truncates to 1d -> not today
		{47 * time.Hour, false},  // truncates to 1d -> not today
	}
	for _, c := range cases {
		got := Age("=", c.dur, "created")(notes["today.md"])
		if got != c.want {
			t.Errorf("Age(=, %v) on today.md = %v, want %v", c.dur, got, c.want)
		}
	}

	if !Age("=", 30*time.Hour, "created")(notes["yesterday.md"]) {
		t.Errorf("Age(=, 30h) should match yesterday's note (truncates to 1d)")
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
func TestAndOrDeeplyNested(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags: [foo]\nstatus: Reading\ntype: Book\n---\n",
		"b.md": "---\ntags: [bar]\nstatus: Reading\ntype: Article\n---\n",
		"c.md": "---\ntags: [foo]\nstatus: Done\ntype: Book\n---\n",
	})
	// ((tag=foo AND status=Reading) OR (tag=bar AND type=Article)) AND has=status
	p := And(
		Or(
			And(Tag("foo"), Status("Reading")),
			And(Tag("bar"), Type("Article")),
		),
		Has("status"),
	)
	cases := map[string]bool{
		"a.md": true,  // foo+Reading
		"b.md": true,  // bar+Article
		"c.md": false, // foo+Done, type=Book — neither inner AND matches
	}
	for path, want := range cases {
		if got := p(notes[path]); got != want {
			t.Errorf("deeply nested predicate on %s = %v, want %v", path, got, want)
		}
	}
}

// TestParseAgeNumericOverflowSeconds locks down the overflow guard for
// large second-unit values. Hours and days overflow at much smaller integers
// than seconds, but the guard must apply uniformly.
func TestParseAgeNumericOverflowSeconds(t *testing.T) {
	// MaxInt64 nanoseconds is the cap. time.Second is 1e9 ns.
	// Therefore values above MaxInt64/1e9 ≈ 9.22e9 should overflow.
	// strconv.Atoi on a 13-digit number is fine on 64-bit; the predicate
	// guard catches the duration overflow.
	cases := []struct {
		in      string
		wantErr bool
	}{
		{">9223372036s", false},   // ~MaxInt64/1e9, well within bounds
		{">9223372037000s", true}, // overflows duration
	}
	for _, c := range cases {
		_, _, err := ParseAge(c.in)
		if c.wantErr && err == nil {
			t.Errorf("ParseAge(%q) expected overflow error, got none", c.in)
		}
		if !c.wantErr && err != nil {
			t.Errorf("ParseAge(%q) unexpected err: %v", c.in, err)
		}
	}
}

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
func TestMissingTrueForUnreadableNote(t *testing.T) {
	// A note whose path points to a nonexistent file: Front() returns a
	// read error, Missing should still report the property as absent
	// (consistent with the "every loadable property is reported correctly,
	// unloadable notes report all properties absent" contract).
	n := &vault.Note{Path: "/nonexistent/path/to/note.md"}
	if Has("anything")(n) {
		t.Errorf("Has(anything) should be false for an unreadable note")
	}
	if !Missing("anything")(n) {
		t.Errorf("Missing(anything) should be true for an unreadable note")
	}
}
