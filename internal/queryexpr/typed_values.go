package queryexpr

import (
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/schema"
)

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
