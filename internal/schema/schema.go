package schema

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/frontmatter"
)

// KindParse is the Violation kind for a note whose frontmatter could not be
// parsed at all. ValidateNote never produces it; `bn lint` reports it for the
// notes vault.NotesLenient skipped, with Property "frontmatter" and the parse
// error as Detail.
const KindParse = "parse"

type Violation struct {
	Property string
	Kind     string // "missing", "type", "value", or KindParse
	Detail   string
	NotePath string
}

// CheckScalar reports whether value matches kind. dateOnly says whether a
// time.Time value was written as a date-only YAML scalar; it is only consulted
// for the date kind.
func CheckScalar(value any, kind config.PropertyKind, dateOnly bool) bool {
	switch kind {
	case config.KindString, config.KindWikilink:
		_, ok := value.(string)
		return ok
	case config.KindDate:
		_, ok := value.(time.Time)
		return ok && dateOnly
	case config.KindDatetime:
		_, ok := value.(time.Time)
		return ok
	case config.KindInt:
		_, ok := value.(int)
		return ok
	case config.KindFloat:
		_, ok := value.(float64)
		return ok
	case config.KindBool:
		_, ok := value.(bool)
		return ok
	}
	return false
}

// CheckDefinition reports whether the value stored at key satisfies def. When
// it does not, detail describes the mismatch.
func CheckDefinition(front *frontmatter.Map, key string, value any, def config.PropertyDefinition) (ok bool, detail string) {
	if def.IsList() {
		list, isList := value.([]any)
		if !isList {
			return false, fmt.Sprintf("expected %s, got %s", KindLabel(def), propertyTypeName(front, key, value))
		}
		for i, item := range list {
			if CheckScalar(item, def.Items, itemIsDateOnly(item)) {
				continue
			}
			return false, fmt.Sprintf("expected %s, got %s at index %d", KindLabel(def), goTypeName(item), i)
		}
		return true, ""
	}
	if CheckScalar(value, def.Type, isFrontmatterDateOnly(front, key)) {
		return true, ""
	}
	return false, fmt.Sprintf("expected %s, got %s", KindLabel(def), propertyTypeName(front, key, value))
}

// KindLabel renders a definition's type for diagnostics.
func KindLabel(def config.PropertyDefinition) string {
	if def.IsList() {
		return "list of " + string(def.Items)
	}
	return string(def.Type)
}

func CheckAllowed(value any, allowed []string, isWikilink bool) (ok bool, offending string) {
	set := make(map[string]struct{}, len(allowed))
	for _, a := range allowed {
		set[a] = struct{}{}
	}

	check := func(s string) bool {
		if isWikilink && isAllowedWikilinkValue(s, set) {
			return true
		}
		_, found := set[s]
		return found
	}

	switch v := value.(type) {
	case string:
		if !check(v) {
			if isWikilink {
				return false, stripWikilink(v)
			}
			return false, v
		}
		return true, ""
	case []any:
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				s = scalarToString(item)
				if s == "" {
					return false, unsupportedValueLabel(item)
				}
				if !check(s) {
					return false, s
				}
				continue
			}
			if !check(s) {
				if isWikilink {
					return false, stripWikilink(s)
				}
				return false, s
			}
		}
		return true, ""
	default:
		// For non-string scalar types (int, bool, float64, time.Time),
		// convert to the canonical string representation and check
		// against the allowed list.
		s := scalarToString(v)
		if s == "" {
			// Structured or unsupported YAML (null, map, nested list) has no
			// canonical scalar spelling, so it can never match an allowed
			// value and is never stringified into one.
			return false, unsupportedValueLabel(v)
		}
		if !check(s) {
			return false, s
		}
		return true, ""
	}
}

func isAllowedWikilinkValue(s string, set map[string]struct{}) bool {
	if _, found := set[stripWikilink(s)]; found {
		return true
	}
	parts, ok := parseWikilinkRepairParts(s)
	if !ok {
		return false
	}
	_, found := set[parts.target]
	return found
}

func stripWikilink(s string) string {
	if strings.HasPrefix(s, "[[") && strings.HasSuffix(s, "]]") && len(s) >= 4 {
		return s[2 : len(s)-2]
	}
	return s
}

func ValidateNote(front *frontmatter.Map, notePath string, props map[string]config.PropertyDefinition) []Violation {
	if front == nil {
		return nil
	}

	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var violations []Violation
	for _, key := range keys {
		def := props[key]
		if !front.Has(key) {
			if def.Required {
				violations = append(violations, Violation{
					Property: key,
					Kind:     "missing",
					Detail:   def.Summary(),
					NotePath: notePath,
				})
			}
			continue
		}

		val, _ := front.Get(key)

		if ok, detail := CheckDefinition(front, key, val, def); !ok {
			violations = append(violations, Violation{
				Property: key,
				Kind:     "type",
				Detail:   detail,
				NotePath: notePath,
			})
			continue
		}

		if len(def.Allowed) > 0 {
			if ok, bad := CheckAllowed(val, def.Allowed, def.IsWikilink()); !ok {
				violations = append(violations, Violation{
					Property: key,
					Kind:     "value",
					Detail:   fmt.Sprintf("invalid value %q (allowed: %s)", bad, strings.Join(def.Allowed, ", ")),
					NotePath: notePath,
				})
			}
		}
	}
	return violations
}

func propertyTypeName(front *frontmatter.Map, key string, value any) string {
	if _, ok := value.(time.Time); ok {
		if isFrontmatterDateOnly(front, key) {
			return "date"
		}
		return "datetime"
	}
	return goTypeName(value)
}

func isFrontmatterDateOnly(front *frontmatter.Map, key string) bool {
	if front != nil {
		return front.TimeIsDateOnly(key)
	}
	return false
}

// itemIsDateOnly applies the midnight-UTC heuristic to a list element. Per-index
// timestamp metadata is not exposed by frontmatter.Map, so a list of dates is
// judged by value rather than by original spelling.
func itemIsDateOnly(item any) bool {
	t, ok := item.(time.Time)
	return ok && isDateOnlyTime(t)
}

func scalarToString(v any) string {
	switch val := v.(type) {
	case int:
		return strconv.Itoa(val)
	case bool:
		return strconv.FormatBool(val)
	case float64:
		return formatScalarFloat(val)
	case time.Time:
		if !isDateOnlyTime(val) {
			return ""
		}
		return val.Format("2006-01-02")
	}
	return ""
}

// unsupportedValueLabel names a value that has no canonical scalar spelling, so
// diagnostics can point at it without inventing a string form for it.
func unsupportedValueLabel(v any) string {
	switch v.(type) {
	case nil:
		return "<null>"
	case map[string]any:
		return "<map>"
	case []any, []string:
		return "<list>"
	case time.Time:
		return "<datetime>"
	}
	return "<unsupported " + fmt.Sprintf("%T", v) + ">"
}

func formatScalarFloat(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return s
	}
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

func isDateOnlyTime(t time.Time) bool {
	return t.Location() == time.UTC &&
		t.Hour() == 0 &&
		t.Minute() == 0 &&
		t.Second() == 0 &&
		t.Nanosecond() == 0
}

func goTypeName(v any) string {
	switch val := v.(type) {
	case string:
		return "string"
	case []any, []string:
		return "list"
	case map[string]any:
		return "map"
	case time.Time:
		if !isDateOnlyTime(val) {
			return "datetime"
		}
		return "date"
	case int:
		return "int"
	case bool:
		return "bool"
	case float64:
		return "float"
	case nil:
		return "nil"
	default:
		return fmt.Sprintf("%T", v)
	}
}
