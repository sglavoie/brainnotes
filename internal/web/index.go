package web

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
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
	Fixes             indexFixesPanelData
	Diagnostics       diagnosticsSummary
	Recent            []indexRecentNote
}

// indexFixesPanelData is the server-rendered shell for the homepage Daily Fixes
// panel. It seeds the counts and any queue warnings; the task list itself is
// hydrated client-side from GET /api/cockpit so the interactive fix affordances
// (preview/apply) always run against the live queue.
type indexFixesPanelData struct {
	Counts      cockpit.Counts
	Warnings    []indexFixesWarning
	HasTasks    bool
	HasWarnings bool
}

type indexFixesWarning struct {
	Message string
}

// buildIndexFixesPanelData maps a filtered cockpit queue onto the homepage
// panel shell.
func buildIndexFixesPanelData(queue cockpit.Queue) indexFixesPanelData {
	warnings := make([]indexFixesWarning, 0, len(queue.Warnings))
	for _, w := range queue.Warnings {
		warnings = append(warnings, indexFixesWarning{Message: w.Message})
	}
	return indexFixesPanelData{
		Counts:      queue.Counts,
		Warnings:    warnings,
		HasTasks:    len(queue.Tasks) > 0,
		HasWarnings: len(warnings) > 0,
	}
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
