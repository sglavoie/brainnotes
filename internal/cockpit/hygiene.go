package cockpit

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
)

// defaultLongTailLimit caps how many of the rarest values per property become
// long-tail tasks. It mirrors the web diagnostics limit; a later session wires
// this to the [cockpit] long_tail_limit config key.
const defaultLongTailLimit = 5

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
// For each property it keeps at most limit of the least-frequent values (the
// same selection the web diagnostics long-tail card makes) and lists the notes
// that use each value as the affected set.
func longTailValueTasks(notes []loadedNote, limit int) []Task {
	if limit <= 0 {
		limit = defaultLongTailLimit
	}

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
		for _, value := range selectLongTailValues(countsByProperty[property], limit) {
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

// selectLongTailValues returns the rarest non-empty values from counts, capped
// at limit, sorted count-descending then value-ascending. This matches the web
// diagnostics selection so both views flag the same values.
func selectLongTailValues(counts map[string]int, limit int) []string {
	type valueCount struct {
		value string
		count int
	}

	values := make([]valueCount, 0, len(counts))
	for value, count := range counts {
		if strings.TrimSpace(value) == "" {
			continue
		}
		values = append(values, valueCount{value: value, count: count})
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

// longTailValueStrings flattens a frontmatter value into its comparable string
// forms. Lists contribute one string per element; maps contribute nothing. It
// mirrors the web diagnostics value counter so long-tail selection matches.
func longTailValueStrings(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return []string{typed}
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
	case time.Time:
		return []string{frontmatter.FormatTimestamp(typed)}
	case bool:
		return []string{strconv.FormatBool(typed)}
	case int:
		return []string{strconv.Itoa(typed)}
	case int64:
		return []string{strconv.FormatInt(typed, 10)}
	case float64:
		return []string{strconv.FormatFloat(typed, 'g', -1, 64)}
	default:
		return []string{fmt.Sprintf("%v", typed)}
	}
}
