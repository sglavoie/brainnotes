package web

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/schema"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

type reviewItemKind string

const (
	reviewKindFrontmatterError reviewItemKind = "frontmatter_error"
	reviewKindSchemaViolation  reviewItemKind = "schema_violation"
	reviewKindBrokenTarget     reviewItemKind = "broken_target"
	reviewKindDuplicateTitle   reviewItemKind = "duplicate_title"
	reviewKindOrphanNote       reviewItemKind = "orphan_note"
	reviewKindDeadEndNote      reviewItemKind = "dead_end_note"
)

const reviewItemIDPrefix = "review_"

type reviewQueue struct {
	Items  []reviewItem `json:"items"`
	Counts reviewCounts `json:"counts"`
}

type reviewCounts struct {
	Active         int `json:"active"`
	DoneHidden     int `json:"done_hidden"`
	SnoozedHidden  int `json:"snoozed_hidden"`
	TotalGenerated int `json:"total_generated"`
}

type reviewItem struct {
	ID        string               `json:"id"`
	Signature string               `json:"signature"`
	Kind      reviewItemKind       `json:"kind"`
	Priority  int                  `json:"priority"`
	Title     string               `json:"title"`
	Summary   string               `json:"summary"`
	Detail    string               `json:"detail,omitempty"`
	Count     int                  `json:"count"`
	Affected  []reviewAffectedNote `json:"affected"`
}

type reviewAffectedNote struct {
	Title      string `json:"title"`
	RelPath    string `json:"rel_path"`
	Path       string `json:"path,omitempty"`
	Folder     string `json:"folder,omitempty"`
	FolderPath string `json:"folder_path,omitempty"`
	Location   string `json:"location,omitempty"`
	FileHash   string `json:"-"`
}

func buildReviewQueue(v *vault.Vault, cfg *config.Config) (reviewQueue, error) {
	if v == nil {
		return reviewQueue{}, errorsf("nil vault")
	}
	return buildReviewQueueWithState(v, cfg, newReviewStateStore(v.Root))
}

func buildReviewQueueWithState(v *vault.Vault, cfg *config.Config, store *reviewStateStore) (reviewQueue, error) {
	if v == nil {
		return reviewQueue{}, errorsf("nil vault")
	}

	data, err := loadDiagnosticsData(v)
	if err != nil {
		return reviewQueue{}, err
	}
	queue := buildReviewQueueFromData(data, cfg, v.Root)
	if store == nil {
		return queue, nil
	}
	return store.filterQueue(queue)
}

func buildReviewQueueFromData(data diagnosticsData, cfg *config.Config, root string) reviewQueue {
	if cfg == nil {
		cfg = &config.Config{}
	}

	var items []reviewItem
	items = append(items, reviewFrontmatterErrorItems(data.frontmatterErrors, root)...)
	items = append(items, reviewSchemaViolationItems(data.loaded, data.graph, cfg)...)
	items = append(items, reviewBrokenTargetItems(data.loaded, data.graph)...)
	items = append(items, reviewDuplicateTitleItems(data.loaded, data.graph)...)
	items = append(items, reviewIssueNoteItems(data.loaded, data.graph, query.Orphan(data.graph), reviewKindOrphanNote)...)
	items = append(items, reviewIssueNoteItems(data.loaded, data.graph, query.DeadEnd(data.graph), reviewKindDeadEndNote)...)

	sortReviewItems(items)
	return reviewQueue{
		Items: items,
		Counts: reviewCounts{
			Active:         len(items),
			TotalGenerated: len(items),
		},
	}
}

func reviewFrontmatterErrorItems(errors []diagnosticFileError, root string) []reviewItem {
	items := make([]reviewItem, 0, len(errors))
	for _, fileErr := range errors {
		affected := []reviewAffectedNote{
			reviewAffectedForRel(root, fileErr.Path, "frontmatter"),
		}
		title := "Frontmatter parse error: " + fileErr.Path
		items = append(items, newReviewItem(
			reviewKindFrontmatterError,
			reviewKindPriority(reviewKindFrontmatterError),
			title,
			fmt.Sprintf("%s has invalid frontmatter", fileErr.Path),
			fileErr.Error,
			affected,
		))
	}
	return items
}

func reviewSchemaViolationItems(notes []diagnosticLoadedNote, graph *query.Graph, cfg *config.Config) []reviewItem {
	if cfg == nil || len(cfg.Frontmatter.Schema) == 0 {
		return nil
	}

	groups := map[groupKey][]reviewAffectedNote{}
	for _, item := range notes {
		front, err := item.note.Front()
		if err != nil {
			continue
		}
		for _, violation := range schema.ValidateNote(front, item.rel, cfg.Frontmatter.Schema, cfg.Frontmatter.WikilinkProperties) {
			key := groupKey{Property: violation.Property, Kind: violation.Kind, Detail: violation.Detail}
			groups[key] = append(groups[key], reviewAffectedForLoadedNote(item, graph, "frontmatter."+violation.Property))
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

	items := make([]reviewItem, 0, len(keys))
	for _, key := range keys {
		affected := groups[key]
		sortReviewAffected(affected)
		label := diagnosticSchemaLabel(key)
		items = append(items, newReviewItem(
			reviewKindSchemaViolation,
			reviewKindPriority(reviewKindSchemaViolation),
			"Schema violation: "+label,
			reviewCountSummary(len(affected), "note violates "+label, "notes violate "+label),
			label,
			affected,
		))
	}
	return items
}

func reviewBrokenTargetItems(notes []diagnosticLoadedNote, graph *query.Graph) []reviewItem {
	if graph == nil {
		return nil
	}

	type targetGroup struct {
		count    int
		affected []reviewAffectedNote
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
			group.affected = append(group.affected, reviewAffectedForLoadedNote(item, graph, brokenLocation(issue.Line, issue.FrontmatterKey)))
		}
	}

	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	items := make([]reviewItem, 0, len(keys))
	for _, key := range keys {
		group := groups[key]
		sortReviewAffected(group.affected)
		items = append(items, newReviewItem(
			reviewKindBrokenTarget,
			reviewKindPriority(reviewKindBrokenTarget),
			"Broken target: "+key,
			reviewCountSummary(group.count, "unresolved link to "+key, "unresolved links to "+key),
			key,
			group.affected,
		))
		items[len(items)-1].Count = group.count
	}
	return items
}

func reviewDuplicateTitleItems(notes []diagnosticLoadedNote, graph *query.Graph) []reviewItem {
	if graph == nil {
		return nil
	}

	notesByTitle := map[string][]diagnosticLoadedNote{}
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

	items := make([]reviewItem, 0, len(keys))
	for _, key := range keys {
		group := notesByTitle[key]
		affected := make([]reviewAffectedNote, 0, len(group))
		for _, item := range group {
			affected = append(affected, reviewAffectedForLoadedNote(item, graph, ""))
		}
		sortReviewAffected(affected)
		title := displayByTitle[key]
		items = append(items, newReviewItem(
			reviewKindDuplicateTitle,
			reviewKindPriority(reviewKindDuplicateTitle),
			"Duplicate title: "+title,
			reviewCountSummary(len(affected), "note uses title "+title, "notes use title "+title),
			strings.ToLower(title),
			affected,
		))
	}
	return items
}

func reviewIssueNoteItems(notes []diagnosticLoadedNote, graph *query.Graph, pred query.Predicate, kind reviewItemKind) []reviewItem {
	if graph == nil || pred == nil {
		return nil
	}

	items := make([]reviewItem, 0)
	for _, item := range notes {
		if graph.KeyForNote(item.note) == "" || !pred(item.note) {
			continue
		}
		affected := []reviewAffectedNote{reviewAffectedForLoadedNote(item, graph, "")}
		title := affected[0].Title
		if title == "" {
			title = affected[0].RelPath
		}

		reviewTitle := ""
		summary := ""
		switch kind {
		case reviewKindOrphanNote:
			reviewTitle = "Orphan note: " + title
			summary = title + " has no inbound links"
		case reviewKindDeadEndNote:
			reviewTitle = "Dead-end note: " + title
			summary = title + " has no outgoing links"
		default:
			reviewTitle = title
			summary = title
		}

		items = append(items, newReviewItem(
			kind,
			reviewKindPriority(kind),
			reviewTitle,
			summary,
			affected[0].RelPath,
			affected,
		))
	}
	return items
}

func newReviewItem(kind reviewItemKind, priority int, title, summary, detail string, affected []reviewAffectedNote) reviewItem {
	sortReviewAffected(affected)
	signature := reviewItemSignature(kind, detail, affected)
	return reviewItem{
		ID:        reviewItemIDPrefix + signature,
		Signature: signature,
		Kind:      kind,
		Priority:  priority,
		Title:     title,
		Summary:   summary,
		Detail:    detail,
		Count:     len(affected),
		Affected:  affected,
	}
}

func reviewAffectedForLoadedNote(item diagnosticLoadedNote, graph *query.Graph, location string) reviewAffectedNote {
	path := ""
	if graph != nil {
		if key := graph.KeyForNote(item.note); key != "" {
			if routePath, err := notePathForKey(key); err == nil {
				path = routePath
			}
		}
	}
	if path == "" {
		path = reviewRoutePathForRel(item.rel)
	}

	affected := reviewAffectedMetadataForRel(item.rel, location)
	affected.Title = item.note.Title()
	affected.Path = path
	affected.FileHash = reviewFileHash(item.note.Path)
	return affected
}

func reviewAffectedForRel(root, rel, location string) reviewAffectedNote {
	affected := reviewAffectedMetadataForRel(rel, location)
	affected.FileHash = reviewFileHash(filepath.Join(root, filepath.FromSlash(affected.RelPath)))
	return affected
}

func reviewAffectedMetadataForRel(rel, location string) reviewAffectedNote {
	rel = filepath.ToSlash(rel)
	folder := filepath.ToSlash(filepath.Dir(rel))
	if folder == "." {
		folder = ""
	}
	folderPath := ""
	if folder != "" {
		folderPath = filterURL("folder", folder)
	}

	return reviewAffectedNote{
		Title:      strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel)),
		RelPath:    rel,
		Path:       reviewRoutePathForRel(rel),
		Folder:     folder,
		FolderPath: folderPath,
		Location:   location,
	}
}

func reviewRoutePathForRel(rel string) string {
	rel = filepath.ToSlash(rel)
	key := strings.TrimSuffix(rel, filepath.Ext(rel))
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	path, err := notePathForKey(key)
	if err != nil {
		return ""
	}
	return path
}

func reviewFileHash(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func reviewItemSignature(kind reviewItemKind, detail string, affected []reviewAffectedNote) string {
	h := sha256.New()
	writeReviewSignatureValue(h, "version", "review-item-v1")
	writeReviewSignatureValue(h, "kind", string(kind))
	writeReviewSignatureValue(h, "detail", detail)
	for _, note := range affected {
		writeReviewSignatureValue(h, "rel", note.RelPath)
		writeReviewSignatureValue(h, "location", note.Location)
		writeReviewSignatureValue(h, "hash", note.FileHash)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func writeReviewSignatureValue(h hash.Hash, label, value string) {
	_, _ = fmt.Fprintf(h, "%s:%d:%s\n", label, len(value), value)
}

func reviewKindPriority(kind reviewItemKind) int {
	switch kind {
	case reviewKindFrontmatterError:
		return 10
	case reviewKindSchemaViolation:
		return 20
	case reviewKindBrokenTarget:
		return 30
	case reviewKindDuplicateTitle:
		return 40
	case reviewKindOrphanNote:
		return 50
	case reviewKindDeadEndNote:
		return 60
	default:
		return 100
	}
}

func sortReviewItems(items []reviewItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Priority != items[j].Priority {
			return items[i].Priority < items[j].Priority
		}
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		leftTitle := strings.ToLower(items[i].Title)
		rightTitle := strings.ToLower(items[j].Title)
		if leftTitle != rightTitle {
			return leftTitle < rightTitle
		}
		if items[i].Detail != items[j].Detail {
			return items[i].Detail < items[j].Detail
		}
		return items[i].ID < items[j].ID
	})
}

func sortReviewAffected(affected []reviewAffectedNote) {
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
		if affected[i].Location != affected[j].Location {
			return affected[i].Location < affected[j].Location
		}
		return affected[i].Path < affected[j].Path
	})
}

func reviewCountSummary(count int, singular, plural string) string {
	if count == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", count, plural)
}
