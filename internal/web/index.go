package web

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
)

const (
	indexRecentLimit  = 12
	indexTimeLayout   = "2006-01-02 15:04"
	indexDefaultTitle = "bn serve"
)

type indexPageData struct {
	Title             string
	CSRFToken         string
	NoteCount         int
	BrokenTargetCount int
	OrphanCount       int
	GraphPath         string
	BrokenPath        string
	FacetControls     facetControlsData
	Review            indexReviewPanelData
	Diagnostics       diagnosticsSummary
	Recent            []indexRecentNote
}

type indexReviewPanelData struct {
	Items       []indexReviewItem
	Counts      reviewCounts
	Warnings    []indexReviewWarning
	HasItems    bool
	HasWarnings bool
}

type indexReviewWarning struct {
	Code    string
	Message string
}

type indexReviewItem struct {
	ID        string
	Signature string
	Kind      string
	KindLabel string
	Title     string
	Summary   string
	Detail    string
	Count     int
	Affected  []reviewAffectedNote
}

type indexRecentNote struct {
	Title   string
	Path    string
	Folder  string
	Updated string
}

type indexRecentCandidate struct {
	view    indexRecentNote
	modTime time.Time
}

func buildIndexPageData(snapshot vaultSnapshot, root string) (indexPageData, error) {
	if snapshot.graph == nil {
		return indexPageData{}, errorsf("nil graph")
	}
	if strings.TrimSpace(root) == "" {
		return indexPageData{}, errorsf("empty vault root")
	}

	orphan := query.Orphan(snapshot.graph)
	brokenTargets := map[string]struct{}{}
	recent := make([]indexRecentCandidate, 0, len(snapshot.notes))

	noteCount := 0
	orphanCount := 0
	for _, note := range snapshot.notes {
		if note == nil {
			continue
		}
		key := snapshot.graph.KeyForNote(note)
		if key == "" {
			continue
		}

		noteCount++
		if orphan(note) {
			orphanCount++
		}

		issues := query.UnresolvedLinks(note, snapshot.graph)
		for _, issue := range issues {
			target := snapshot.graph.UnresolvedTargetKey(issue.Link.Target)
			if target == "" {
				target = strings.ToLower(strings.TrimSpace(issue.Link.Target))
			}
			if target == "" {
				continue
			}
			brokenTargets[target] = struct{}{}
		}

		path, err := notePathForKey(key)
		if err != nil {
			return indexPageData{}, fmt.Errorf("route path for %q: %w", key, err)
		}
		folder, err := note.Folder(root)
		if err != nil {
			return indexPageData{}, fmt.Errorf("folder for %q: %w", key, err)
		}
		info, err := os.Stat(note.Path)
		if err != nil {
			return indexPageData{}, fmt.Errorf("stat %q: %w", note.Path, err)
		}

		title := note.Title()
		if title == "" {
			title = key
		}
		recent = append(recent, indexRecentCandidate{
			view: indexRecentNote{
				Title:   title,
				Path:    path,
				Folder:  folder,
				Updated: info.ModTime().Local().Format(indexTimeLayout),
			},
			modTime: info.ModTime(),
		})
	}

	sort.SliceStable(recent, func(i, j int) bool {
		if !recent[i].modTime.Equal(recent[j].modTime) {
			return recent[i].modTime.After(recent[j].modTime)
		}
		leftTitle := strings.ToLower(recent[i].view.Title)
		rightTitle := strings.ToLower(recent[j].view.Title)
		if leftTitle != rightTitle {
			return leftTitle < rightTitle
		}
		return recent[i].view.Path < recent[j].view.Path
	})
	if len(recent) > indexRecentLimit {
		recent = recent[:indexRecentLimit]
	}

	recentViews := make([]indexRecentNote, 0, len(recent))
	for _, item := range recent {
		recentViews = append(recentViews, item.view)
	}

	return indexPageData{
		Title:             indexDefaultTitle,
		NoteCount:         noteCount,
		BrokenTargetCount: len(brokenTargets),
		OrphanCount:       orphanCount,
		GraphPath:         "/graph",
		BrokenPath:        "/broken",
		FacetControls:     overviewFacetControlsData(),
		Recent:            recentViews,
	}, nil
}

func buildIndexReviewPanelData(data diagnosticsData, cfg *config.Config, root string, store *reviewStateStore) indexReviewPanelData {
	queue := buildReviewQueueFromData(data, cfg, root)
	warnings := []indexReviewWarning(nil)
	if store != nil {
		filtered, err := store.filterQueue(queue)
		if err == nil {
			queue = filtered
		} else {
			warnings = append(warnings, indexReviewWarning{
				Code:    reviewStateWarningCode,
				Message: "Review state could not be loaded; showing all generated review items.",
			})
		}
	}

	items := make([]indexReviewItem, 0, len(queue.Items))
	for _, item := range queue.Items {
		items = append(items, indexReviewItem{
			ID:        item.ID,
			Signature: item.Signature,
			Kind:      string(item.Kind),
			KindLabel: reviewKindLabel(item.Kind),
			Title:     item.Title,
			Summary:   item.Summary,
			Detail:    item.Detail,
			Count:     item.Count,
			Affected:  item.Affected,
		})
	}

	return indexReviewPanelData{
		Items:       items,
		Counts:      queue.Counts,
		Warnings:    warnings,
		HasItems:    len(items) > 0,
		HasWarnings: len(warnings) > 0,
	}
}

func reviewKindLabel(kind reviewItemKind) string {
	switch kind {
	case reviewKindFrontmatterError:
		return "Frontmatter"
	case reviewKindSchemaViolation:
		return "Schema"
	case reviewKindBrokenTarget:
		return "Broken target"
	case reviewKindDuplicateTitle:
		return "Duplicate title"
	case reviewKindOrphanNote:
		return "Orphan"
	case reviewKindDeadEndNote:
		return "Dead end"
	default:
		return "Review"
	}
}
