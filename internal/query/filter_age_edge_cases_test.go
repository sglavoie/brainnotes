package query

import (
	"testing"
	"time"
)

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
