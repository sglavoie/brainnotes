package queryexpr

import (
	"strconv"
	"strings"
	"time"
)

// parseBoolLiteral reads true/false case-insensitively, matching how boolField
// spells the flag-shaped reserved fields.
func parseBoolLiteral(text string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

// parseIntLiteral reads a whole number. A float literal on an int property is a
// compile error rather than a silent truncation: `rating>3.5` on an int
// property can only ever have been a mistake about one of the two.
func parseIntLiteral(atom Atom, src, name string) (int, error) {
	text := strings.TrimSpace(atom.Text)
	if n, err := strconv.Atoi(text); err == nil {
		return n, nil
	}
	if _, err := strconv.ParseFloat(text, 64); err == nil {
		return 0, NewParseError(src, atom.Span,
			"property "+quote(name)+" is declared int, so "+quote(atom.Text)+" must be a whole number", nil)
	}
	return 0, NewParseError(src, atom.Span,
		"property "+quote(name)+" is declared int, so it expects a whole number, got "+quote(atom.Text), nil)
}

func parseFloatLiteral(atom Atom, src, name string) (float64, error) {
	f, err := strconv.ParseFloat(strings.TrimSpace(atom.Text), 64)
	if err != nil {
		return 0, NewParseError(src, atom.Span,
			"property "+quote(name)+" is declared float, so it expects a number, got "+quote(atom.Text), nil)
	}
	return f, nil
}

// parseDateLiteral accepts YYYY-MM-DD, `today`, and a signed day offset such as
// "+30d" or "-7d".
//
// A negative offset has to be quoted — due<"-7d" — because a leading `-` is
// negation to the lexer wherever it starts a token. `+30d` needs no quoting,
// since `+` is an ordinary word rune.
func parseDateLiteral(atom Atom, src, name string) (func(time.Time) time.Time, error) {
	text := strings.TrimSpace(atom.Text)
	if strings.EqualFold(text, "today") {
		return startOfDay, nil
	}
	if t, err := time.Parse("2006-01-02", text); err == nil {
		y, m, d := t.Date()
		return func(now time.Time) time.Time {
			return time.Date(y, m, d, 0, 0, 0, 0, now.Location())
		}, nil
	}
	if n, unit, ok := parseOffset(text); ok && unit == 'd' {
		return func(now time.Time) time.Time {
			return startOfDay(now).AddDate(0, 0, n)
		}, nil
	}
	return nil, NewParseError(src, atom.Span,
		"property "+quote(name)+" is declared date, so it expects YYYY-MM-DD, \"today\", or a day offset such as +30d or \"-7d\", got "+
			quote(atom.Text), nil)
}

// parseDatetimeLiteral accepts `now`, RFC3339, and a signed offset in s/m/h/d —
// the same unit vocabulary query.ParseAge uses for the age field. A date-only
// spelling is also accepted and means midnight local on that day, so
// `created>2026-01-01` works on a datetime property.
func parseDatetimeLiteral(atom Atom, src, name string) (func(time.Time) time.Time, error) {
	text := strings.TrimSpace(atom.Text)
	if strings.EqualFold(text, "now") {
		return func(now time.Time) time.Time { return now }, nil
	}
	if strings.EqualFold(text, "today") {
		return startOfDay, nil
	}
	if t, err := time.Parse(time.RFC3339, text); err == nil {
		return func(time.Time) time.Time { return t }, nil
	}
	if t, err := time.Parse("2006-01-02", text); err == nil {
		y, m, d := t.Date()
		return func(now time.Time) time.Time {
			return time.Date(y, m, d, 0, 0, 0, 0, now.Location())
		}, nil
	}
	if n, unit, ok := parseOffset(text); ok {
		if dur, ok := offsetDuration(n, unit); ok {
			return func(now time.Time) time.Time { return now.Add(dur) }, nil
		}
	}
	return nil, NewParseError(src, atom.Span,
		"property "+quote(name)+" is declared datetime, so it expects an RFC3339 timestamp, \"now\", or an offset such as +2h or \"-7d\", got "+
			quote(atom.Text), nil)
}

// parseOffset splits a signed relative literal like "+30d" into its count and
// unit. The sign is mandatory: a bare "7d" would be ambiguous with a note that
// really does store the string "7d".
func parseOffset(text string) (n int, unit byte, ok bool) {
	if len(text) < 3 {
		return 0, 0, false
	}
	sign := 1
	switch text[0] {
	case '+':
	case '-':
		sign = -1
	default:
		return 0, 0, false
	}
	unit = text[len(text)-1]
	v, err := strconv.Atoi(text[1 : len(text)-1])
	if err != nil || v < 0 {
		return 0, 0, false
	}
	return sign * v, unit, true
}

// offsetDuration converts a relative literal's count and unit into a duration,
// using query.ParseAge's units so `age>7d` and `modified>"-7d"` agree on what a
// day is.
func offsetDuration(n int, unit byte) (time.Duration, bool) {
	var mult time.Duration
	switch unit {
	case 's':
		mult = time.Second
	case 'm':
		mult = time.Minute
	case 'h':
		mult = time.Hour
	case 'd':
		mult = 24 * time.Hour
	default:
		return 0, false
	}
	return time.Duration(n) * mult, true
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// dayOrdinal collapses a calendar date to a comparable integer, so date
// comparison is a day-vs-day question and never a clock-time one.
func dayOrdinal(t time.Time) int {
	y, m, d := t.Date()
	return y*10000 + int(m)*100 + d
}
