package query

import (
	"path/filepath"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
)

// BrokenLink describes a single unresolved wikilink in a note.
type BrokenLink struct {
	Link           wikilink.Link
	FrontmatterKey string
	Line           int
}

var mediaExts = map[string]struct{}{
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".svg": {}, ".webp": {},
	".pdf": {}, ".mp4": {}, ".mov": {}, ".mp3": {}, ".wav": {}, ".ogg": {}, ".heic": {},
}

func isMediaEmbed(title string) bool {
	ext := strings.ToLower(filepath.Ext(title))
	_, ok := mediaExts[ext]
	return ok
}

// UnresolvedLinks returns every wikilink in n (body and frontmatter) whose
// title does not appear in the graph's title set.
func UnresolvedLinks(n *vault.Note, g *Graph) []BrokenLink {
	if n == nil {
		return nil
	}
	var out []BrokenLink

	body, err := n.Body()
	if err == nil && body != "" {
		links := wikilink.Parse(body)
		nlOffsets := buildNewlineOffsets(body)
		for _, link := range links {
			title := link.Title()
			if title == "" || isMediaEmbed(title) {
				continue
			}
			if _, ok := g.Titles[strings.ToLower(title)]; ok {
				continue
			}
			line := lineFromOffset(nlOffsets, link.Start)
			out = append(out, BrokenLink{Link: link, Line: line})
		}
	}

	fm, err := n.Front()
	if err != nil {
		return out
	}
	for _, k := range fm.Keys() {
		v, _ := fm.Get(k)
		scanBrokenValue(v, k, g, &out)
	}
	return out
}

func scanBrokenValue(v any, key string, g *Graph, out *[]BrokenLink) {
	switch vv := v.(type) {
	case string:
		for _, link := range wikilink.Parse(vv) {
			title := link.Title()
			if title == "" || isMediaEmbed(title) {
				continue
			}
			if _, ok := g.Titles[strings.ToLower(title)]; !ok {
				*out = append(*out, BrokenLink{Link: link, FrontmatterKey: key, Line: 0})
			}
		}
	case []any:
		for _, item := range vv {
			scanBrokenValue(item, key, g, out)
		}
	case map[string]any:
		for _, item := range vv {
			scanBrokenValue(item, key, g, out)
		}
	}
}

func buildNewlineOffsets(s string) []int {
	var offsets []int
	for i, b := range []byte(s) {
		if b == '\n' {
			offsets = append(offsets, i)
		}
	}
	return offsets
}

func lineFromOffset(nlOffsets []int, offset int) int {
	line := 1
	for _, nlOff := range nlOffsets {
		if nlOff < offset {
			line++
		} else {
			break
		}
	}
	return line
}

// BrokenLinks returns a predicate that matches notes with at least one
// unresolved wikilink.
func BrokenLinks(g *Graph) Predicate {
	return func(n *vault.Note) bool {
		if n == nil {
			return false
		}
		return len(UnresolvedLinks(n, g)) > 0
	}
}
