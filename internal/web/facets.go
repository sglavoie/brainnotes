package web

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
)

type facetsResponse struct {
	Tags       []facetOption      `json:"tags"`
	Categories []facetOption      `json:"categories"`
	Status     []facetOption      `json:"status"`
	Types      []facetOption      `json:"types"`
	Folders    []facetOption      `json:"folders"`
	Properties []facetOption      `json:"properties"`
	Issues     []facetIssueOption `json:"issues"`
}

type facetOption struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

type facetIssueOption struct {
	Param string `json:"param"`
	Value string `json:"value"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

type facetCounter struct {
	entries         map[string]*facetOption
	caseInsensitive bool
}

func newFacetCounter(caseInsensitive bool) facetCounter {
	return facetCounter{
		entries:         make(map[string]*facetOption),
		caseInsensitive: caseInsensitive,
	}
}

func (c facetCounter) addValuesForNote(values []string) {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		key := value
		if c.caseInsensitive {
			key = strings.ToLower(value)
		}
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		entry := c.entries[key]
		if entry == nil {
			c.entries[key] = &facetOption{Value: value, Count: 1}
			continue
		}
		entry.Count++
		if facetDisplayLess(value, entry.Value) {
			entry.Value = value
		}
	}
}

func (c facetCounter) options() []facetOption {
	options := make([]facetOption, 0, len(c.entries))
	for _, entry := range c.entries {
		options = append(options, *entry)
	}
	sortFacetOptions(options)
	return options
}

func buildFacetsResponse(snapshot vaultSnapshot, root string) (facetsResponse, error) {
	if snapshot.graph == nil {
		return facetsResponse{}, errorsf("nil graph")
	}
	if strings.TrimSpace(root) == "" {
		return facetsResponse{}, errorsf("empty vault root")
	}

	tags := newFacetCounter(true)
	categories := newFacetCounter(true)
	status := newFacetCounter(true)
	types := newFacetCounter(true)
	folders := newFacetCounter(false)
	properties := newFacetCounter(false)

	orphan := query.Orphan(snapshot.graph)
	deadEnd := query.DeadEnd(snapshot.graph)
	var orphanCount, brokenCount, deadEndCount int

	for _, note := range snapshot.notes {
		if note == nil {
			continue
		}
		if snapshot.graph.KeyForNote(note) == "" {
			continue
		}

		folder, err := note.Folder(root)
		if err != nil {
			return facetsResponse{}, fmt.Errorf("folder for %q: %w", note.Path, err)
		}
		folders.addValuesForNote(folderFacetValues(folder))

		if orphan(note) {
			orphanCount++
		}
		if len(query.UnresolvedLinks(note, snapshot.graph)) > 0 {
			brokenCount++
		}
		if deadEnd(note) {
			deadEndCount++
		}

		fm, err := note.Front()
		if err != nil {
			continue
		}
		properties.addValuesForNote(fm.Keys())

		if raw, ok := fm.Get("tags"); ok {
			tags.addValuesForNote(normalizeLinkedFacetValues(flattenFacetValues(raw)))
		}
		if raw, ok := fm.Get("categories"); ok {
			categories.addValuesForNote(linkedFacetValues(raw))
		}
		if raw, ok := fm.Get("status"); ok {
			status.addValuesForNote(flattenFacetValues(raw))
		}

		var typeValues []string
		if raw, ok := fm.Get("type"); ok {
			typeValues = append(typeValues, flattenFacetValues(raw)...)
		}
		if raw, ok := fm.Get("kind"); ok {
			typeValues = append(typeValues, flattenFacetValues(raw)...)
		}
		types.addValuesForNote(typeValues)
	}

	issues := []facetIssueOption{
		{Param: "broken", Value: "1", Label: "Broken links", Count: brokenCount},
		{Param: "dead_end", Value: "1", Label: "Dead ends", Count: deadEndCount},
		{Param: "orphan", Value: "1", Label: "Orphans", Count: orphanCount},
	}
	sortIssueOptions(issues)

	return facetsResponse{
		Tags:       tags.options(),
		Categories: categories.options(),
		Status:     status.options(),
		Types:      types.options(),
		Folders:    folders.options(),
		Properties: properties.options(),
		Issues:     issues,
	}, nil
}

func flattenFacetValues(value any) []string {
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch vv := v.(type) {
		case nil:
			return
		case string:
			out = append(out, vv)
		case []any:
			for _, item := range vv {
				walk(item)
			}
		case []string:
			out = append(out, vv...)
		case map[string]any:
			return
		case time.Time:
			out = append(out, frontmatter.FormatTimestamp(vv))
		case bool:
			out = append(out, strconv.FormatBool(vv))
		case int:
			out = append(out, strconv.Itoa(vv))
		case int64:
			out = append(out, strconv.FormatInt(vv, 10))
		case float64:
			out = append(out, formatFacetFloat(vv))
		default:
			out = append(out, fmt.Sprintf("%v", vv))
		}
	}
	walk(value)
	return out
}

func linkedFacetValues(value any) []string {
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch vv := v.(type) {
		case string:
			if value := normalizeLinkedFacetValue(vv); value != "" {
				out = append(out, value)
			}
		case []any:
			for _, item := range vv {
				walk(item)
			}
		case []string:
			for _, item := range vv {
				walk(item)
			}
		}
	}
	walk(value)
	return out
}

func normalizeLinkedFacetValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	links := wikilink.Parse(value)
	if len(links) == 1 && links[0].Start == 0 && links[0].End == len(value) {
		return stripMarkdownExt(strings.TrimSpace(links[0].Title()))
	}
	return value
}

func normalizeLinkedFacetValues(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if normalized := normalizeLinkedFacetValue(value); normalized != "" {
			out = append(out, normalized)
		}
	}
	return out
}

func folderFacetValues(folder string) []string {
	folder = normalizeFolderFacetValue(folder)
	if folder == "" {
		return nil
	}

	parts := strings.Split(folder, "/")
	values := make([]string, 0, len(parts))
	for i := range parts {
		values = append(values, strings.Join(parts[:i+1], "/"))
	}
	return values
}

func normalizeFolderFacetValue(folder string) string {
	folder = strings.TrimSpace(strings.ReplaceAll(folder, "\\", "/"))
	folder = strings.Trim(folder, "/")
	folder = path.Clean(folder)
	if folder == "." || folder == ".." || strings.HasPrefix(folder, "../") {
		return ""
	}
	return folder
}

func stripMarkdownExt(value string) string {
	ext := path.Ext(value)
	if !strings.EqualFold(ext, ".md") {
		return value
	}
	return strings.TrimSuffix(value, ext)
}

func formatFacetFloat(value float64) string {
	formatted := strconv.FormatFloat(value, 'g', -1, 64)
	if !strings.ContainsAny(formatted, ".eE") {
		formatted += ".0"
	}
	return formatted
}

func sortFacetOptions(options []facetOption) {
	sort.Slice(options, func(i, j int) bool {
		if options[i].Count != options[j].Count {
			return options[i].Count > options[j].Count
		}
		return facetDisplayLess(options[i].Value, options[j].Value)
	})
}

func sortIssueOptions(options []facetIssueOption) {
	sort.Slice(options, func(i, j int) bool {
		if options[i].Count != options[j].Count {
			return options[i].Count > options[j].Count
		}
		return facetDisplayLess(options[i].Label, options[j].Label)
	})
}

func facetDisplayLess(left, right string) bool {
	leftFolded := strings.ToLower(left)
	rightFolded := strings.ToLower(right)
	if leftFolded != rightFolded {
		return leftFolded < rightFolded
	}
	return left < right
}
