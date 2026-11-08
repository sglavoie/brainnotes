package cockpit

import (
	"sort"
	"strings"

	"github.com/sglavoie/brainnotes/internal/query"
)

// brokenTargetGroup is every unresolved link to one target. The group key is
// folded so differently cased links share one task, but a task titled
// "Broken target: project alpha" for [[Project Alpha]] reads as a different
// note, so display keeps a spelling as written: the lexically smallest one, as
// duplicateTitleTasks picks, so the title does not depend on walk order. The
// key stays the task's Detail and so its signature.
type brokenTargetGroup struct {
	count    int
	display  string
	affected []AffectedNote
}

func brokenTargetTasks(notes []loadedNote, graph *query.Graph) []Task {
	if graph == nil {
		return nil
	}
	groups := groupBrokenTargets(notes, graph)

	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	tasks := make([]Task, 0, len(keys))
	for _, key := range keys {
		group := groups[key]
		sortAffected(group.affected)
		display := group.display
		if display == "" {
			display = key
		}
		task := newTask(
			RuleBrokenTarget,
			"Broken target: "+display,
			countSummary(group.count, "unresolved link to "+display, "unresolved links to "+display),
			key,
			group.affected,
		)
		task.Count = group.count
		tasks = append(tasks, task)
	}
	return tasks
}

// groupBrokenTargets collects the unresolved links of every graph note, keyed
// by brokenTargetKey (fix_relink.go), the key the relink and stub fixes match.
func groupBrokenTargets(notes []loadedNote, graph *query.Graph) map[string]*brokenTargetGroup {
	groups := map[string]*brokenTargetGroup{}
	for _, item := range notes {
		if graph.KeyForNote(item.note) == "" {
			continue
		}
		for _, issue := range query.UnresolvedLinks(item.note, graph) {
			key := brokenTargetKey(graph, issue.Link.Target)
			if key == "" {
				continue
			}
			group := groups[key]
			if group == nil {
				group = &brokenTargetGroup{}
				groups[key] = group
			}
			if spelled := strings.TrimSpace(issue.Link.Target); group.display == "" || spelled < group.display {
				group.display = spelled
			}
			group.count++
			group.affected = append(group.affected, affectedForNote(item, brokenLocation(issue.Line, issue.FrontmatterKey)))
		}
	}
	return groups
}

func duplicateTitleTasks(notes []loadedNote, graph *query.Graph) []Task {
	if graph == nil {
		return nil
	}

	notesByTitle := map[string][]loadedNote{}
	displayByTitle := map[string]string{}
	for _, item := range notes {
		if graph.KeyForNote(item.note) == "" {
			continue
		}
		titleKey := strings.ToLower(item.note.Title())
		if titleKey == "" {
			continue
		}
		notesByTitle[titleKey] = append(notesByTitle[titleKey], item)
		if displayByTitle[titleKey] == "" || item.note.Title() < displayByTitle[titleKey] {
			displayByTitle[titleKey] = item.note.Title()
		}
	}

	keys := make([]string, 0, len(notesByTitle))
	for key, group := range notesByTitle {
		if len(group) >= 2 {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		return strings.ToLower(displayByTitle[keys[i]]) < strings.ToLower(displayByTitle[keys[j]])
	})

	tasks := make([]Task, 0, len(keys))
	for _, key := range keys {
		group := notesByTitle[key]
		affected := make([]AffectedNote, 0, len(group))
		for _, item := range group {
			affected = append(affected, affectedForNote(item, ""))
		}
		sortAffected(affected)
		title := displayByTitle[key]
		tasks = append(tasks, newTask(
			RuleDuplicateTitle,
			"Duplicate title: "+title,
			countSummary(len(affected), "note uses title "+title, "notes use title "+title),
			strings.ToLower(title),
			affected,
		))
	}
	return tasks
}
