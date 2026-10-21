package queryexpr

import (
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/brainnotes/internal/query"
)

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
