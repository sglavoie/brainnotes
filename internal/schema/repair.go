package schema

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"

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
		if !checkPropertyType(front, key, working, rule.Type) {
			coerced, ok := TryCoerce(working, rule.Type)
			if ok {
				working = coerced
				kinds = append(kinds, "type_coerce")
			} else {
				result.Skips = append(result.Skips, RepairSkip{
					Property: key,
					Kind:     "type_unconvertible",
					Detail:   fmt.Sprintf("cannot safely convert %s to %s", propertyTypeName(front, key, working), rule.Type),
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

func isAllowedValue(s string, allowed []string) bool {
	return slices.Contains(allowed, s)
}

func appendUniqueString(values []string, value string) []string {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
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
