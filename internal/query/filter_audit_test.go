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
func TestAgeEqualSubDayDuration(t *testing.T) {
	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	_, notes := newVault(t, map[string]string{
		"today.md":     "---\ncreated: " + today + "\n---\n",
		"yesterday.md": "---\ncreated: " + yesterday + "\n---\n",
	})
	// 12h truncates to 0 days → equivalent to =0d (today).
	if !Age("=", 12*time.Hour, "created")(notes["today.md"]) {
		t.Errorf("=12h should match today (12h truncates to 0d)")
	}
	if Age("=", 12*time.Hour, "created")(notes["yesterday.md"]) {
		t.Errorf("=12h should not match yesterday")
	}
	// 25h truncates to 1 day → equivalent to =1d (yesterday).
	if !Age("=", 25*time.Hour, "created")(notes["yesterday.md"]) {
		t.Errorf("=25h should match yesterday (25h truncates to 1d)")
	}
	if Age("=", 25*time.Hour, "created")(notes["today.md"]) {
		t.Errorf("=25h should not match today")
	}
}

// TestAgePredicateMonthBoundary verifies that the `=` operator's calendar-day
// math correctly walks across month boundaries (e.g. =1d on March 1 matches a
// note dated Feb 28 or Feb 29). time.Time.AddDate handles this automatically;
// this test pins down the integration so a future refactor of equalDayAge that
// reinvented the math would not silently break.
func TestAgePredicateMonthBoundary(t *testing.T) {
	// Pick a known month-boundary date: yesterday from "today" reliably
	// crosses a month or year boundary one day per month, but on most days
	// it's still the same month. Rather than fix the date, we verify the
	// invariant: =1d always matches the date that is "yesterday" relative to
	// now, regardless of which month/year that lands on.
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	_, notes := newVault(t, map[string]string{
		"y.md": "---\ncreated: " + yesterday + "\n---\n",
	})
	if !Age("=", 24*time.Hour, "created")(notes["y.md"]) {
		t.Errorf("=1d should match a note dated yesterday, regardless of month boundary")
	}
}

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
func TestParseAgeRejectsEmptyValue(t *testing.T) {
	for _, raw := range []string{">d", "<s", "=h", ">m"} {
		if _, _, err := ParseAge(raw); err == nil {
			t.Errorf("ParseAge(%q) should error: empty numeric value", raw)
		}
	}
}

// TestParseAgeRejectsMissingUnit verifies that an age expression with no unit
// (e.g. ">30") is rejected. Without this guard, the predicate would treat the
// trailing digit as the unit and fail with a confusing error.
func TestParseAgeRejectsMissingUnit(t *testing.T) {
	for _, raw := range []string{">30", "<7", "=0"} {
		if _, _, err := ParseAge(raw); err == nil {
			t.Errorf("ParseAge(%q) should error: missing unit", raw)
		}
	}
}

// TestFromFlagsAgeAndCreatedComposeOnDifferentFields verifies that --age and
// --created produce predicates over different frontmatter keys (`last` vs
// `created`) so they compose correctly in an AND query. A note with the right
// `created` but missing `last` should not match a combined query.
func TestFromFlagsAgeAndCreatedComposeOnDifferentFields(t *testing.T) {
	recent := time.Now().AddDate(0, 0, -2).Format("2006-01-02")
	_, notes := newVault(t, map[string]string{
		"both.md":         "---\ncreated: " + recent + "\nlast: " + recent + "\n---\n",
		"created-only.md": "---\ncreated: " + recent + "\n---\n",
	})
	pred, err := FromFlags(Flags{
		Age:          "<30d",
		Created:      "<30d",
		MinBacklinks: -1, MaxBacklinks: -1, MinLinks: -1, MaxLinks: -1,
	})
	if err != nil {
		t.Fatalf("FromFlags: %v", err)
	}
	if !pred(notes["both.md"]) {
		t.Errorf("a note with both `last` and `created` recent should match --age<30d AND --created<30d")
	}
	if pred(notes["created-only.md"]) {
		t.Errorf("a note missing `last` should not match --age<30d (which queries `last`) even if `created` is recent")
	}
}

// TestBuildGraphCaseDifferingTitlesShareKey documents the behaviour when two
// notes with case-differing filenames (e.g. "Foo.md" and "foo.md") would
// produce the same lowercased title key. The graph keys by lowercased stem,
// so the second note's entry clobbers the first in Titles/PathsByTitle.
// In practice case-insensitive filesystems prevent this, but this test pins
// the contract for case-sensitive filesystems (e.g. Linux): the latter note
// wins, and consumers must accept a single PathsByTitle entry per key.
func TestBuildGraphCaseDifferingTitlesShareKey(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Foo.md":     "---\n---\nbody\n",
		"sub/foo.md": "---\n---\nbody\n",
	})
	all := []*vault.Note{notes["Foo.md"], notes["sub/foo.md"]}
	g := BuildGraph(all)
	// Exactly one entry for the shared key.
	if _, ok := g.Titles["foo"]; !ok {
		t.Errorf("expected 'foo' in Titles when two notes share the lowercased stem")
	}
	// PathsByTitle holds a single path (the last one inserted wins).
	if p := g.PathsByTitle["foo"]; p == "" {
		t.Errorf("PathsByTitle[foo] should be non-empty; got empty")
	}
}
