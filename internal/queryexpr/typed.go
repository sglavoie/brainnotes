package queryexpr

import (
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/schema"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// Typed frontmatter comparison.
//
// Every decision here is driven by cfg.Frontmatter.Definition(name) — the
// declared PropertyDefinition — and never by the property's name. That is
// CLAUDE.md's config-driven model: a vault that declares `rating` as an int
// gets numeric ordering on it, and a vault that declares it as a string gets a
// compile error for `rating>3`, with nothing in this package knowing what
// "rating" means.
//
// Kind checking and coercion are internal/schema's job: CheckScalar classifies
// a stored value against a kind and TryCoerce salvages one written in another
// spelling (the same rules `bn set` applies). This file adds only the two
// things schema has no opinion about — parsing a *literal* out of query text,
// and applying a comparison *operator* to the pair.
//
// The operator legality table, keyed off config's kind constants so a new
// PropertyKind cannot silently fall through:
//
//	string     : = !=        fold-equal via query.FoldText
//	wikilink   : = !=        query.WikilinkTitleKey normalization
//	bool       : = !=        boolean equality
//	date       all           calendar-day compare; YYYY-MM-DD, today, "+30d", "-7d"
//	datetime   all           instant compare; now, RFC3339, relative
//	int float  all           numeric; a float literal on an int property is an error
//	list       :             contains; elements parsed as def.Items, any-element match
//
// `allowed` lists are deliberately NOT enforced on a query. You have to be able
// to find the notes that violate your own schema, so a query for a value
// outside the allow-list compiles and runs — it is `bn doctor`, not `bn find`,
// that reports the violation.

// comparer answers one comparison against a single stored frontmatter value.
type comparer func(stored any) bool

// comparerFor is a comparer that is not yet pinned to a clock. Splitting it
// this way is what lets a relative literal such as "-7d" be *syntax*-checked at
// compile time and *resolved* once per predicate build, rather than once per
// note scanned.
type comparerFor func(now time.Time) comparer

// constant lifts a clock-independent comparer into a comparerFor.
func constant(c comparer) comparerFor {
	return func(time.Time) comparer { return c }
}

// nowFrom pins the clock for relative literals. This is Env.Now's first
// consumer: query.Age still reads time.Now itself, so only the literals in this
// file honour a caller-supplied clock today.
func nowFrom(env Env) time.Time {
	if env.Now != nil {
		return env.Now()
	}
	return time.Now()
}

// propertyTerm lowers a term naming a frontmatter property onto a comparison
// against that property's stored value.
//
// declared says whether def came from [frontmatter.properties]. A declared
// property gets the typed path — its operator set and its literal grammar both
// follow from def. An undeclared property stays legal (CLAUDE.md requires
// undeclared properties to remain usable) but falls back to the untyped path,
// which compares text the way query.Tag and query.Status already do.
func propertyTerm(t *Term, src, name string, def config.PropertyDefinition, declared bool) (predicateBuilder, error) {
	if t.HasArg {
		return nil, NewParseError(src, t.Arg.Span, "property "+quote(name)+" takes no argument", nil)
	}
	if t.Op == OpNone || t.Value.Kind == ValueNone || len(t.Value.Atoms) == 0 {
		return nil, NewParseError(src, t.Span, "property "+quote(name)+" needs a value, e.g. "+name+":something", nil)
	}

	// `!=` is the negation of `=`, applied to the whole predicate rather than
	// inside the comparer. That keeps it reading the same way tag!=alpha
	// already does: a note that has no value at all for the property matches.
	op := t.Op
	negated := op == OpNe
	if negated {
		op = OpEq
	}

	var factories []comparerFor
	var err error
	if declared {
		factories, err = typedComparers(t, src, name, def, op)
	} else {
		factories, err = untypedComparers(t, src, name, op)
	}
	if err != nil {
		return nil, err
	}

	return func(env Env) (query.Predicate, error) {
		now := nowFrom(env)
		cmps := make([]comparer, 0, len(factories))
		for _, factory := range factories {
			cmps = append(cmps, factory(now))
		}
		pred := propertyPredicate(name, cmps)
		if negated {
			pred = negate(pred)
		}
		return pred, nil
	}, nil
}

// propertyPredicate matches a note whose value at name satisfies any comparer.
// A list value in the query is any-of, and a list value in the note is
// any-element, so `topics:[go, rust]` against `topics: [rust, web]` matches.
//
// A note carrying no value at name never matches, which is what makes `!=`
// match it once the negation wraps this predicate.
func propertyPredicate(name string, cmps []comparer) query.Predicate {
	return func(n *vault.Note) bool {
		if n == nil {
			return false
		}
		fm, err := n.Front()
		if err != nil {
			return false
		}
		raw, ok := fm.Get(name)
		if !ok {
			return false
		}
		for _, item := range listElements(raw) {
			for _, cmp := range cmps {
				if cmp(item) {
					return true
				}
			}
		}
		return false
	}
}

// listElements flattens a stored value into the elements a comparison runs
// against: a YAML list yields its items and any other value yields itself.
//
// Flattening unconditionally rather than only for a declared list is
// deliberate. A scalar stored where a list was declared — or a list stored
// where a scalar was — is a schema violation `bn doctor` reports, and a query
// that silently matched nothing would hide exactly the note the user is looking
// for.
func listElements(v any) []any {
	switch list := v.(type) {
	case []any:
		return list
	case []string:
		out := make([]any, len(list))
		for i, s := range list {
			out[i] = s
		}
		return out
	}
	return []any{v}
}

// typedComparers builds the per-atom comparers for a declared property, after
// checking that the operator is defined for its declared type.
func typedComparers(t *Term, src, name string, def config.PropertyDefinition, op Op) ([]comparerFor, error) {
	kind := def.ScalarKind()
	if !config.ValidScalarKind(kind) {
		return nil, NewParseError(src, fieldSpan(t),
			"property "+quote(name)+" has an unsupported declared type "+quote(schema.KindLabel(def))+" in brainnotes.toml", nil)
	}
	if !opAllowed(def, t.Op) {
		return nil, typedOpError(t, src, name, def)
	}
	if isOrderingOp(op) && (t.Value.Kind != ValueAtom || len(t.Value.Atoms) != 1) {
		return nil, NewParseError(src, t.Value.Span,
			"property "+quote(name)+" takes a single value with "+quote(string(t.Op))+", not a list", nil)
	}

	out := make([]comparerFor, 0, len(t.Value.Atoms))
	for _, atom := range t.Value.Atoms {
		factory, err := typedComparer(kind, op, atom, src, name)
		if err != nil {
			return nil, err
		}
		out = append(out, factory)
	}
	return out, nil
}

// opAllowed is the operator legality table. It switches on config's kind
// constants rather than string literals, so adding a PropertyKind without
// deciding its operator set fails closed instead of inheriting one.
func opAllowed(def config.PropertyDefinition, op Op) bool {
	switch op {
	case OpMatch:
		// `:` is defined for every kind: contains for a list, equality
		// otherwise.
		return true
	case OpEq, OpNe:
		// A list is contains-only; `tags=go` would read as "the list is
		// exactly [go]", which is not what it would do.
		return !def.IsList()
	case OpLt, OpLe, OpGt, OpGe:
		return !def.IsList() && kindIsOrdered(def.ScalarKind())
	}
	return false
}

// kindIsOrdered reports whether a scalar kind has a total order to compare on.
// string, wikilink, and bool deliberately do not: lexicographic string ordering
// is almost never what someone means by `status>wip`.
func kindIsOrdered(kind config.PropertyKind) bool {
	switch kind {
	case config.KindDate, config.KindDatetime, config.KindInt, config.KindFloat:
		return true
	}
	return false
}

// typedOpError reports an operator that the property's declared type does not
// define, naming the operator, the property, and the declaration to change:
//
//	operator ">" is not defined for string property "rating" (declared type = "string" in brainnotes.toml)
func typedOpError(t *Term, src, name string, def config.PropertyDefinition) *ParseError {
	return NewParseError(src, t.Span,
		"operator "+quote(string(t.Op))+" is not defined for "+string(def.Type)+" property "+quote(name)+
			" (declared type = "+quote(schema.KindLabel(def))+" in brainnotes.toml)", nil)
}

// typedComparer parses one literal against the element kind the property stores
// and returns the comparison it induces under op.
func typedComparer(kind config.PropertyKind, op Op, atom Atom, src, name string) (comparerFor, error) {
	switch kind {
	case config.KindString:
		want := query.FoldText(atom.Text)
		return constant(func(stored any) bool {
			s, ok := storedString(stored)
			return ok && query.FoldText(s) == want
		}), nil

	case config.KindWikilink:
		want := wikilinkKey(atom.Text)
		return constant(func(stored any) bool {
			s, ok := storedString(stored)
			return ok && wikilinkKey(s) == want
		}), nil

	case config.KindBool:
		want, ok := parseBoolLiteral(atom.Text)
		if !ok {
			return nil, NewParseError(src, atom.Span,
				"property "+quote(name)+" is declared bool, so it expects true or false, got "+quote(atom.Text), nil)
		}
		return constant(func(stored any) bool {
			b, ok := storedBool(stored)
			return ok && b == want
		}), nil

	case config.KindInt:
		n, err := parseIntLiteral(atom, src, name)
		if err != nil {
			return nil, err
		}
		want := float64(n)
		return constant(func(stored any) bool {
			got, ok := storedNumber(stored)
			return ok && compareOrder(op, signFloat(got, want))
		}), nil

	case config.KindFloat:
		want, err := parseFloatLiteral(atom, src, name)
		if err != nil {
			return nil, err
		}
		return constant(func(stored any) bool {
			got, ok := storedNumber(stored)
			return ok && compareOrder(op, signFloat(got, want))
		}), nil

	case config.KindDate:
		resolve, err := parseDateLiteral(atom, src, name)
		if err != nil {
			return nil, err
		}
		return func(now time.Time) comparer {
			want := dayOrdinal(resolve(now))
			loc := now.Location()
			return func(stored any) bool {
				ts, ok := storedTime(stored)
				if !ok {
					return false
				}
				return compareOrder(op, signInt(dayOrdinal(query.LocalDate(ts, loc)), want))
			}
		}, nil

	case config.KindDatetime:
		resolve, err := parseDatetimeLiteral(atom, src, name)
		if err != nil {
			return nil, err
		}
		return func(now time.Time) comparer {
			want := resolve(now)
			return func(stored any) bool {
				ts, ok := storedTime(stored)
				return ok && compareOrder(op, signTime(ts, want))
			}
		}, nil
	}
	return nil, NewParseError(src, atom.Span,
		"property "+quote(name)+" has an unsupported declared type in brainnotes.toml", nil)
}

// untypedComparers is the fallback for a property with no [frontmatter.properties]
// entry. CLAUDE.md keeps undeclared properties legal, so they stay queryable —
// just without a declared type to derive semantics from.
//
// `:` and `=` take the same path query.Tag and query.Status already use:
// stringify the stored value through query.StringifyValue, then fold-compare.
// Ordering operators have no declared type to interpret the literal against, so
// they require the stored value to actually be an int, a float64, or a
// time.Time and skip every note where it is not. Declare a type for predictable
// ordering.
func untypedComparers(t *Term, src, name string, op Op) ([]comparerFor, error) {
	if !isOrderingOp(op) {
		out := make([]comparerFor, 0, len(t.Value.Atoms))
		for _, atom := range t.Value.Atoms {
			want := query.FoldText(atom.Text)
			wantKey := wikilinkKey(atom.Text)
			out = append(out, constant(func(stored any) bool {
				// A stored value that is exactly one wikilink compares by its
				// resolved title, so `parent:[[Areas/Deep Work.md|Focus]]` is
				// found by parent:"deep work" — the same leniency Tag and
				// Category already give.
				if s, isString := stored.(string); isString {
					if key, isLink := query.WikilinkTitleKey(s); isLink {
						return key == wantKey
					}
				}
				return query.FoldText(query.StringifyValue(stored)) == want
			}))
		}
		return out, nil
	}

	if t.Value.Kind != ValueAtom || len(t.Value.Atoms) != 1 {
		return nil, NewParseError(src, t.Value.Span,
			"property "+quote(name)+" takes a single value with "+quote(string(t.Op))+", not a list", nil)
	}
	atom := t.Value.Atoms[0]
	bound, err := parseUntypedBound(atom, src, name)
	if err != nil {
		return nil, err
	}
	return []comparerFor{func(now time.Time) comparer {
		loc := now.Location()
		var wantTime time.Time
		var wantDay int
		if bound.resolve != nil {
			wantTime = bound.resolve(now)
			wantDay = dayOrdinal(wantTime)
		}
		return func(stored any) bool {
			switch v := stored.(type) {
			case int:
				return bound.hasNum && compareOrder(op, signFloat(float64(v), bound.num))
			case float64:
				return bound.hasNum && compareOrder(op, signFloat(v, bound.num))
			case time.Time:
				if bound.resolve == nil {
					return false
				}
				if bound.dateOnly {
					return compareOrder(op, signInt(dayOrdinal(query.LocalDate(v, loc)), wantDay))
				}
				return compareOrder(op, signTime(v, wantTime))
			}
			return false
		}
	}}, nil
}

// untypedBound is an ordering literal with no declared type behind it: either a
// number or a moment in time, decided by how the literal is spelled rather than
// by what the notes hold.
type untypedBound struct {
	num      float64
	hasNum   bool
	resolve  func(time.Time) time.Time
	dateOnly bool
}

// parseUntypedBound classifies an ordering literal for an undeclared property.
// Numbers win over dates, so a bare `2026` is a number rather than a year.
func parseUntypedBound(atom Atom, src, name string) (untypedBound, error) {
	if f, err := strconv.ParseFloat(strings.TrimSpace(atom.Text), 64); err == nil {
		return untypedBound{num: f, hasNum: true}, nil
	}
	if resolve, err := parseDateLiteral(atom, src, name); err == nil {
		return untypedBound{resolve: resolve, dateOnly: true}, nil
	}
	if resolve, err := parseDatetimeLiteral(atom, src, name); err == nil {
		return untypedBound{resolve: resolve}, nil
	}
	return untypedBound{}, NewParseError(src, atom.Span,
		"property "+quote(name)+" is not declared in brainnotes.toml, so ordering it needs a number, a date, or a timestamp, got "+
			quote(atom.Text)+"; declare a type for predictable ordering", nil)
}

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

// storedString reads a stored value as a string: schema.CheckScalar classifies
// it, and schema.TryCoerce salvages one stored in another spelling, so a
// `status: 3` written before the property was declared a string still compares.
func storedString(v any) (string, bool) {
	if schema.CheckScalar(v, config.KindString, false) {
		s, ok := v.(string)
		return s, ok
	}
	if coerced, ok := schema.TryCoerce(v, config.PropertyDefinition{Type: config.KindString}); ok {
		s, isString := coerced.(string)
		return s, isString
	}
	return "", false
}

func storedBool(v any) (bool, bool) {
	if schema.CheckScalar(v, config.KindBool, false) {
		b, ok := v.(bool)
		return b, ok
	}
	if coerced, ok := schema.TryCoerce(v, config.PropertyDefinition{Type: config.KindBool}); ok {
		b, isBool := coerced.(bool)
		return b, isBool
	}
	return false, false
}

// storedNumber reads a stored value as a float64. int and float64 are compared
// on the same scale, which is exact for every magnitude below 2^53 — far past
// anything a hand-written frontmatter number reaches.
func storedNumber(v any) (float64, bool) {
	if schema.CheckScalar(v, config.KindInt, false) {
		n, ok := v.(int)
		return float64(n), ok
	}
	if schema.CheckScalar(v, config.KindFloat, false) {
		f, ok := v.(float64)
		return f, ok
	}
	if coerced, ok := schema.TryCoerce(v, config.PropertyDefinition{Type: config.KindFloat}); ok {
		f, isFloat := coerced.(float64)
		return f, isFloat
	}
	return 0, false
}

// storedTime reads a stored value as an instant.
//
// The kind check is deliberately KindDatetime even when the property is
// declared date: CheckScalar's date branch additionally demands that the YAML
// scalar was written date-only, and a datetime sitting in a date property is a
// schema violation the user needs to be able to *find*, not one a query should
// silently skip. The calendar-day reduction happens in the comparison instead.
func storedTime(v any) (time.Time, bool) {
	if schema.CheckScalar(v, config.KindDatetime, false) {
		t, ok := v.(time.Time)
		return t, ok
	}
	for _, kind := range []config.PropertyKind{config.KindDate, config.KindDatetime} {
		if coerced, ok := schema.TryCoerce(v, config.PropertyDefinition{Type: kind}); ok {
			if t, isTime := coerced.(time.Time); isTime {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

// wikilinkKey normalizes either side of a wikilink comparison to the key
// BuildGraph resolves links with: folder prefix, `.md` suffix, heading, and
// alias stripped, then NFC-normalized and lowercased.
//
// It accepts both spellings because both occur: a stored value is usually
// written `[[Deep Work]]`, while a query atom has already had its brackets
// removed by the lexer (or was never bracketed at all, as in parent:"Deep
// Work"). Text that is not a wikilink either way folds as plain text, which is
// session 3's prescribed reading of WikilinkTitleKey returning false.
func wikilinkKey(s string) string {
	if key, ok := query.WikilinkTitleKey(s); ok {
		return key
	}
	if key, ok := query.WikilinkTitleKey("[[" + s + "]]"); ok {
		return key
	}
	return query.FoldText(s)
}

// isOrderingOp reports whether op asks for a total order rather than equality.
func isOrderingOp(op Op) bool {
	switch op {
	case OpLt, OpLe, OpGt, OpGe:
		return true
	}
	return false
}

// compareOrder answers op given the sign of (stored - literal).
//
// OpNe is absent on purpose: propertyTerm rewrites `!=` to `=` and negates the
// whole predicate, so a note that lacks the property matches `x!=v` the same
// way it matches `tag!=alpha`.
func compareOrder(op Op, sign int) bool {
	switch op {
	case OpMatch, OpEq:
		return sign == 0
	case OpLt:
		return sign < 0
	case OpLe:
		return sign <= 0
	case OpGt:
		return sign > 0
	case OpGe:
		return sign >= 0
	}
	return false
}

func signFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func signInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func signTime(a, b time.Time) int {
	switch {
	case a.Before(b):
		return -1
	case a.After(b):
		return 1
	}
	return 0
}
