package schema

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/frontmatter"
	"github.com/sglavoie/brainnotes/internal/textnorm"
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

// PlanRepairs inspects a note's frontmatter against the configured property
// definitions, returning safe mechanical fixes and reasons for skipped issues.
// Each property produces at most one action (combining all applicable fix
// categories) and zero or more skips.
//
// Fix categories applied in order per property:
//  1. type_coerce — convert to the defined kind when lossless
//  2. canonicalize — case-insensitive match to an allowed value
//  3. wikilink_wrap — wrap bare strings in [[ ]] for wikilink properties
//  4. list_dedup — remove duplicate list entries
func PlanRepairs(front *frontmatter.Map, props map[string]config.PropertyDefinition) RepairResult {
	var result RepairResult
	if front == nil {
		return result
	}

	processed := make(map[string]bool)

	definedKeys := make([]string, 0, len(props))
	for k := range props {
		definedKeys = append(definedKeys, k)
	}
	sort.Strings(definedKeys)

	for _, key := range definedKeys {
		def := props[key]
		processed[key] = true

		if !front.Has(key) {
			if def.Required {
				result.Skips = append(result.Skips, RepairSkip{
					Property: key,
					Kind:     "missing_required",
					Detail:   "required property is missing; no default available (" + def.Summary() + ")",
				})
			}
			continue
		}

		planProperty(&result, front, key, &def)
	}

	frontKeys := front.Keys()
	sort.Strings(frontKeys)
	for _, key := range frontKeys {
		if processed[key] {
			continue
		}
		planProperty(&result, front, key, nil)
	}

	return result
}

func planProperty(result *RepairResult, front *frontmatter.Map, key string, def *config.PropertyDefinition) {
	val, _ := front.Get(key)
	working := val
	var kinds []string
	skipRest := false
	isWikilink := def != nil && def.IsWikilink()

	if def != nil {
		if ok, _ := CheckDefinition(front, key, working, *def); !ok {
			coerced, ok := TryCoerce(working, *def)
			if ok {
				working = coerced
				kinds = append(kinds, "type_coerce")
			} else {
				result.Skips = append(result.Skips, RepairSkip{
					Property: key,
					Kind:     "type_unconvertible",
					Detail:   fmt.Sprintf("cannot safely convert %s to %s", propertyTypeName(front, key, working), KindLabel(*def)),
				})
				skipRest = true
			}
		}

		if !skipRest && len(def.Allowed) > 0 {
			canonized, unfixable := canonicalizeAll(working, def.Allowed, isWikilink)
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

// dedupList drops repeated list items, keeping the first occurrence. Two
// strings are one item when their NFC forms are equal, so a decomposed and a
// composed `café` collapse to whichever spelling came first; every other value
// compares with reflect.DeepEqual.
func dedupList(list []any) []any {
	seen := make([]any, 0, len(list))
	for _, item := range list {
		if !slices.ContainsFunc(seen, func(s any) bool { return sameListItem(item, s) }) {
			seen = append(seen, item)
		}
	}
	return seen
}

func sameListItem(a, b any) bool {
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok && bok {
		return as == bs || textnorm.NFC(as) == textnorm.NFC(bs)
	}
	return reflect.DeepEqual(a, b)
}
