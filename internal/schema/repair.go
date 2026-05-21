package schema

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
)

type RepairAction struct {
	Property string
	Kind     string // comma-joined: "type_coerce", "canonicalize", "wikilink_wrap", "list_dedup"
	OldValue any
	NewValue any
}

type RepairSkip struct {
	Property string
	Kind     string // "missing_required", "type_unconvertible", "value_not_allowed"
	Detail   string
}

type RepairResult struct {
	Actions []RepairAction
	Skips   []RepairSkip
}

// PlanRepairs inspects a note's frontmatter against the schema and config,
// returning safe mechanical fixes and reasons for skipped issues. Each
// property produces at most one action (combining all applicable fix
// categories) and zero or more skips.
//
// Fix categories applied in order per property:
//  1. type_coerce — convert to schema type when lossless
//  2. canonicalize — case-insensitive match to an allowed value
//  3. wikilink_wrap — wrap bare strings in [[ ]] for wikilink properties
//  4. list_dedup — remove duplicate list entries
func PlanRepairs(front *frontmatter.Map, sch map[string]config.PropertySchema, wikilinkProps []string) RepairResult {
	var result RepairResult
	if front == nil {
		return result
	}
	wlSet := make(map[string]bool, len(wikilinkProps))
	for _, p := range wikilinkProps {
		wlSet[p] = true
	}

	processed := make(map[string]bool)

	schemaKeys := make([]string, 0, len(sch))
	for k := range sch {
		schemaKeys = append(schemaKeys, k)
	}
	sort.Strings(schemaKeys)

	for _, key := range schemaKeys {
		rule := sch[key]
		processed[key] = true

		if !front.Has(key) {
			if rule.Required {
				result.Skips = append(result.Skips, RepairSkip{
					Property: key,
					Kind:     "missing_required",
					Detail:   "required property is missing; no default available (" + RuleSummary(rule) + ")",
				})
			}
			continue
		}

		planProperty(&result, front, key, &rule, wlSet[key])
	}

	for _, key := range front.Keys() {
		if processed[key] {
			continue
		}
		planProperty(&result, front, key, nil, wlSet[key])
	}

	return result
}

func planProperty(result *RepairResult, front *frontmatter.Map, key string, rule *config.PropertySchema, isWikilink bool) {
	val, _ := front.Get(key)
	working := val
	var kinds []string
	skipRest := false

	if rule != nil {
		if !CheckType(working, rule.Type) {
			coerced, ok := TryCoerce(working, rule.Type)
			if ok {
				working = coerced
				kinds = append(kinds, "type_coerce")
			} else {
				result.Skips = append(result.Skips, RepairSkip{
					Property: key,
					Kind:     "type_unconvertible",
					Detail:   fmt.Sprintf("cannot safely convert %s to %s", goTypeName(working), rule.Type),
				})
				skipRest = true
			}
		}

		if !skipRest && len(rule.Allowed) > 0 {
			canonized, unfixable := canonicalizeAll(working, rule.Allowed, isWikilink)
			if !reflect.DeepEqual(working, canonized) {
				working = canonized
				kinds = append(kinds, "canonicalize")
			}
			for _, bad := range unfixable {
				result.Skips = append(result.Skips, RepairSkip{
					Property: key,
					Kind:     "value_not_allowed",
					Detail:   fmt.Sprintf("no allowed value matches %q", bad),
				})
			}
		}
	}

	if isWikilink {
		wrapped := wrapAllWikilinks(working)
		if !reflect.DeepEqual(working, wrapped) {
			working = wrapped
			kinds = append(kinds, "wikilink_wrap")
		}
	}

	if list, ok := working.([]any); ok {
		deduped := dedupList(list)
		if len(deduped) != len(list) {
			working = deduped
			kinds = append(kinds, "list_dedup")
		}
	}

	if len(kinds) > 0 {
		result.Actions = append(result.Actions, RepairAction{
			Property: key,
			Kind:     strings.Join(kinds, ","),
			OldValue: val,
			NewValue: working,
		})
	}
}

// TryCoerce attempts a lossless conversion of val to targetType. Returns
// the converted value and true on success. Follows the same coercion
// rules as `bn set` (bool, int, float, date, string, list promotion).
func TryCoerce(val any, targetType string) (any, bool) {
	switch targetType {
	case "bool":
		if s, ok := val.(string); ok {
			switch strings.ToLower(s) {
			case "true":
				return true, true
			case "false":
				return false, true
			}
		}
	case "int":
		if s, ok := val.(string); ok {
			if n, err := strconv.Atoi(s); err == nil {
				return n, true
			}
		}
		if f, ok := val.(float64); ok {
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return nil, false
			}
			n := int(f)
			if float64(n) == f {
				return n, true
			}
		}
	case "float":
		if s, ok := val.(string); ok {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return f, true
			}
		}
		if n, ok := val.(int); ok {
			return float64(n), true
		}
	case "date":
		if s, ok := val.(string); ok {
			if t, err := time.Parse("2006-01-02", s); err == nil {
				return t, true
			}
		}
	case "string":
		switch v := val.(type) {
		case bool:
			if v {
				return "true", true
			}
			return "false", true
		case int:
			return strconv.Itoa(v), true
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64), true
		case time.Time:
			return v.Format("2006-01-02"), true
		}
	case "list":
		switch v := val.(type) {
		case string:
			return []any{v}, true
		case int:
			return []any{v}, true
		case bool:
			return []any{v}, true
		case float64:
			return []any{v}, true
		case time.Time:
			return []any{v}, true
		}
	}
	return nil, false
}

func canonicalizeString(s string, allowed []string) (string, bool) {
	lower := strings.ToLower(s)
	var matches []string
	for _, a := range allowed {
		if strings.ToLower(a) == lower {
			matches = append(matches, a)
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return "", false
}

func canonicalizeAll(val any, allowed []string, isWikilink bool) (any, []string) {
	switch v := val.(type) {
	case string:
		bare := v
		if isWikilink {
			bare = stripWikilink(v)
		}
		if isAllowedValue(bare, allowed) {
			return val, nil
		}
		canon, ok := canonicalizeString(bare, allowed)
		if !ok {
			return val, []string{bare}
		}
		if isWikilink {
			return "[[" + canon + "]]", nil
		}
		return canon, nil
	case []any:
		out := make([]any, len(v))
		copy(out, v)
		changed := false
		var unfixable []string
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				continue
			}
			bare := s
			if isWikilink {
				bare = stripWikilink(s)
			}
			if isAllowedValue(bare, allowed) {
				continue
			}
			canon, ok := canonicalizeString(bare, allowed)
			if !ok {
				unfixable = append(unfixable, bare)
				continue
			}
			if isWikilink {
				out[i] = "[[" + canon + "]]"
			} else {
				out[i] = canon
			}
			changed = true
		}
		if changed {
			return out, unfixable
		}
		return val, unfixable
	}
	return val, nil
}

func isAllowedValue(s string, allowed []string) bool {
	return slices.Contains(allowed, s)
}

func wrapAllWikilinks(val any) any {
	switch v := val.(type) {
	case string:
		return wrapSingleWikilink(v)
	case []any:
		out := make([]any, len(v))
		changed := false
		for i, item := range v {
			if s, ok := item.(string); ok {
				w := wrapSingleWikilink(s)
				out[i] = w
				if w != s {
					changed = true
				}
			} else {
				out[i] = item
			}
		}
		if changed {
			return out
		}
		return v
	}
	return val
}

func wrapSingleWikilink(s string) string {
	if s == "" || (strings.HasPrefix(s, "[[") && strings.HasSuffix(s, "]]")) {
		return s
	}
	s = strings.TrimPrefix(s, "[[")
	s = strings.TrimSuffix(s, "]]")
	return "[[" + s + "]]"
}

func dedupList(list []any) []any {
	seen := make([]any, 0, len(list))
	for _, item := range list {
		dup := false
		for _, s := range seen {
			if reflect.DeepEqual(item, s) {
				dup = true
				break
			}
		}
		if !dup {
			seen = append(seen, item)
		}
	}
	return seen
}
