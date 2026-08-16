package web

import (
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/schema"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

type diagnosticsSummary struct {
	Cards             []diagnosticCard
	BrokenTargets     []diagnosticGroup
	OrphanNotes       []diagnosticNote
	DeadEndNotes      []diagnosticNote
	DuplicateTitles   []diagnosticGroup
	FrontmatterErrors []diagnosticFileError
	SkippedNotes      []diagnosticFileError
	SchemaViolations  []diagnosticGroup
	LongTailValues    []diagnosticLongTailGroup
}

type diagnosticCard struct {
	Key   string
	Label string
	Count int
	Path  string
}

type diagnosticGroup struct {
	Label string
	Count int
	Items []diagnosticNote
	Path  string
}

type diagnosticNote struct {
	Title      string
	Path       string
	Folder     string
	FolderPath string
	Location   string
}

type diagnosticFileError struct {
	Path  string
	Error string
}

type diagnosticLongTailGroup struct {
	Property string
	Values   []diagnosticValueCount
}

type diagnosticValueCount struct {
	Value string
	Count int
}

type diagnosticLoadedNote struct {
	note *vault.Note
	rel  string
}

type diagnosticsData struct {
	loaded            []diagnosticLoadedNote
	notes             []*vault.Note
	graph             *query.Graph
	frontmatterErrors []diagnosticFileError
	skippedNotes      []diagnosticFileError
}

func buildDiagnosticsSummary(v *vault.Vault, cfg *config.Config) (diagnosticsSummary, error) {
	if v == nil {
		return diagnosticsSummary{}, errorsf("nil vault")
	}
	if cfg == nil {
		return diagnosticsSummary{}, errorsf("nil config")
	}

	data, err := loadDiagnosticsData(v)
	if err != nil {
		return diagnosticsSummary{}, err
	}
	return buildDiagnosticsSummaryFromData(data, cfg), nil
}

func loadDiagnosticsData(v *vault.Vault) (diagnosticsData, error) {
	// Load through the shared bounded loader so the diagnostics walk classifies
	// timed-out reads the same way the server snapshot does. This call keeps no
	// persistent quarantine of its own; the shared snapshot owns that.
	result, err := loadVaultNotes(v, nil, 0, nil)
	if err != nil {
		return diagnosticsData{}, err
	}
	notes := make([]*vault.Note, 0, len(result.loaded))
	for _, item := range result.loaded {
		notes = append(notes, item.note)
	}

	return diagnosticsData{
		loaded:            result.loaded,
		notes:             notes,
		graph:             query.BuildGraphForRoot(v.Root, notes),
		frontmatterErrors: result.parseErrors,
		skippedNotes:      result.skipped,
	}, nil
}

// diagnosticsDataFromSnapshot adapts the resilient shared snapshot into the
// diagnosticsData view the homepage diagnostics summary and Daily Fixes panel
// consume, so a single bounded load feeds index data, health diagnostics, and
// fixes counts without re-walking the vault. The snapshot already carries the
// classified frontmatter parse errors and skipped (timed-out) notes; loaded
// pairs each healthy note with its vault-relative path in the snapshot's walk
// order.
func diagnosticsDataFromSnapshot(snapshot vaultSnapshot, root string) diagnosticsData {
	loaded := make([]diagnosticLoadedNote, 0, len(snapshot.notes))
	for _, note := range snapshot.notes {
		if note == nil {
			continue
		}
		loaded = append(loaded, diagnosticLoadedNote{note: note, rel: displayPath(note.Path, root)})
	}
	return diagnosticsData{
		loaded:            loaded,
		notes:             snapshot.notes,
		graph:             snapshot.graph,
		frontmatterErrors: snapshot.parseErrors,
		skippedNotes:      snapshot.skipped,
	}
}

func buildDiagnosticsSummaryFromData(data diagnosticsData, cfg *config.Config) diagnosticsSummary {
	if cfg == nil {
		cfg = &config.Config{}
	}

	summary := diagnosticsSummary{
		BrokenTargets:     diagnosticBrokenTargets(data.loaded, data.graph),
		OrphanNotes:       diagnosticIssueNotes(data.loaded, data.graph, query.Orphan(data.graph)),
		DeadEndNotes:      diagnosticIssueNotes(data.loaded, data.graph, query.DeadEnd(data.graph)),
		DuplicateTitles:   diagnosticDuplicateTitles(data.loaded, data.graph),
		FrontmatterErrors: data.frontmatterErrors,
		SkippedNotes:      data.skippedNotes,
		SchemaViolations:  diagnosticSchemaViolations(data.loaded, data.graph, cfg),
		LongTailValues:    diagnosticLongTailValues(data.loaded, cfg),
	}
	summary.Cards = diagnosticCards(summary)
	return summary
}

func diagnosticCards(summary diagnosticsSummary) []diagnosticCard {
	brokenLinkCount := 0
	for _, group := range summary.BrokenTargets {
		brokenLinkCount += group.Count
	}

	duplicateNoteCount := 0
	for _, group := range summary.DuplicateTitles {
		duplicateNoteCount += group.Count
	}

	longTailCount := 0
	for _, group := range summary.LongTailValues {
		longTailCount += len(group.Values)
	}

	return []diagnosticCard{
		{Key: "broken", Label: "Broken links", Count: brokenLinkCount, Path: healthRoutePath + "#diagnostic-broken-targets"},
		{Key: "orphan", Label: "Orphans", Count: len(summary.OrphanNotes), Path: healthRoutePath + "#diagnostic-orphans"},
		{Key: "dead_end", Label: "Dead ends", Count: len(summary.DeadEndNotes), Path: healthRoutePath + "#diagnostic-dead-ends"},
		{Key: "duplicate_titles", Label: "Duplicate titles", Count: duplicateNoteCount, Path: healthRoutePath + "#diagnostic-duplicate-titles"},
		{Key: "frontmatter_errors", Label: "Frontmatter errors", Count: len(summary.FrontmatterErrors), Path: healthRoutePath + "#diagnostic-frontmatter-errors"},
		{Key: "skipped", Label: "Skipped notes", Count: len(summary.SkippedNotes), Path: healthRoutePath + "#diagnostic-skipped-notes"},
		{Key: "schema_violations", Label: "Schema violations", Count: diagnosticGroupItemCount(summary.SchemaViolations), Path: healthRoutePath + "#diagnostic-schema-violations"},
		{Key: "long_tail", Label: "Long-tail values", Count: longTailCount, Path: healthRoutePath + "#diagnostic-long-tail-values"},
	}
}

func diagnosticBrokenTargets(notes []diagnosticLoadedNote, graph *query.Graph) []diagnosticGroup {
	type targetGroup struct {
		count int
		items []diagnosticNote
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
			group.items = append(group.items, diagnosticNoteForIssue(item, graph, brokenLocation(issue.Line, issue.FrontmatterKey)))
		}
	}

	out := make([]diagnosticGroup, 0, len(groups))
	for key, group := range groups {
		sortDiagnosticNotes(group.items)
		out = append(out, diagnosticGroup{
			Label: key,
			Count: group.count,
			Items: group.items,
			Path:  "/broken?" + url.Values{"q": []string{key}}.Encode(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Label < out[j].Label
	})
	return out
}

func diagnosticIssueNotes(notes []diagnosticLoadedNote, graph *query.Graph, pred query.Predicate) []diagnosticNote {
	items := make([]diagnosticNote, 0)
	for _, item := range notes {
		if graph.KeyForNote(item.note) == "" || !pred(item.note) {
			continue
		}
		note := diagnosticNoteForIssue(item, graph, "")
		items = append(items, note)
	}
	sortDiagnosticNotes(items)
	return items
}

func diagnosticDuplicateTitles(notes []diagnosticLoadedNote, graph *query.Graph) []diagnosticGroup {
	pathsByTitle := map[string][]diagnosticLoadedNote{}
	displayByTitle := map[string]string{}

	for _, item := range notes {
		key := graph.KeyForNote(item.note)
		if key == "" {
			continue
		}
		titleKey := strings.ToLower(item.note.Title())
		if titleKey == "" {
			continue
		}
		pathsByTitle[titleKey] = append(pathsByTitle[titleKey], item)
		if displayByTitle[titleKey] == "" || item.note.Title() < displayByTitle[titleKey] {
			displayByTitle[titleKey] = item.note.Title()
		}
	}

	out := make([]diagnosticGroup, 0)
	for titleKey, items := range pathsByTitle {
		if len(items) < 2 {
			continue
		}
		groupItems := make([]diagnosticNote, 0, len(items))
		for _, item := range items {
			groupItems = append(groupItems, diagnosticNoteForIssue(item, graph, ""))
		}
		sortDiagnosticNotes(groupItems)
		out = append(out, diagnosticGroup{
			Label: displayByTitle[titleKey],
			Count: len(groupItems),
			Items: groupItems,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Label != out[j].Label {
			return strings.ToLower(out[i].Label) < strings.ToLower(out[j].Label)
		}
		return out[i].Count > out[j].Count
	})
	return out
}

func diagnosticSchemaViolations(notes []diagnosticLoadedNote, graph *query.Graph, cfg *config.Config) []diagnosticGroup {
	if len(cfg.Frontmatter.Properties) == 0 {
		return nil
	}

	groups := map[groupKey][]diagnosticNote{}
	for _, item := range notes {
		front, err := item.note.Front()
		if err != nil {
			continue
		}
		violations := schema.ValidateNote(front, item.rel, cfg.Frontmatter.Properties)
		for _, violation := range violations {
			key := groupKey{Property: violation.Property, Kind: violation.Kind, Detail: violation.Detail}
			groups[key] = append(groups[key], diagnosticNoteForIssue(item, graph, ""))
		}
	}

	keys := make([]groupKey, 0, len(groups))
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

	out := make([]diagnosticGroup, 0, len(keys))
	for _, key := range keys {
		items := groups[key]
		sortDiagnosticNotes(items)
		out = append(out, diagnosticGroup{
			Label: diagnosticSchemaLabel(key),
			Count: len(items),
			Items: items,
		})
	}
	return out
}

func diagnosticLongTailValues(notes []diagnosticLoadedNote, cfg *config.Config) []diagnosticLongTailGroup {
	countsByProperty := map[string]map[string]int{}
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
			countDiagnosticValue(value, counts)
		}
	}

	properties := make([]string, 0, len(countsByProperty))
	for property := range countsByProperty {
		properties = append(properties, property)
	}
	sort.Strings(properties)

	limit := cfg.Cockpit.LongTailLimit
	maxCount := cfg.Cockpit.LongTailMaxCount
	headFactor := cfg.Cockpit.LongTailHeadFactor
	// LongTailIgnoreSet is the same helper the cockpit queue uses to skip
	// ignored properties (internal/cockpit/hygiene.go), so the diagnostics card
	// and the queue exclude the identical set and never drift.
	ignored := cockpit.LongTailIgnoreSet(cfg.Cockpit.LongTailIgnoreProperties)

	out := make([]diagnosticLongTailGroup, 0, len(properties))
	for _, property := range properties {
		if ignored[property] {
			continue
		}
		counts := countsByProperty[property]
		// SelectLongTailValues is the single helper shared with the cockpit
		// queue (internal/cockpit/hygiene.go), so the diagnostics card and the
		// queue always flag the identical set. It returns count-desc/value-asc;
		// the card re-sorts ascending below to keep rarest values first.
		selected := cockpit.SelectLongTailValues(counts, limit, maxCount, headFactor)
		if len(selected) == 0 {
			continue
		}
		values := make([]diagnosticValueCount, 0, len(selected))
		for _, value := range selected {
			values = append(values, diagnosticValueCount{Value: value, Count: counts[value]})
		}
		sort.Slice(values, func(i, j int) bool {
			if values[i].Count != values[j].Count {
				return values[i].Count < values[j].Count
			}
			return values[i].Value < values[j].Value
		})
		out = append(out, diagnosticLongTailGroup{Property: property, Values: values})
	}
	return out
}

type groupKey struct {
	Property string
	Kind     string
	Detail   string
}

func diagnosticSchemaLabel(key groupKey) string {
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

func diagnosticGroupItemCount(groups []diagnosticGroup) int {
	total := 0
	for _, group := range groups {
		total += group.Count
	}
	return total
}

func diagnosticNoteForIssue(item diagnosticLoadedNote, graph *query.Graph, location string) diagnosticNote {
	title := item.note.Title()
	path := ""
	if graph != nil {
		if key := graph.KeyForNote(item.note); key != "" {
			if notePath, err := notePathForKey(key); err == nil {
				path = notePath
			}
		}
	}
	if path == "" {
		path = "/n/" + strings.TrimSuffix(strings.ToLower(item.rel), strings.ToLower(filepath.Ext(item.rel)))
	}

	folder := filepath.ToSlash(filepath.Dir(item.rel))
	if folder == "." {
		folder = ""
	}
	folderPath := ""
	if folder != "" {
		folderPath = filterURL("folder", folder)
	}

	return diagnosticNote{
		Title:      title,
		Path:       path,
		Folder:     folder,
		FolderPath: folderPath,
		Location:   location,
	}
}

func sortDiagnosticNotes(items []diagnosticNote) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Title != items[j].Title {
			return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
		}
		if items[i].Folder != items[j].Folder {
			return items[i].Folder < items[j].Folder
		}
		if items[i].Location != items[j].Location {
			return items[i].Location < items[j].Location
		}
		return items[i].Path < items[j].Path
	})
}

func countDiagnosticValue(value any, counts map[string]int) {
	switch typed := value.(type) {
	case nil:
		return
	case string:
		counts[typed]++
	case []any:
		for _, item := range typed {
			countDiagnosticValue(item, counts)
		}
	case []string:
		for _, item := range typed {
			counts[item]++
		}
	case map[string]any:
		return
	case time.Time:
		counts[frontmatter.FormatTimestamp(typed)]++
	case bool:
		counts[strconv.FormatBool(typed)]++
	case int:
		counts[strconv.Itoa(typed)]++
	case int64:
		counts[strconv.FormatInt(typed, 10)]++
	case float64:
		counts[strconv.FormatFloat(typed, 'g', -1, 64)]++
	default:
		counts[fmt.Sprintf("%v", typed)]++
	}
}

func filterURL(key, value string) string {
	return libraryRoutePath + "?" + url.Values{key: []string{value}}.Encode()
}

func displayPath(absPath, vaultRoot string) string {
	if vaultRoot == "" {
		return absPath
	}
	rel, err := filepath.Rel(vaultRoot, absPath)
	if err != nil {
		return absPath
	}
	return filepath.ToSlash(rel)
}
