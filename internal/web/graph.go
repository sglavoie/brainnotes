package web

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sglavoie/brainnotes/internal/query"
	"github.com/sglavoie/brainnotes/internal/vault"
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

	noteByKey := graphNotesByKey(snapshot)
	included, err := graphIncludedKeys(snapshot, filters, focusQuery)
	if err != nil {
		return graphJSON{}, err
	}
	matched, err := graphMatchedKeys(snapshot, filters, focusQuery, included)
	if err != nil {
		return graphJSON{}, err
	}
	degrees := graphNodeDegrees(snapshot)
	filteredDegrees := graphFilteredNodeDegrees(snapshot, included)

	keys := make([]string, 0, len(included))
	for key := range included {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	nodeContext := graphResponseContext{
		snapshot: snapshot, root: root, notes: noteByKey,
		degrees: degrees, filteredDegrees: filteredDegrees, matched: matched,
		orphan: query.Orphan(snapshot.graph), deadEnd: query.DeadEnd(snapshot.graph),
	}
	nodes, err := nodeContext.nodes(keys)
	if err != nil {
		return graphJSON{}, err
	}
	edges := graphResponseEdges(snapshot, included)

	return graphJSON{
		Nodes: nodes,
		Edges: edges,
	}, nil
}

func graphNotesByKey(snapshot vaultSnapshot) map[string]*vault.Note {
	notes := make(map[string]*vault.Note, len(snapshot.notes))
	for _, note := range snapshot.notes {
		if note == nil {
			continue
		}
		if key := snapshot.graph.KeyForNote(note); key != "" {
			notes[key] = note
		}
	}
	return notes
}

func graphIncludedKeys(snapshot vaultSnapshot, filters noteFilters, focusQuery bool) (map[string]struct{}, error) {
	if focusQuery {
		filters = filters.withoutQuery()
	}
	included := make(map[string]struct{}, len(snapshot.graph.Titles))
	if filters.foldedQuery == "" && !filters.hasNonTextFilters() {
		for key := range snapshot.graph.Titles {
			included[key] = struct{}{}
		}
		return included, nil
	}
	for _, note := range snapshot.notes {
		if note == nil {
			continue
		}
		key := snapshot.graph.KeyForNote(note)
		if key == "" {
			continue
		}
		_, ok, err := matchNoteWithFilters(note, filters)
		if err != nil {
			return nil, err
		}
		if ok {
			included[key] = struct{}{}
		}
	}
	return included, nil
}

func graphMatchedKeys(snapshot vaultSnapshot, filters noteFilters, focusQuery bool, included map[string]struct{}) (map[string]struct{}, error) {
	matched := make(map[string]struct{}, len(included))
	if filters.foldedQuery == "" {
		return matched, nil
	}
	if !focusQuery {
		// Filter mode already required the text match for inclusion.
		for key := range included {
			matched[key] = struct{}{}
		}
		return matched, nil
	}
	for _, note := range snapshot.notes {
		if note == nil {
			continue
		}
		key := snapshot.graph.KeyForNote(note)
		if _, ok := included[key]; !ok || key == "" {
			continue
		}
		_, ok, err := matchNoteWithFilters(note, filters)
		if err != nil {
			return nil, err
		}
		if ok {
			matched[key] = struct{}{}
		}
	}
	return matched, nil
}

type graphResponseContext struct {
	snapshot        vaultSnapshot
	root            string
	notes           map[string]*vault.Note
	degrees         map[string]graphNodeDegree
	filteredDegrees map[string]graphNodeDegree
	matched         map[string]struct{}
	orphan          query.Predicate
	deadEnd         query.Predicate
}

func (ctx graphResponseContext) nodes(keys []string) ([]graphJSONNode, error) {
	nodes := make([]graphJSONNode, 0, len(keys))
	for _, key := range keys {
		data, err := ctx.node(key)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, graphJSONNode{Data: data})
	}
	return nodes, nil
}

func (ctx graphResponseContext) node(key string) (graphJSONNodeData, error) {
	title := ctx.snapshot.graph.OriginalTitles[key]
	if title == "" {
		title = key
	}
	degree := ctx.degrees[key]
	filteredDegree := ctx.filteredDegrees[key]
	data := graphJSONNodeData{
		ID:                key,
		Title:             title,
		InDegree:          degree.inDegree,
		OutDegree:         degree.outDegree,
		Degree:            degree.inDegree + degree.outDegree,
		FilteredInDegree:  filteredDegree.inDegree,
		FilteredOutDegree: filteredDegree.outDegree,
		FilteredDegree:    filteredDegree.inDegree + filteredDegree.outDegree,
	}
	_, data.MatchesQuery = ctx.matched[key]
	note := ctx.notes[key]
	if note == nil {
		return data, nil
	}
	folder, err := note.Folder(ctx.root)
	if err != nil {
		return graphJSONNodeData{}, fmt.Errorf("folder for %q: %w", key, err)
	}
	data.Folder = folder
	data.BrokenCount = len(query.UnresolvedLinks(note, ctx.snapshot.graph))
	data.HasBroken = data.BrokenCount > 0
	data.IsOrphan = ctx.orphan(note)
	data.IsDeadEnd = ctx.deadEnd(note)
	return data, nil
}

func graphResponseEdges(snapshot vaultSnapshot, included map[string]struct{}) []graphJSONEdge {
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
			edges = append(edges, graphJSONEdge{Data: graphJSONEdgeData{
				ID: edgeID, Source: source, Target: target,
			}})
		}
	}
	return edges
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
