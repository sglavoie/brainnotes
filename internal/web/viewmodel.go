package web

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/schema"
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
	Key      string
	Path     string
	Location string
}

type frontmatterEntry struct {
	Key       string
	Value     frontmatterValue
	EditValue string
	InputID   string
	CanSet    bool
	IsList    bool
	Type      string
	Allowed   []string
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
	SourceKey   string
	Target      string
	Key         string
	Location    string
	LinkText    string
}

func noteForRoute(snapshot vaultSnapshot, routeKey string) (*vault.Note, string, bool) {
	note, key, err := noteops.ResolveRouteNote(snapshot.notes, snapshot.graph, routeKey)
	if err != nil {
		return nil, "", false
	}
	return note, key, true
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
		signature := key + "\x00" + issue.Link.Target + "\x00" + location
		if _, ok := seen[signature]; ok {
			continue
		}
		seen[signature] = struct{}{}
		items = append(items, brokenAffordance{
			Target:   issue.Link.Target,
			Key:      key,
			Path:     "/broken?" + url.Values{"q": []string{key}}.Encode(),
			Location: location,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Key != items[j].Key {
			return items[i].Key < items[j].Key
		}
		if items[i].Target != items[j].Target {
			return items[i].Target < items[j].Target
		}
		return items[i].Location < items[j].Location
	})
	return items
}

func renderFrontmatterEntries(front *frontmatter.Map, graph *query.Graph, fmCfg *config.FrontmatterConfig) []frontmatterEntry {
	if front == nil || front.IsEmpty() {
		return nil
	}

	keys := front.Keys()
	sort.Strings(keys)
	entries := make([]frontmatterEntry, 0, len(keys))
	for i, key := range keys {
		value, ok := front.Get(key)
		if !ok {
			continue
		}
		entries = append(entries, renderFrontmatterEntry(key, value, i, graph, fmCfg))
	}
	return entries
}

func renderFrontmatterEntry(key string, value any, index int, graph *query.Graph, fmCfg *config.FrontmatterConfig) frontmatterEntry {
	entry := frontmatterEntry{
		Key:       key,
		Value:     renderFrontmatterValue(value, graph),
		EditValue: frontmatterEditValue(value),
		InputID:   frontmatterInputID(key, index),
		CanSet:    noteops.ValidFrontmatterKey(key),
	}
	if fmCfg == nil {
		return entry
	}
	entry.IsList = fmCfg.IsList(key)
	if def, ok := fmCfg.Definition(key); ok {
		entry.Type = schema.KindLabel(def)
		entry.Allowed = append([]string(nil), def.Allowed...)
	}
	return entry
}

func frontmatterPropertyChoices(fmCfg *config.FrontmatterConfig) []string {
	if fmCfg == nil {
		return nil
	}
	return fmCfg.PropertyNames()
}

func frontmatterInputID(key string, index int) string {
	var b strings.Builder
	b.WriteString("frontmatter-")
	b.WriteString(fmt.Sprintf("%d", index))
	for _, r := range strings.ToLower(key) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.TrimRight(b.String(), "-")
}

func frontmatterEditValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case time.Time:
		return frontmatter.FormatTimestamp(typed)
	case bool:
		return fmt.Sprintf("%t", typed)
	case int:
		return fmt.Sprintf("%d", typed)
	case int8, int16, int32, int64:
		return fmt.Sprintf("%d", typed)
	case uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", typed)
	case float32, float64:
		return fmt.Sprintf("%v", typed)
	case []string:
		return strings.Join(typed, ", ")
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			parts = append(parts, frontmatterEditValue(item))
		}
		return strings.Join(parts, ", ")
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func stringInSlice(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
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
