package cockpit

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/schema"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// BuildQueue loads the vault once and returns the deterministic read-only queue
// of hygiene tasks. It groups frontmatter parse errors, schema violations,
// broken link targets, and duplicate titles. It never mutates the vault.
func BuildQueue(v *vault.Vault, cfg *config.Config) (Queue, error) {
	if v == nil {
		return Queue{}, fmt.Errorf("cockpit: nil vault")
	}
	data, err := loadVaultData(v)
	if err != nil {
		return Queue{}, err
	}
	return buildQueueFromData(data, cfg, v.Root), nil
}

// buildQueueFromData assembles tasks from an already-loaded vault snapshot. It
// is the deterministic core shared by BuildQueue and the package tests.
func buildQueueFromData(data vaultData, cfg *config.Config, root string) Queue {
	if cfg == nil {
		cfg = &config.Config{}
	}

	var tasks []Task
	tasks = append(tasks, frontmatterErrorTasks(data.frontmatterErrors, root)...)
	tasks = append(tasks, schemaViolationTasks(data.loaded, cfg)...)
	tasks = append(tasks, brokenTargetTasks(data.loaded, data.graph)...)
	tasks = append(tasks, duplicateTitleTasks(data.loaded, data.graph)...)
	tasks = append(tasks, issueNoteTasks(data.loaded, data.graph, query.Orphan(data.graph), RuleOrphanNote)...)
	tasks = append(tasks, issueNoteTasks(data.loaded, data.graph, query.DeadEnd(data.graph), RuleDeadEndNote)...)
	tasks = append(tasks, longTailValueTasks(data.loaded, cfg.Cockpit.LongTailLimit)...)

	sortTasks(tasks)
	return Queue{
		Tasks: tasks,
		Counts: Counts{
			Active:         len(tasks),
			TotalGenerated: len(tasks),
		},
		Warnings: data.warnings,
	}
}

func frontmatterErrorTasks(errors []fileError, root string) []Task {
	tasks := make([]Task, 0, len(errors))
	for _, fileErr := range errors {
		affected := []AffectedNote{{
			Title:    strings.TrimSuffix(filepath.Base(fileErr.rel), filepath.Ext(fileErr.rel)),
			RelPath:  fileErr.rel,
			Folder:   folderForRel(fileErr.rel),
			Location: "frontmatter",
			FileHash: fileHash(filepath.Join(root, filepath.FromSlash(fileErr.rel))),
		}}
		tasks = append(tasks, newTask(
			RuleFrontmatterError,
			"Frontmatter parse error: "+fileErr.rel,
			fileErr.rel+" has invalid frontmatter",
			fileErr.err,
			affected,
		))
	}
	return tasks
}

func schemaViolationTasks(notes []loadedNote, cfg *config.Config) []Task {
	if len(cfg.Frontmatter.Schema) == 0 {
		return nil
	}

	groups := map[schemaGroupKey][]AffectedNote{}
	for _, item := range notes {
		front, err := item.note.Front()
		if err != nil {
			continue
		}
		for _, violation := range schema.ValidateNote(front, item.rel, cfg.Frontmatter.Schema, cfg.Frontmatter.WikilinkProperties) {
			key := schemaGroupKey{Property: violation.Property, Kind: violation.Kind, Detail: violation.Detail}
			groups[key] = append(groups[key], affectedForNote(item, "frontmatter."+violation.Property))
		}
	}

	keys := make([]schemaGroupKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Property != keys[j].Property {
			return keys[i].Property < keys[j].Property
		}
		if keys[i].Kind != keys[j].Kind {
			return keys[i].Kind < keys[j].Kind
		}
		return keys[i].Detail < keys[j].Detail
	})

	tasks := make([]Task, 0, len(keys))
	for _, key := range keys {
		affected := groups[key]
		sortAffected(affected)
		label := schemaLabel(key)
		tasks = append(tasks, newTask(
			RuleSchemaViolation,
			"Schema violation: "+label,
			countSummary(len(affected), "note violates "+label, "notes violate "+label),
			label,
			affected,
		))
	}
	return tasks
}

func brokenTargetTasks(notes []loadedNote, graph *query.Graph) []Task {
	if graph == nil {
		return nil
	}

	type targetGroup struct {
		count    int
		affected []AffectedNote
	}

	groups := map[string]*targetGroup{}
	for _, item := range notes {
		if graph.KeyForNote(item.note) == "" {
			continue
		}
		for _, issue := range query.UnresolvedLinks(item.note, graph) {
			key := graph.UnresolvedTargetKey(issue.Link.Target)
			if key == "" {
				key = strings.ToLower(strings.TrimSpace(issue.Link.Target))
			}
			if key == "" {
				continue
			}
			group := groups[key]
			if group == nil {
				group = &targetGroup{}
				groups[key] = group
			}
			group.count++
			group.affected = append(group.affected, affectedForNote(item, brokenLocation(issue.Line, issue.FrontmatterKey)))
		}
	}

	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	tasks := make([]Task, 0, len(keys))
	for _, key := range keys {
		group := groups[key]
		sortAffected(group.affected)
		task := newTask(
			RuleBrokenTarget,
			"Broken target: "+key,
			countSummary(group.count, "unresolved link to "+key, "unresolved links to "+key),
			key,
			group.affected,
		)
		task.Count = group.count
		tasks = append(tasks, task)
	}
	return tasks
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

func newTask(kind RuleKind, title, summary, detail string, affected []AffectedNote) Task {
	sortAffected(affected)
	signature := taskSignature(kind, detail, affected)
	return Task{
		ID:         taskIDPrefix + signature,
		Signature:  signature,
		Kind:       kind,
		Priority:   rulePriority(kind),
		Title:      title,
		Summary:    summary,
		Detail:     detail,
		Count:      len(affected),
		Affected:   affected,
		Actions:    actionsForRule(kind),
		TriageOnly: ruleTriageOnly(kind),
	}
}

func affectedForNote(item loadedNote, location string) AffectedNote {
	return AffectedNote{
		Title:    item.note.Title(),
		RelPath:  filepath.ToSlash(item.rel),
		Folder:   folderForRel(item.rel),
		Location: location,
		FileHash: fileHash(item.note.Path),
	}
}

// schemaGroupKey groups schema violations that share the same property, kind,
// and detail so identical violations across notes collapse into one task.
type schemaGroupKey struct {
	Property string
	Kind     string
	Detail   string
}

func schemaLabel(key schemaGroupKey) string {
	switch key.Kind {
	case "missing":
		return fmt.Sprintf("%s missing (%s)", key.Property, key.Detail)
	case "type":
		return fmt.Sprintf("%s wrong type (%s)", key.Property, key.Detail)
	case "value":
		return fmt.Sprintf("%s %s", key.Property, key.Detail)
	default:
		return fmt.Sprintf("%s %s (%s)", key.Property, key.Kind, key.Detail)
	}
}

// brokenLocation describes where an unresolved link sits: a frontmatter key or
// a body line number.
func brokenLocation(line int, frontmatterKey string) string {
	if frontmatterKey != "" {
		return "frontmatter." + frontmatterKey
	}
	if line > 0 {
		return fmt.Sprintf("line %d", line)
	}
	return "unknown"
}

func countSummary(count int, singular, plural string) string {
	if count == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", count, plural)
}

func sortTasks(tasks []Task) {
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].Priority != tasks[j].Priority {
			return tasks[i].Priority < tasks[j].Priority
		}
		if tasks[i].Count != tasks[j].Count {
			return tasks[i].Count > tasks[j].Count
		}
		leftTitle := strings.ToLower(tasks[i].Title)
		rightTitle := strings.ToLower(tasks[j].Title)
		if leftTitle != rightTitle {
			return leftTitle < rightTitle
		}
		if tasks[i].Detail != tasks[j].Detail {
			return tasks[i].Detail < tasks[j].Detail
		}
		return tasks[i].ID < tasks[j].ID
	})
}

func sortAffected(affected []AffectedNote) {
	sort.Slice(affected, func(i, j int) bool {
		leftTitle := strings.ToLower(affected[i].Title)
		rightTitle := strings.ToLower(affected[j].Title)
		if leftTitle != rightTitle {
			return leftTitle < rightTitle
		}
		if affected[i].Folder != affected[j].Folder {
			return affected[i].Folder < affected[j].Folder
		}
		if affected[i].RelPath != affected[j].RelPath {
			return affected[i].RelPath < affected[j].RelPath
		}
		return affected[i].Location < affected[j].Location
	})
}
