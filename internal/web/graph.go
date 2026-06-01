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
	ID     string `json:"id"`
	Title  string `json:"title"`
	Folder string `json:"folder"`
}

type graphJSONEdge struct {
	Data graphJSONEdgeData `json:"data"`
}

type graphJSONEdgeData struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
}

func buildGraphResponse(snapshot vaultSnapshot, root string) (graphJSON, error) {
	if snapshot.graph == nil {
		return graphJSON{}, errorsf("nil graph")
	}
	if strings.TrimSpace(root) == "" {
		return graphJSON{}, errorsf("empty vault root")
	}

	noteByKey := make(map[string]*vault.Note, len(snapshot.notes))
	for _, note := range snapshot.notes {
		key := snapshot.graph.KeyForNote(note)
		if key == "" {
			continue
		}
		noteByKey[key] = note
	}

	keys := make([]string, 0, len(snapshot.graph.Titles))
	for key := range snapshot.graph.Titles {
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
		nodes = append(nodes, graphJSONNode{
			Data: graphJSONNodeData{
				ID:     key,
				Title:  title,
				Folder: folder,
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
		if _, ok := snapshot.graph.Titles[source]; !ok {
			continue
		}

		targets := append([]string(nil), snapshot.graph.Forward[source]...)
		sort.Strings(targets)
		for _, target := range targets {
			if source == target {
				continue
			}
			if _, ok := snapshot.graph.Titles[target]; !ok {
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
