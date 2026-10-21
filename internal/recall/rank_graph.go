package recall

import (
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/brainnotes/internal/query"
	"github.com/sglavoie/brainnotes/internal/textnorm"
)

func buildGraphIndex(graph *query.Graph, documents []Document) graphIndex {
	index := graphIndex{
		documentKeys: make(map[string]string, len(documents)),
		forward:      make(map[string][]string),
		neighbors:    make(map[string][]string),
		nodes:        make(map[string]graphNode),
	}
	if graph == nil {
		return index
	}

	graphKeys := make([]string, 0, len(graph.PathsByTitle))
	for key := range graph.PathsByTitle {
		graphKeys = append(graphKeys, key)
	}
	sort.Strings(graphKeys)
	keysByPath := make(map[string]string, len(graphKeys))
	for _, key := range graphKeys {
		graphPath := graph.PathsByTitle[key]
		relativePath, ok := graphRelativePath(graph.Root, graphPath)
		if !ok {
			continue
		}
		index.nodes[key] = graphNode{
			path:  relativePath,
			title: graph.OriginalTitles[key],
		}
		if _, exists := keysByPath[relativePath]; !exists {
			keysByPath[relativePath] = key
		}
	}
	for _, document := range documents {
		key := keysByPath[document.Path]
		if key == "" {
			fallback := graphDocumentKey(document.Path)
			if _, ok := graph.Titles[fallback]; ok {
				key = fallback
			}
		}
		if key == "" {
			continue
		}
		index.documentKeys[document.Path] = key
		index.nodes[key] = graphNode{path: document.Path, title: document.Title}
	}

	neighborSets := make(map[string]map[string]struct{})
	forwardSets := make(map[string]map[string]struct{})
	for source, targets := range graph.Forward {
		for _, target := range targets {
			if source == "" || target == "" || source == target {
				continue
			}
			if neighborSets[source] == nil {
				neighborSets[source] = make(map[string]struct{})
			}
			if neighborSets[target] == nil {
				neighborSets[target] = make(map[string]struct{})
			}
			if forwardSets[source] == nil {
				forwardSets[source] = make(map[string]struct{})
			}
			forwardSets[source][target] = struct{}{}
			neighborSets[source][target] = struct{}{}
			neighborSets[target][source] = struct{}{}
		}
	}
	for key, targets := range forwardSets {
		values := make([]string, 0, len(targets))
		for target := range targets {
			values = append(values, target)
		}
		sort.Strings(values)
		index.forward[key] = values
	}
	for key, neighbors := range neighborSets {
		values := make([]string, 0, len(neighbors))
		for neighbor := range neighbors {
			values = append(values, neighbor)
		}
		sort.Strings(values)
		index.neighbors[key] = values
	}
	return index
}

func graphRelativePath(root, graphPath string) (string, bool) {
	if graphPath == "" {
		return "", false
	}
	if root == "" {
		if filepath.IsAbs(graphPath) {
			return "", false
		}
		return filepath.ToSlash(filepath.Clean(graphPath)), true
	}
	relative, err := filepath.Rel(root, graphPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", false
	}
	return textnorm.NFC(filepath.ToSlash(relative)), true
}

func graphDocumentKey(documentPath string) string {
	documentPath = strings.TrimSpace(strings.ReplaceAll(documentPath, "\\", "/"))
	for _, part := range strings.Split(documentPath, "/") {
		if part == "." || part == ".." {
			return ""
		}
	}
	documentPath = strings.TrimPrefix(path.Clean("/"+documentPath), "/")
	extension := path.Ext(documentPath)
	if strings.EqualFold(extension, ".md") {
		documentPath = strings.TrimSuffix(documentPath, extension)
	}
	return strings.ToLower(textnorm.NFC(documentPath))
}

func (index graphIndex) compare(sourcePath, candidatePath string) graphMatch {
	sourceKey := index.documentKeys[sourcePath]
	candidateKey := index.documentKeys[candidatePath]
	if sourceKey == "" || candidateKey == "" {
		return graphMatch{}
	}
	if sortedStringsContain(index.neighbors[sourceKey], candidateKey) {
		return graphMatch{score: 1, relationship: RelationshipDirect}
	}

	sharedKeys := intersectStrings(index.neighbors[sourceKey], index.neighbors[candidateKey])
	if len(sharedKeys) == 0 {
		return graphMatch{}
	}
	twoHopKeys := make([]string, 0, len(sharedKeys))
	for _, bridgeKey := range sharedKeys {
		if (sortedStringsContain(index.forward[sourceKey], bridgeKey) &&
			sortedStringsContain(index.forward[bridgeKey], candidateKey)) ||
			(sortedStringsContain(index.forward[candidateKey], bridgeKey) &&
				sortedStringsContain(index.forward[bridgeKey], sourceKey)) {
			twoHopKeys = append(twoHopKeys, bridgeKey)
		}
	}
	relationship := RelationshipSharedNeighbor
	bridgeKeys := sharedKeys
	if len(twoHopKeys) > 0 {
		relationship = RelationshipTwoHop
		bridgeKeys = twoHopKeys
	}
	sort.SliceStable(bridgeKeys, func(i, j int) bool {
		left := index.nodes[bridgeKeys[i]].path
		right := index.nodes[bridgeKeys[j]].path
		if left != right {
			return left < right
		}
		return bridgeKeys[i] < bridgeKeys[j]
	})
	overlap := float64(len(sharedKeys))
	return graphMatch{
		score:        0.6 + 0.4*(overlap-1)/overlap,
		relationship: relationship,
		bridge:       index.nodes[bridgeKeys[0]],
		shared:       len(sharedKeys),
	}
}

func sortedStringsContain(values []string, wanted string) bool {
	index := sort.SearchStrings(values, wanted)
	return index < len(values) && values[index] == wanted
}

func intersectStrings(left, right []string) []string {
	values := make([]string, 0, min(len(left), len(right)))
	for leftIndex, rightIndex := 0, 0; leftIndex < len(left) && rightIndex < len(right); {
		switch {
		case left[leftIndex] < right[rightIndex]:
			leftIndex++
		case left[leftIndex] > right[rightIndex]:
			rightIndex++
		default:
			values = append(values, left[leftIndex])
			leftIndex++
			rightIndex++
		}
	}
	return values
}
