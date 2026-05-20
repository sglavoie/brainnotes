package query

import (
	"path/filepath"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
)

// TitleIndex is the set of lowercased note stems present in the vault.
type TitleIndex map[string]struct{}

// BuildTitleIndex populates a TitleIndex from the given notes.
func BuildTitleIndex(notes []*vault.Note) TitleIndex {
	idx := make(TitleIndex, len(notes))
	for _, n := range notes {
		if n == nil {
			continue
		}
		stem := strings.ToLower(strings.TrimSuffix(filepath.Base(n.Path), ".md"))
		idx[stem] = struct{}{}
	}
	return idx
}

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
// title does not appear in idx.
func UnresolvedLinks(n *vault.Note, idx TitleIndex) []BrokenLink {
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
			if _, ok := idx[strings.ToLower(title)]; ok {
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
		scanBrokenValue(v, k, idx, &out)
	}
	return out
}

func scanBrokenValue(v any, key string, idx TitleIndex, out *[]BrokenLink) {
	switch vv := v.(type) {
	case string:
		for _, link := range wikilink.Parse(vv) {
			title := link.Title()
			if title == "" || isMediaEmbed(title) {
				continue
			}
			if _, ok := idx[strings.ToLower(title)]; !ok {
				*out = append(*out, BrokenLink{Link: link, FrontmatterKey: key, Line: 0})
			}
		}
	case []any:
		for _, item := range vv {
			scanBrokenValue(item, key, idx, out)
		}
	case map[string]any:
		for _, item := range vv {
			scanBrokenValue(item, key, idx, out)
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
func BrokenLinks(idx TitleIndex) Predicate {
	return func(n *vault.Note) bool {
		return len(UnresolvedLinks(n, idx)) > 0
	}
}
