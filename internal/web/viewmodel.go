package web

import (
	"net/url"
	"sort"
	"strings"

	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/query"
	"github.com/sglavoie/brainnotes/internal/vault"
)

type noteLinkView struct {
	Title       string
	Path        string
	Count       int
	Occurrences []backlinkOccurrence
}

func (link noteLinkView) PreviewOccurrences() []backlinkOccurrence {
	return link.Occurrences[:min(2, len(link.Occurrences))]
}

func (link noteLinkView) RemainingOccurrences() []backlinkOccurrence {
	return link.Occurrences[min(2, len(link.Occurrences)):]
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
