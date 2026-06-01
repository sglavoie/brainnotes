package web

import (
	"fmt"
	"sort"
	"strings"

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
	ID        string `json:"id"`
	Title     string `json:"title"`
	Folder    string `json:"folder"`
	InDegree  int    `json:"inDegree"`
	OutDegree int    `json:"outDegree"`
	Degree    int    `json:"degree"`
}

type graphJSONEdge struct {
	Data graphJSONEdgeData `json:"data"`
}

type graphJSONEdgeData struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
}

func buildGraphResponse(snapshot vaultSnapshot, root, queryText string) (graphJSON, error) {
	if snapshot.graph == nil {
		return graphJSON{}, errorsf("nil graph")
	}
	if strings.TrimSpace(root) == "" {
		return graphJSON{}, errorsf("empty vault root")
	}
	queryFolded := foldSearchQuery(queryText)

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

	included := make(map[string]struct{}, len(snapshot.graph.Titles))
	if queryFolded == "" {
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
			_, matched, err := matchNoteText(note, queryFolded)
			if err != nil {
				return graphJSON{}, err
			}
			if matched {
				included[key] = struct{}{}
			}
		}
	}

	keys := make([]string, 0, len(included))
	for key := range included {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	nodes := make([]graphJSONNode, 0, len(keys))
	for _, key := range keys {
		title := snapshot.graph.OriginalTitles[key]
		if title == "" {
			title = key
		}
		folder := ""
		if note := noteByKey[key]; note != nil {
			var err error
			folder, err = note.Folder(root)
			if err != nil {
				return graphJSON{}, fmt.Errorf("folder for %q: %w", key, err)
			}
		}
		degree := degrees[key]
		nodes = append(nodes, graphJSONNode{
			Data: graphJSONNodeData{
				ID:        key,
				Title:     title,
				Folder:    folder,
				InDegree:  degree.inDegree,
				OutDegree: degree.outDegree,
				Degree:    degree.inDegree + degree.outDegree,
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

		targets := append([]string(nil), snapshot.graph.Forward[source]...)
		sort.Strings(targets)
		for _, target := range targets {
			if source == target {
				continue
			}
			if _, ok := included[target]; !ok {
				continue
			}

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
