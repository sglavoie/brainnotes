package web

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

type graphJSON struct {
	Nodes []graphJSONNode `json:"nodes"`
	Edges []graphJSONEdge `json:"edges"`
}

type graphJSONNode struct {
	Data graphJSONNodeData `json:"data"`
}

type graphJSONNodeData struct {
	ID                string `json:"id"`
	Title             string `json:"title"`
	Folder            string `json:"folder"`
	InDegree          int    `json:"inDegree"`
	OutDegree         int    `json:"outDegree"`
	Degree            int    `json:"degree"`
	FilteredInDegree  int    `json:"filteredInDegree"`
	FilteredOutDegree int    `json:"filteredOutDegree"`
	FilteredDegree    int    `json:"filteredDegree"`
	HasBroken         bool   `json:"hasBroken"`
	BrokenCount       int    `json:"brokenCount"`
	IsOrphan          bool   `json:"isOrphan"`
	IsDeadEnd         bool   `json:"isDeadEnd"`
	MatchesQuery      bool   `json:"matchesQuery"`
}

type graphJSONEdge struct {
	Data graphJSONEdgeData `json:"data"`
}

type graphJSONEdgeData struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
}

// buildGraphResponse renders the graph payload for the current filters. When
// focusQuery is true the free-text query stops removing nodes: the included set
// (and therefore node sizes and edges) is derived from the non-text filters
// only, and the query is reported per node through MatchesQuery so the client
// can dim non-matches in place without a relayout.
func buildGraphResponse(snapshot vaultSnapshot, root string, filters noteFilters, focusQuery bool) (graphJSON, error) {
	if snapshot.graph == nil {
		return graphJSON{}, errorsf("nil graph")
	}
	if strings.TrimSpace(root) == "" {
		return graphJSON{}, errorsf("empty vault root")
	}

	noteByKey := make(map[string]*vault.Note, len(snapshot.notes))
	for _, note := range snapshot.notes {
		if note == nil {
			continue
		}
		key := snapshot.graph.KeyForNote(note)
		if key == "" {
			continue
		}
		noteByKey[key] = note
	}
	degrees := graphNodeDegrees(snapshot)

	inclusionFilters := filters
	if focusQuery {
		inclusionFilters = filters.withoutQuery()
	}

	included := make(map[string]struct{}, len(snapshot.graph.Titles))
	if inclusionFilters.foldedQuery == "" && !inclusionFilters.hasNonTextFilters() {
		for key := range snapshot.graph.Titles {
			included[key] = struct{}{}
		}
	} else {
		for _, note := range snapshot.notes {
			if note == nil {
				continue
			}
			key := snapshot.graph.KeyForNote(note)
			if key == "" {
				continue
			}
			_, ok, err := matchNoteWithFilters(note, inclusionFilters)
			if err != nil {
				return graphJSON{}, err
			}
			if ok {
				included[key] = struct{}{}
			}
		}
	}

	matched := make(map[string]struct{}, len(included))
	if filters.foldedQuery != "" {
		if !focusQuery {
			// Filter mode already required the text match for inclusion.
			for key := range included {
				matched[key] = struct{}{}
			}
		} else {
			for _, note := range snapshot.notes {
				if note == nil {
					continue
				}
				key := snapshot.graph.KeyForNote(note)
				if key == "" {
					continue
				}
				if _, ok := included[key]; !ok {
					continue
				}
				_, ok, err := matchNoteWithFilters(note, filters)
				if err != nil {
					return graphJSON{}, err
				}
				if ok {
					matched[key] = struct{}{}
				}
			}
		}
	}
	filteredDegrees := graphFilteredNodeDegrees(snapshot, included)

	keys := make([]string, 0, len(included))
	for key := range included {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	orphan := query.Orphan(snapshot.graph)
	deadEnd := query.DeadEnd(snapshot.graph)
	nodes := make([]graphJSONNode, 0, len(keys))
	for _, key := range keys {
		title := snapshot.graph.OriginalTitles[key]
		if title == "" {
			title = key
		}
		folder := ""
		var hasBroken, isOrphan, isDeadEnd bool
		var brokenCount int
		if note := noteByKey[key]; note != nil {
			var err error
			folder, err = note.Folder(root)
			if err != nil {
				return graphJSON{}, fmt.Errorf("folder for %q: %w", key, err)
			}
			brokenCount = len(query.UnresolvedLinks(note, snapshot.graph))
			hasBroken = brokenCount > 0
			isOrphan = orphan(note)
			isDeadEnd = deadEnd(note)
		}
		degree := degrees[key]
		filteredDegree := filteredDegrees[key]
		_, matchesQuery := matched[key]
		nodes = append(nodes, graphJSONNode{
			Data: graphJSONNodeData{
				ID:                key,
				Title:             title,
				Folder:            folder,
				InDegree:          degree.inDegree,
				OutDegree:         degree.outDegree,
				Degree:            degree.inDegree + degree.outDegree,
				FilteredInDegree:  filteredDegree.inDegree,
				FilteredOutDegree: filteredDegree.outDegree,
				FilteredDegree:    filteredDegree.inDegree + filteredDegree.outDegree,
				HasBroken:         hasBroken,
				BrokenCount:       brokenCount,
				IsOrphan:          isOrphan,
				IsDeadEnd:         isDeadEnd,
				MatchesQuery:      matchesQuery,
			},
		})
	}

	sources := make([]string, 0, len(snapshot.graph.Forward))
	for source := range snapshot.graph.Forward {
		sources = append(sources, source)
	}
	sort.Strings(sources)

	edges := make([]graphJSONEdge, 0, len(sources))
	seen := make(map[string]struct{})
	for _, source := range sources {
		if _, ok := included[source]; !ok {
			continue
		}

		for _, target := range visibleGraphTargets(source, snapshot.graph.Forward[source], included) {
			edgeID := source + "|" + target
			if _, exists := seen[edgeID]; exists {
				continue
			}
			seen[edgeID] = struct{}{}

			edges = append(edges, graphJSONEdge{
				Data: graphJSONEdgeData{
					ID:     edgeID,
					Source: source,
					Target: target,
				},
			})
		}
	}

	return graphJSON{
		Nodes: nodes,
		Edges: edges,
	}, nil
}

type graphNodeDegree struct {
	inDegree  int
	outDegree int
}

func graphNodeDegrees(snapshot vaultSnapshot) map[string]graphNodeDegree {
	degrees := make(map[string]graphNodeDegree, len(snapshot.graph.Titles))
	for key := range snapshot.graph.Titles {
		degrees[key] = graphNodeDegree{}
	}

	for source, targets := range snapshot.graph.Forward {
		if _, ok := snapshot.graph.Titles[source]; !ok {
			continue
		}
		outDegree := uniqueLinkedTargetCount(source, targets, snapshot.graph.Titles)
		degree := degrees[source]
		degree.outDegree = outDegree
		degrees[source] = degree
	}

	for target, referrers := range snapshot.graph.Backward {
		if _, ok := snapshot.graph.Titles[target]; !ok {
			continue
		}
		degree := degrees[target]
		degree.inDegree = uniqueStringCount(referrers)
		degrees[target] = degree
	}

	return degrees
}

func graphFilteredNodeDegrees(snapshot vaultSnapshot, included map[string]struct{}) map[string]graphNodeDegree {
	degrees := make(map[string]graphNodeDegree, len(included))
	for key := range included {
		degrees[key] = graphNodeDegree{}
	}

	for source, targets := range snapshot.graph.Forward {
		if _, ok := included[source]; !ok {
			continue
		}
		for _, target := range visibleGraphTargets(source, targets, included) {
			sourceDegree := degrees[source]
			sourceDegree.outDegree++
			degrees[source] = sourceDegree

			targetDegree := degrees[target]
			targetDegree.inDegree++
			degrees[target] = targetDegree
		}
	}

	return degrees
}

func visibleGraphTargets(source string, targets []string, included map[string]struct{}) []string {
	seen := make(map[string]struct{}, len(targets))
	visible := make([]string, 0, len(targets))
	for _, target := range targets {
		if target == "" || target == source {
			continue
		}
		if _, ok := included[target]; !ok {
			continue
		}
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		visible = append(visible, target)
	}
	sort.Strings(visible)
	return visible
}

func uniqueLinkedTargetCount(source string, targets []string, noteKeys map[string]struct{}) int {
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		if target == "" || target == source {
			continue
		}
		if _, ok := noteKeys[target]; !ok {
			continue
		}
		seen[target] = struct{}{}
	}
	return len(seen)
}

func uniqueStringCount(values []string) int {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		seen[value] = struct{}{}
	}
	return len(seen)
}
