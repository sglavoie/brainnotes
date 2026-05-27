package schema

import (
	"fmt"
	"math"
	"math/bits"
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

type wikilinkRepairParts struct {
	target  string
	heading string
	alias   string
	full    bool
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

	frontKeys := front.Keys()
	sort.Strings(frontKeys)
	for _, key := range frontKeys {
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

	if skipRest {
		return
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
			// Use the platform-sized int bounds, not int64. The upper bound is
			// exclusive so float64(MaxInt) rounding on 64-bit platforms still
			// rejects 2^63 (already outside the signed-int range).
			intUpperExclusive := math.Ldexp(1, bits.UintSize-1)
			intLowerInclusive := -intUpperExclusive
			if f >= intUpperExclusive || f < intLowerInclusive {
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
			if !intExactlyRepresentableAsFloat64(n) {
				return nil, false
			}
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
			return scalarToString(v), true
		case time.Time:
			if isDateOnlyTime(v) {
				return v.Format("2006-01-02"), true
			}
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

func intExactlyRepresentableAsFloat64(n int) bool {
	n64 := int64(n)
	if n64 == 0 {
		return true
	}

	var magnitude uint64
	if n64 < 0 {
		magnitude = uint64(-(n64 + 1)) + 1
	} else {
		magnitude = uint64(n64)
	}
	oddPart := magnitude >> bits.TrailingZeros64(magnitude)
	return bits.Len64(oddPart) <= 53
}

func canonicalizeAll(val any, allowed []string, isWikilink bool) (any, []string) {
	switch v := val.(type) {
	case string:
		if isWikilink {
			canon, ok := canonicalizeWikilinkString(v, allowed)
			if !ok {
				return val, []string{stripWikilink(v)}
			}
			return canon, nil
		}
		if isAllowedValue(v, allowed) {
			return val, nil
		}
		canon, ok := canonicalizeString(v, allowed)
		if !ok {
			return val, []string{v}
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
				scalar := scalarToString(item)
				if scalar == "" {
					unfixable = appendUniqueString(unfixable, fmt.Sprintf("%v", item))
					continue
				}
				if isAllowedValue(scalar, allowed) {
					continue
				}
				unfixable = appendUniqueString(unfixable, scalar)
				continue
			}
			if isWikilink {
				canon, ok := canonicalizeWikilinkString(s, allowed)
				if ok {
					if canon != s {
						out[i] = canon
						changed = true
					}
					continue
				}
				unfixable = appendUniqueString(unfixable, stripWikilink(s))
				continue
			}
			if isAllowedValue(s, allowed) {
				continue
			}
			canon, ok := canonicalizeString(s, allowed)
			if !ok {
				unfixable = appendUniqueString(unfixable, s)
				continue
			}
			out[i] = canon
			changed = true
		}
		if changed {
			return out, unfixable
		}
		return val, unfixable
	default:
		// For non-string scalar types (int, bool, float64, time.Time),
		// convert to the canonical string representation and check
		// against the allowed list. Case-insensitive canonicalization
		// is not applicable to numeric/bool/date types, so mismatches
		// are reported as unfixable.
		s := scalarToString(v)
		if s == "" {
			return val, nil
		}
		if isAllowedValue(s, allowed) {
			return val, nil
		}
		return val, []string{s}
	}
}

func repairWikilinkTarget(s string) string {
	parts, ok := parseWikilinkRepairParts(s)
	if !ok {
		return s
	}
	return parts.target
}

func isFullWikilink(s string) bool {
	parts, ok := parseWikilinkRepairParts(s)
	return ok && parts.full
}

func isAllowedValue(s string, allowed []string) bool {
	return slices.Contains(allowed, s)
}

func appendUniqueString(values []string, value string) []string {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
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
	parts, ok := parseWikilinkRepairParts(s)
	if !ok || parts.full {
		return s
	}
	return formatWikilinkRepairParts(parts, true)
}

// wikilinkRepairTarget extracts the target from forms that are safe for repair:
// bare strings, fully wrapped `[[target]]`, and simple one-sided partial wraps
// (`[[target` or `target]]`). Values containing embedded bracket markers or
// extra text around a wrapped link are left untouched because wrapping them
// would produce malformed output such as `[[[[X]] B]]`.
func wikilinkRepairTarget(s string) (target string, full bool, ok bool) {
	parts, ok := parseWikilinkRepairParts(s)
	if !ok {
		return "", false, false
	}
	return parts.target, parts.full, true
}

func parseWikilinkRepairParts(s string) (wikilinkRepairParts, bool) {
	if s == "" {
		return wikilinkRepairParts{}, false
	}
	starts := strings.HasPrefix(s, "[[")
	ends := strings.HasSuffix(s, "]]")
	openCount := strings.Count(s, "[[")
	closeCount := strings.Count(s, "]]")

	var raw string
	full := false
	switch {
	case starts && ends && len(s) >= 4 && openCount == 1 && closeCount == 1:
		raw = s[2 : len(s)-2]
		full = true
	case starts && !ends && openCount == 1 && closeCount == 0 && len(s) > 2:
		raw = s[2:]
	case !starts && ends && openCount == 0 && closeCount == 1 && len(s) > 2:
		raw = s[:len(s)-2]
	case openCount == 0 && closeCount == 0:
		raw = s
	default:
		return wikilinkRepairParts{}, false
	}

	parts, ok := splitWikilinkRepairContent(raw)
	if !ok {
		return wikilinkRepairParts{}, false
	}
	parts.full = full
	return parts, true
}

func splitWikilinkRepairContent(raw string) (wikilinkRepairParts, bool) {
	if raw == "" || strings.Contains(raw, "[[") || strings.Contains(raw, "]]") {
		return wikilinkRepairParts{}, false
	}

	parts := wikilinkRepairParts{}
	targetPart := raw
	if pipe := strings.IndexByte(targetPart, '|'); pipe >= 0 {
		parts.alias = targetPart[pipe+1:]
		targetPart = targetPart[:pipe]
	}

	if hash := strings.IndexByte(targetPart, '#'); hash >= 0 {
		parts.heading = targetPart[hash+1:]
		targetPart = targetPart[:hash]
	}

	if targetPart == "" {
		return wikilinkRepairParts{}, false
	}

	parts.target = targetPart
	return parts, true
}

func canonicalizeWikilinkString(s string, allowed []string) (string, bool) {
	parts, ok := parseWikilinkRepairParts(s)
	if !ok {
		return "", false
	}

	raw := formatWikilinkRepairParts(parts, false)
	switch {
	case isAllowedValue(raw, allowed), isAllowedValue(parts.target, allowed):
		return s, true
	}

	if canon, ok := canonicalizeString(raw, allowed); ok {
		if parts.full {
			return "[[" + canon + "]]", true
		}
		return canon, true
	}

	if canon, ok := canonicalizeString(parts.target, allowed); ok {
		parts.target = canon
		return formatWikilinkRepairParts(parts, parts.full), true
	}

	return "", false
}

func formatWikilinkRepairParts(parts wikilinkRepairParts, full bool) string {
	raw := parts.target
	if parts.heading != "" {
		raw += "#" + parts.heading
	}
	if parts.alias != "" {
		raw += "|" + parts.alias
	}
	if full {
		return "[[" + raw + "]]"
	}
	return raw
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
