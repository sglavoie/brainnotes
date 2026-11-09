package web

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/brainnotes/internal/brief"
	"github.com/sglavoie/brainnotes/internal/query"
	"github.com/sglavoie/brainnotes/internal/recall"
	"github.com/sglavoie/brainnotes/internal/vault"
)

func overviewRecentCandidate(note *vault.Note, key, root string) (indexRecentCandidate, error) {
	path, err := notePathForKey(key)
	if err != nil {
		return indexRecentCandidate{}, fmt.Errorf("route path for %q: %w", key, err)
	}
	folder, err := note.Folder(root)
	if err != nil {
		return indexRecentCandidate{}, fmt.Errorf("folder for %q: %w", key, err)
	}
	info, err := os.Stat(note.Path)
	if err != nil {
		return indexRecentCandidate{}, fmt.Errorf("stat %q: %w", note.Path, err)
	}

	title := note.Title()
	if title == "" {
		title = key
	}
	return indexRecentCandidate{
		note: note,
		view: indexRecentNote{
			Title:      title,
			Path:       path,
			Folder:     folder,
			Updated:    info.ModTime().Local().Format(indexTimeLayout),
			UpdatedISO: info.ModTime().Format(time.RFC3339),
		},
		modTime: info.ModTime(),
	}, nil
}

func overviewRecentViews(recent []indexRecentCandidate, root string) []indexRecentNote {
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
		// Extract only the displayed notes, never the entire vault for a list.
		if document, err := recall.ExtractDocument(root, item.note, recall.ExtractOptions{}); err == nil {
			item.view.Excerpt = brief.RepresentativeExcerpt(document)
		}
		recentViews = append(recentViews, item.view)
	}

	return recentViews
}

func addOverviewBrokenTargets(brokenTargets map[string]struct{}, note *vault.Note, snapshot vaultSnapshot) {
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
}
