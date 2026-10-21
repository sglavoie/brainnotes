package cockpit

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/query"
	"github.com/sglavoie/brainnotes/internal/schema"
	"github.com/sglavoie/brainnotes/internal/vault"
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
	return buildQueueFromData(data, cfg, v.Root, time.Now()), nil
}

// LoadedNote pairs an already-parsed note with its vault-relative slash path.
// Callers that have walked the vault themselves (e.g. the web diagnostics
// loader) hand these to BuildQueueFromSnapshot to avoid a second walk.
type LoadedNote struct {
	Note    *vault.Note
	RelPath string
}

// ParseError records a note whose frontmatter could not be parsed, keyed by its
// vault-relative slash path. These become frontmatter-error tasks.
type ParseError struct {
	RelPath string
	Err     string
}

// Snapshot is a preloaded vault view: the parsed notes, the link graph built
// from them, and any frontmatter parse errors. It lets a caller that already
// loaded the vault reuse that work instead of triggering a second walk. The
// notes, graph, and parse errors must describe the same load, and RelPath must
// be the same forward-slash vault-relative path BuildQueue derives, or the
// generated queue may diverge from BuildQueue's.
type Snapshot struct {
	Notes             []LoadedNote
	Graph             *query.Graph
	FrontmatterErrors []ParseError
}

// BuildQueueFromSnapshot assembles the deterministic queue from an
// already-loaded vault view, avoiding the walk BuildQueue performs. Given a
// snapshot describing the same load, it produces the identical queue: it feeds
// the same buildQueueFromData core BuildQueue uses.
func BuildQueueFromSnapshot(snapshot Snapshot, cfg *config.Config, root string) Queue {
	return buildQueueFromData(vaultDataFromSnapshot(snapshot), cfg, root, time.Now())
}

// vaultDataFromSnapshot adapts the exported Snapshot into the package-internal
// vaultData a single load produces. Both BuildQueueFromSnapshot and the
// snapshot-backed Fixer path (loadContext) share it so a caller that already
// loaded the vault reuses that work instead of triggering a second walk.
func vaultDataFromSnapshot(snapshot Snapshot) vaultData {
	loaded := make([]loadedNote, 0, len(snapshot.Notes))
	notes := make([]*vault.Note, 0, len(snapshot.Notes))
	for _, item := range snapshot.Notes {
		loaded = append(loaded, loadedNote{note: item.Note, rel: item.RelPath})
		notes = append(notes, item.Note)
	}
	parseErrors := make([]fileError, 0, len(snapshot.FrontmatterErrors))
	for _, item := range snapshot.FrontmatterErrors {
		parseErrors = append(parseErrors, fileError{rel: item.RelPath, err: item.Err})
	}
	sort.Slice(parseErrors, func(i, j int) bool {
		return parseErrors[i].rel < parseErrors[j].rel
	})
	return vaultData{
		loaded:            loaded,
		notes:             notes,
		graph:             snapshot.Graph,
		frontmatterErrors: parseErrors,
	}
}

// buildQueueFromData assembles tasks from an already-loaded vault snapshot. It
// is the deterministic core shared by BuildQueue and the package tests. The now
// argument is the clock the review-metadata rules (age/staleness, wired up in
// later sessions) measure against; the public paths pass time.Now() while tests
// pass a fixed instant for reproducible age computations.
func buildQueueFromData(data vaultData, cfg *config.Config, root string, now time.Time) Queue {
	if cfg == nil {
		cfg = &config.Config{}
	}

	var tasks []Task
	tasks = append(tasks, frontmatterErrorTasks(data.frontmatterErrors, root)...)
	// Sensitive-content advisory scan (session 4): gated off by default via
	// [cockpit] sensitive_scan; surfaces redacted findings only.
	tasks = append(tasks, sensitiveContentTasks(data.loaded, cfg)...)
	tasks = append(tasks, schemaViolationTasks(data.loaded, cfg)...)
	tasks = append(tasks, brokenTargetTasks(data.loaded, data.graph)...)
	tasks = append(tasks, duplicateTitleTasks(data.loaded, data.graph)...)
	tasks = append(tasks, issueNoteTasks(data.loaded, data.graph, query.Orphan(data.graph), RuleOrphanNote)...)
	tasks = append(tasks, issueNoteTasks(data.loaded, data.graph, query.DeadEnd(data.graph), RuleDeadEndNote)...)
	tasks = append(tasks, longTailValueTasks(data.loaded, cfg.Cockpit.LongTailLimit, cfg.Cockpit.LongTailMaxCount, cfg.Cockpit.LongTailHeadFactor, cfg.Cockpit.LongTailIgnoreProperties)...)
	// Status-drift advisory rules (session 3): review-date drift and the
	// work-in-progress overflow. now is the clock the staleness age measures
	// against; the public paths pass time.Now() while tests pass a fixed instant.
	tasks = append(tasks, reviewDriftTasks(data.loaded, cfg, now)...)
	tasks = append(tasks, wipLimitTasks(data.loaded, cfg)...)
	// Capture-backlog advisory rule: fires when the configured capture inbox
	// holds more captures than [cockpit] max_captures allows. Off unless
	// max_captures is set.
	tasks = append(tasks, captureBacklogTasks(data.loaded, cfg)...)
	// Hub-needs-synthesis advisory rule (session 5): flags hub notes whose
	// configured synthesis headings are present but empty. Lower priority than
	// safety and status findings (priority 80).
	tasks = append(tasks, hubSynthesisTasks(data.loaded, cfg)...)

	if disabled := disabledRuleSet(cfg); disabled != nil {
		kept := tasks[:0]
		for _, task := range tasks {
			if disabled[task.Kind] {
				continue
			}
			kept = append(kept, task)
		}
		tasks = kept
	}
	if override := priorityOverride(cfg); override != nil {
		for i := range tasks {
			if weight, ok := override[tasks[i].Kind]; ok {
				tasks[i].Priority = weight
			}
		}
	}

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
	if len(cfg.Frontmatter.Properties) == 0 {
		return nil
	}

	groups := map[schemaGroupKey][]AffectedNote{}
	for _, item := range notes {
		front, err := item.note.Front()
		if err != nil {
			continue
		}
		for _, violation := range schema.ValidateNote(front, item.rel, cfg.Frontmatter.Properties) {
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

// disabledRuleSet builds the set of rule kinds that config suppresses from the
// queue entirely. Each configured string is treated as a RuleKind; unknown
// names simply match no tasks. Returns nil when nothing is disabled.
func disabledRuleSet(cfg *config.Config) map[RuleKind]bool {
	if len(cfg.Cockpit.DisabledRules) == 0 {
		return nil
	}
	set := make(map[RuleKind]bool, len(cfg.Cockpit.DisabledRules))
	for _, name := range cfg.Cockpit.DisabledRules {
		set[RuleKind(name)] = true
	}
	return set
}

// priorityOverride maps each rule kind named in config to a weight below every
// built-in rulePriority weight (which start at 10), preserving the configured
// order so named kinds surface first. Kinds not named are absent from the map
// and keep their built-in weight; unknown names get a weight but match no task,
// so they have no effect. Returns nil when no override is configured.
func priorityOverride(cfg *config.Config) map[RuleKind]int {
	if len(cfg.Cockpit.Priority) == 0 {
		return nil
	}
	n := len(cfg.Cockpit.Priority)
	order := make(map[RuleKind]int, n)
	for i, name := range cfg.Cockpit.Priority {
		kind := RuleKind(name)
		if _, seen := order[kind]; seen {
			continue
		}
		order[kind] = i - n
	}
	return order
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
