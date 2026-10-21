package cockpit

import (
	"sort"
	"strings"

	"github.com/sglavoie/brainnotes/internal/query"
)

// defaultLongTailLimit caps how many of the rarest values per property become
// long-tail tasks. It mirrors the web diagnostics limit and the [cockpit]
// long_tail_limit config key.
const defaultLongTailLimit = 5

// defaultLongTailMaxCount / defaultLongTailHeadFactor are the rarity thresholds
// applied before the rarest-limit cap: a value is flagged only when it is used
// at most defaultLongTailMaxCount times AND the property has a dominant sibling
// value used at least defaultLongTailHeadFactor times as often (something to
// consolidate into). They mirror the [cockpit] long_tail_max_count /
// long_tail_head_factor config keys and keep the core correct when the helper
// is handed a bare 0 (as several tests do via &config.Config{}).
const (
	defaultLongTailMaxCount   = 2
	defaultLongTailHeadFactor = 3
)

// issueNoteTasks builds one triage task per note matching pred (orphan or
// dead-end). It mirrors the web review queue's per-note issue items so the two
// views stay behavior-compatible. Each task carries a single affected note.
func issueNoteTasks(notes []loadedNote, graph *query.Graph, pred query.Predicate, kind RuleKind) []Task {
	if graph == nil || pred == nil {
		return nil
	}

	tasks := make([]Task, 0)
	for _, item := range notes {
		if graph.KeyForNote(item.note) == "" || !pred(item.note) {
			continue
		}
		affected := []AffectedNote{affectedForNote(item, "")}
		title := affected[0].Title
		if title == "" {
			title = affected[0].RelPath
		}

		var taskTitle, summary string
		switch kind {
		case RuleOrphanNote:
			taskTitle = "Orphan note: " + title
			summary = title + " has no inbound links"
		case RuleDeadEndNote:
			taskTitle = "Dead-end note: " + title
			summary = title + " has no outgoing links"
		default:
			taskTitle = title
			summary = title
		}

		tasks = append(tasks, newTask(kind, taskTitle, summary, affected[0].RelPath, affected))
	}
	return tasks
}

// longTailKey identifies a single (property, value) pair while collecting the
// notes that use it.
type longTailKey struct {
	property string
	value    string
}

// longTailValueTasks builds one triage task per rarely-used frontmatter value.
// For each property it selects the qualifying rare values via
// SelectLongTailValues (the same helper the web diagnostics long-tail card
// uses) and lists the notes that use each value as the affected set.
func longTailValueTasks(notes []loadedNote, limit, maxCount, headFactor int, ignore []string) []Task {
	if limit <= 0 {
		limit = defaultLongTailLimit
	}
	if maxCount <= 0 {
		maxCount = defaultLongTailMaxCount
	}
	if headFactor <= 0 {
		headFactor = defaultLongTailHeadFactor
	}
	ignored := LongTailIgnoreSet(ignore)

	countsByProperty := map[string]map[string]int{}
	notesByValue := map[longTailKey][]loadedNote{}
	for _, item := range notes {
		front, err := item.note.Front()
		if err != nil {
			continue
		}
		for _, key := range front.Keys() {
			value, ok := front.Get(key)
			if !ok {
				continue
			}
			counts := countsByProperty[key]
			if counts == nil {
				counts = map[string]int{}
				countsByProperty[key] = counts
			}
			seen := map[string]bool{}
			for _, str := range longTailValueStrings(value) {
				counts[str]++
				if !seen[str] {
					seen[str] = true
					vk := longTailKey{property: key, value: str}
					notesByValue[vk] = append(notesByValue[vk], item)
				}
			}
		}
	}

	properties := make([]string, 0, len(countsByProperty))
	for property := range countsByProperty {
		properties = append(properties, property)
	}
	sort.Strings(properties)

	tasks := make([]Task, 0)
	for _, property := range properties {
		if ignored[property] {
			continue
		}
		for _, value := range SelectLongTailValues(countsByProperty[property], limit, maxCount, headFactor) {
			group := notesByValue[longTailKey{property: property, value: value}]
			affected := make([]AffectedNote, 0, len(group))
			for _, item := range group {
				affected = append(affected, affectedForNote(item, "frontmatter."+property))
			}
			detail := property + " = " + value
			tasks = append(tasks, newTask(
				RuleLongTailValue,
				"Long-tail value: "+detail,
				countSummary(len(affected), "note uses "+detail, "notes use "+detail),
				detail,
				affected,
			))
		}
	}
	return tasks
}

// SelectLongTailValues returns the non-empty values from counts worth flagging
// for consolidation, sorted count-descending then value-ascending and capped at
// limit (rarest first). A value V qualifies only when it is rare and the
// property has a dominant sibling to consolidate into:
//
//	count(V) <= maxCount                   (V is rare)
//	AND globalMax >= headFactor * count(V) (a dominant sibling exists)
//
// where globalMax is the highest count among the property's values. The second
// condition automatically excludes the head value itself (for headFactor >= 2)
// and any property whose values are all equally rare (e.g. unique-per-note
// fields where globalMax == count(V)). limit/maxCount/headFactor fall back to
// their package defaults when handed a non-positive value, so the helper stays
// correct when called with an unconfigured config. Both the cockpit queue and
// the web diagnostics long-tail card call this helper so the two views can
// never drift.
func SelectLongTailValues(counts map[string]int, limit, maxCount, headFactor int) []string {
	if limit <= 0 {
		limit = defaultLongTailLimit
	}
	if maxCount <= 0 {
		maxCount = defaultLongTailMaxCount
	}
	if headFactor <= 0 {
		headFactor = defaultLongTailHeadFactor
	}

	globalMax := 0
	for value, count := range counts {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if count > globalMax {
			globalMax = count
		}
	}

	type valueCount struct {
		value string
		count int
	}

	values := make([]valueCount, 0, len(counts))
	for value, count := range counts {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if count <= maxCount && globalMax >= headFactor*count {
			values = append(values, valueCount{value: value, count: count})
		}
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].count != values[j].count {
			return values[i].count > values[j].count
		}
		return values[i].value < values[j].value
	})
	if len(values) > limit {
		values = values[len(values)-limit:]
	}

	out := make([]string, len(values))
	for i, vc := range values {
		out[i] = vc.value
	}
	return out
}

// LongTailIgnoreSet builds the lookup set of frontmatter property names the
// long-tail rule must never flag, from the configured
// [cockpit] long_tail_ignore_properties list. Entries are trimmed and empty
// entries skipped; matching is case-sensitive against the frontmatter key.
// Both the cockpit queue and the web diagnostics long-tail card use this helper
// so the two views apply the same exclusions and can never drift. Returns a
// non-nil (possibly empty) set so callers can index it unconditionally.
func LongTailIgnoreSet(ignore []string) map[string]bool {
	set := make(map[string]bool, len(ignore))
	for _, name := range ignore {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		set[trimmed] = true
	}
	return set
}

// longTailValueStrings flattens a frontmatter value into its comparable string
// forms. Lists contribute one string per element; maps contribute nothing. It
// mirrors the web diagnostics value counter so long-tail selection matches.
func longTailValueStrings(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case []any:
		var out []string
		for _, item := range typed {
			out = append(out, longTailValueStrings(item)...)
		}
		return out
	case []string:
		return append([]string(nil), typed...)
	case map[string]any:
		return nil
	default:
		// Scalars (string, bool, ints, float, time) stringify the same way the
		// review-metadata helpers compare them.
		return []string{scalarString(typed)}
	}
}
