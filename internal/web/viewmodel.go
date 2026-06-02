package web

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
)

type noteLinkView struct {
	Title       string
	Path        string
	Count       int
	Occurrences []backlinkOccurrence
}

type breadcrumbItem struct {
	Label   string
	Path    string
	Current bool
}

type tableOfContentsItem struct {
	ID    string
	Level int
	Text  string
	Path  string
}

type noteCopyPayloads struct {
	Wikilink     string
	VaultPath    string
	RoutePath    string
	MarkdownLink string
}

type backlinkOccurrence struct {
	Location       string
	Line           int
	FrontmatterKey string
	Snippet        string
}

type brokenAffordance struct {
	Target   string
	Path     string
	Location string
}

type frontmatterEntry struct {
	Key   string
	Value frontmatterValue
}

type frontmatterValue struct {
	Kind    string
	Text    string
	Anchors []frontmatterAnchor
	Items   []frontmatterValue
	Pairs   []frontmatterEntry
}

type frontmatterAnchor struct {
	Label string
	Path  string
}

type brokenPageEntry struct {
	SourceTitle string
	SourcePath  string
	Target      string
	Location    string
	LinkText    string
}

func noteForRoute(snapshot vaultSnapshot, routeKey string) (*vault.Note, string, bool) {
	if snapshot.graph == nil {
		return nil, "", false
	}
	key := snapshot.graph.UnresolvedTargetKey(routeKey)
	if key == "" {
		key = strings.ToLower(strings.TrimSpace(routeKey))
	}
	if key == "" {
		return nil, "", false
	}

	path, ok := snapshot.graph.PathsByTitle[key]
	if !ok {
		return nil, "", false
	}
	for _, n := range snapshot.notes {
		if n != nil && n.Path == path {
			return n, key, true
		}
	}

	return &vault.Note{Path: path}, key, true
}

func outgoingLinksForNote(graph *query.Graph, note *vault.Note) []noteLinkView {
	if graph == nil || note == nil {
		return nil
	}

	keys := graph.ForwardLinksForNote(note)
	links := make([]noteLinkView, 0, len(keys))
	for _, key := range keys {
		path, err := notePathForKey(key)
		if err != nil {
			continue
		}
		title := graph.OriginalTitles[key]
		if title == "" {
			title = key
		}
		links = append(links, noteLinkView{
			Title: title,
			Path:  path,
		})
	}
	return links
}

func backlinksForNote(snapshot vaultSnapshot, note *vault.Note) []noteLinkView {
	graph := snapshot.graph
	if graph == nil || note == nil {
		return nil
	}

	targetKey := graph.KeyForNote(note)
	if targetKey == "" {
		return nil
	}

	keysByPath := make(map[string]string, len(graph.PathsByTitle))
	for key, path := range graph.PathsByTitle {
		keysByPath[path] = key
	}
	notesByPath := make(map[string]*vault.Note, len(snapshot.notes))
	for _, source := range snapshot.notes {
		if source == nil {
			continue
		}
		notesByPath[source.Path] = source
	}

	paths := graph.BackLinksForNote(note)
	links := make([]noteLinkView, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		key := keysByPath[path]
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		notePath, err := notePathForKey(key)
		if err != nil {
			continue
		}
		title := graph.OriginalTitles[key]
		if title == "" {
			title = key
		}
		sourceNote := notesByPath[path]
		if sourceNote == nil {
			sourceNote = &vault.Note{Path: path}
		}
		occurrences := backlinkOccurrencesForNote(graph, targetKey, sourceNote)
		links = append(links, noteLinkView{
			Title:       title,
			Path:        notePath,
			Count:       len(occurrences),
			Occurrences: occurrences,
		})
	}

	sort.Slice(links, func(i, j int) bool {
		left := strings.ToLower(links[i].Title)
		right := strings.ToLower(links[j].Title)
		if left != right {
			return left < right
		}
		return links[i].Path < links[j].Path
	})
	return links
}

func brokenAffordancesForNote(note *vault.Note, graph *query.Graph) []brokenAffordance {
	if note == nil || graph == nil {
		return nil
	}

	unresolved := query.UnresolvedLinks(note, graph)
	items := make([]brokenAffordance, 0, len(unresolved))
	seen := map[string]struct{}{}
	for _, issue := range unresolved {
		key := graph.UnresolvedTargetKey(issue.Link.Target)
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(issue.Link.Target))
		}
		if key == "" {
			continue
		}
		location := brokenLocation(issue.Line, issue.FrontmatterKey)
		signature := key + "\x00" + location
		if _, ok := seen[signature]; ok {
			continue
		}
		seen[signature] = struct{}{}
		items = append(items, brokenAffordance{
			Target:   key,
			Path:     "/broken?" + url.Values{"q": []string{key}}.Encode(),
			Location: location,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Target != items[j].Target {
			return items[i].Target < items[j].Target
		}
		return items[i].Location < items[j].Location
	})
	return items
}

func renderFrontmatterEntries(front *frontmatter.Map, graph *query.Graph) []frontmatterEntry {
	if front == nil || front.IsEmpty() {
		return nil
	}

	keys := front.Keys()
	sort.Strings(keys)
	entries := make([]frontmatterEntry, 0, len(keys))
	for _, key := range keys {
		value, ok := front.Get(key)
		if !ok {
			continue
		}
		entries = append(entries, frontmatterEntry{
			Key:   key,
			Value: renderFrontmatterValue(value, graph),
		})
	}
	return entries
}

func renderFrontmatterValue(value any, graph *query.Graph) frontmatterValue {
	switch typed := value.(type) {
	case nil:
		return frontmatterValue{
			Kind: "scalar",
			Text: "null",
		}
	case string:
		return frontmatterValue{
			Kind:    "scalar",
			Text:    typed,
			Anchors: frontmatterAnchorsForString(typed, graph),
		}
	case time.Time:
		return frontmatterValue{
			Kind: "scalar",
			Text: frontmatter.FormatTimestamp(typed),
		}
	case bool:
		return frontmatterValue{
			Kind: "scalar",
			Text: fmt.Sprintf("%t", typed),
		}
	case int:
		return frontmatterValue{
			Kind: "scalar",
			Text: fmt.Sprintf("%d", typed),
		}
	case int8, int16, int32, int64:
		return frontmatterValue{
			Kind: "scalar",
			Text: fmt.Sprintf("%d", typed),
		}
	case uint, uint8, uint16, uint32, uint64:
		return frontmatterValue{
			Kind: "scalar",
			Text: fmt.Sprintf("%d", typed),
		}
	case float32, float64:
		return frontmatterValue{
			Kind: "scalar",
			Text: fmt.Sprintf("%v", typed),
		}
	case []string:
		items := make([]frontmatterValue, 0, len(typed))
		for _, item := range typed {
			items = append(items, renderFrontmatterValue(item, graph))
		}
		return frontmatterValue{
			Kind:  "list",
			Items: items,
		}
	case []any:
		items := make([]frontmatterValue, 0, len(typed))
		for _, item := range typed {
			items = append(items, renderFrontmatterValue(item, graph))
		}
		return frontmatterValue{
			Kind:  "list",
			Items: items,
		}
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		pairs := make([]frontmatterEntry, 0, len(keys))
		for _, key := range keys {
			pairs = append(pairs, frontmatterEntry{
				Key:   key,
				Value: renderFrontmatterValue(typed[key], graph),
			})
		}
		return frontmatterValue{
			Kind:  "map",
			Pairs: pairs,
		}
	default:
		return frontmatterValue{
			Kind: "scalar",
			Text: fmt.Sprintf("%v", typed),
		}
	}
}

func frontmatterAnchorsForString(raw string, graph *query.Graph) []frontmatterAnchor {
	if graph == nil || raw == "" {
		return nil
	}

	links := wikilink.Parse(raw)
	if len(links) == 0 {
		return nil
	}

	anchors := make([]frontmatterAnchor, 0, len(links))
	seen := map[string]struct{}{}
	for _, link := range links {
		if query.IsMediaTarget(link.Title()) {
			continue
		}

		label := linkLabel(link)
		resolved := graph.ResolveTarget(link.Target)
		if len(resolved) == 1 {
			path, err := notePathForKey(resolved[0])
			if err != nil {
				continue
			}
			signature := label + "\x00" + path
			if _, ok := seen[signature]; ok {
				continue
			}
			seen[signature] = struct{}{}
			anchors = append(anchors, frontmatterAnchor{
				Label: label,
				Path:  path,
			})
			continue
		}

		key := graph.UnresolvedTargetKey(link.Target)
		if key == "" {
			continue
		}
		path := "/broken?" + url.Values{"q": []string{key}}.Encode()
		signature := label + "\x00" + path
		if _, ok := seen[signature]; ok {
			continue
		}
		seen[signature] = struct{}{}
		anchors = append(anchors, frontmatterAnchor{
			Label: label,
			Path:  path,
		})
	}
	return anchors
}

func brokenEntries(snapshot vaultSnapshot, filter string) []brokenPageEntry {
	if snapshot.graph == nil {
		return nil
	}

	entries := []brokenPageEntry{}
	for _, note := range snapshot.notes {
		if note == nil {
			continue
		}
		sourcePath, err := notePathForNote(snapshot.graph, note)
		if err != nil {
			continue
		}

		for _, issue := range query.UnresolvedLinks(note, snapshot.graph) {
			key := snapshot.graph.UnresolvedTargetKey(issue.Link.Target)
			if key == "" {
				key = strings.ToLower(strings.TrimSpace(issue.Link.Target))
			}
			if key == "" {
				continue
			}
			if filter != "" && key != filter {
				continue
			}

			entries = append(entries, brokenPageEntry{
				SourceTitle: note.Title(),
				SourcePath:  sourcePath,
				Target:      key,
				Location:    brokenLocation(issue.Line, issue.FrontmatterKey),
				LinkText:    issue.Link.Raw,
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Target != entries[j].Target {
			return entries[i].Target < entries[j].Target
		}
		if entries[i].SourceTitle != entries[j].SourceTitle {
			return entries[i].SourceTitle < entries[j].SourceTitle
		}
		if entries[i].Location != entries[j].Location {
			return entries[i].Location < entries[j].Location
		}
		return entries[i].LinkText < entries[j].LinkText
	})
	return entries
}

func normalizeBrokenFilter(graph *query.Graph, queryText string) string {
	queryText = strings.TrimSpace(queryText)
	if queryText == "" || graph == nil {
		return ""
	}
	key := graph.UnresolvedTargetKey(queryText)
	if key != "" {
		return key
	}
	return strings.ToLower(queryText)
}

func brokenLocation(line int, frontmatterKey string) string {
	if frontmatterKey != "" {
		return "frontmatter." + frontmatterKey
	}
	if line > 0 {
		return fmt.Sprintf("line %d", line)
	}
	return "unknown"
}

func linkLabel(link wikilink.Link) string {
	if alias := strings.TrimSpace(link.Alias); alias != "" {
		return alias
	}
	if title := strings.TrimSpace(link.Title()); title != "" {
		return title
	}
	return strings.TrimSpace(link.Target)
}
