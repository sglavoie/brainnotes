package schema

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
)

type Violation struct {
	Property string
	Kind     string // "missing", "type", "value"
	Detail   string
	NotePath string
}

func ValidType(schemaType string) bool {
	switch schemaType {
	case "string", "list", "date", "int", "bool", "float":
		return true
	}
	return false
}

func CheckType(value any, schemaType string) bool {
	switch schemaType {
	case "string":
		_, ok := value.(string)
		return ok
	case "list":
		_, ok := value.([]any)
		return ok
	case "date":
		t, ok := value.(time.Time)
		return ok && isDateOnlyTime(t)
	case "int":
		_, ok := value.(int)
		return ok
	case "bool":
		_, ok := value.(bool)
		return ok
	case "float":
		_, ok := value.(float64)
		return ok
	}
	return false
}

func CheckAllowed(value any, allowed []string, isWikilink bool) (ok bool, offending string) {
	set := make(map[string]struct{}, len(allowed))
	for _, a := range allowed {
		set[a] = struct{}{}
	}

	check := func(s string) bool {
		if isWikilink {
			s = stripWikilink(s)
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
				return false, fmt.Sprintf("%v", item)
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
			// Unknown type (nil, map, etc.) — cannot match any allowed
			// value, so reject.
			return false, fmt.Sprintf("%v", v)
		}
		if !check(s) {
			return false, s
		}
		return true, ""
	}
}

func stripWikilink(s string) string {
	if strings.HasPrefix(s, "[[") && strings.HasSuffix(s, "]]") && len(s) >= 4 {
		return s[2 : len(s)-2]
	}
	return s
}

func RuleSummary(rule config.PropertySchema) string {
	s := "type: " + rule.Type
	if len(rule.Allowed) > 0 {
		s += ", allowed: " + strings.Join(rule.Allowed, ", ")
	}
	return s
}

func ValidateNote(front *frontmatter.Map, notePath string, schema map[string]config.PropertySchema, wikilinkProps []string) []Violation {
	if front == nil {
		return nil
	}
	wlSet := make(map[string]struct{}, len(wikilinkProps))
	for _, p := range wikilinkProps {
		wlSet[p] = struct{}{}
	}

	keys := make([]string, 0, len(schema))
	for k := range schema {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var violations []Violation
	for _, key := range keys {
		rule := schema[key]
		if !front.Has(key) {
			if rule.Required {
				violations = append(violations, Violation{
					Property: key,
					Kind:     "missing",
					Detail:   RuleSummary(rule),
					NotePath: notePath,
				})
			}
			continue
		}

		val, _ := front.Get(key)

		if !CheckType(val, rule.Type) {
			violations = append(violations, Violation{
				Property: key,
				Kind:     "type",
				Detail:   fmt.Sprintf("expected %s, got %s", rule.Type, goTypeName(val)),
				NotePath: notePath,
			})
			continue
		}

		if len(rule.Allowed) > 0 {
			_, isWL := wlSet[key]
			if ok, bad := CheckAllowed(val, rule.Allowed, isWL); !ok {
				violations = append(violations, Violation{
					Property: key,
					Kind:     "value",
					Detail:   fmt.Sprintf("invalid value %q (allowed: %s)", bad, strings.Join(rule.Allowed, ", ")),
					NotePath: notePath,
				})
			}
		}
	}
	return violations
}

func scalarToString(v any) string {
	switch val := v.(type) {
	case int:
		return strconv.Itoa(val)
	case bool:
		return strconv.FormatBool(val)
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case time.Time:
		if !isDateOnlyTime(val) {
			return ""
		}
		return val.Format("2006-01-02")
	}
	return ""
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
	case []any:
		return "list"
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
