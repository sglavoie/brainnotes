package web

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sglavoie/brainnotes/internal/query"
	"github.com/sglavoie/brainnotes/internal/wikilink"
)

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
		sourceKey := snapshot.graph.KeyForNote(note)
		if sourceKey == "" {
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
				SourceKey:   sourceKey,
				Target:      issue.Link.Target,
				Key:         key,
				Location:    brokenLocation(issue.Line, issue.FrontmatterKey),
				LinkText:    issue.Link.Raw,
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Key != entries[j].Key {
			return entries[i].Key < entries[j].Key
		}
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
