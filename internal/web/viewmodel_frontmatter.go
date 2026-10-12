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
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
)

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
