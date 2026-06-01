package query

import (
	"os"
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

// IsMediaTarget reports whether title points to a media file extension.
func IsMediaTarget(title string) bool {
	ext := strings.ToLower(filepath.Ext(title))
	_, ok := mediaExts[ext]
	return ok
}

// UnresolvedLinks returns every wikilink in n (body and frontmatter) whose
// target does not resolve in the graph. Folder-qualified targets follow the
// same semantics as BuildGraph: exact path match first, then title fallback
// only when the title is unique.
func UnresolvedLinks(n *vault.Note, g *Graph) []BrokenLink {
	if n == nil {
		return nil
	}
	var out []BrokenLink

	body, err := n.Body()
	if err == nil && body != "" {
		links := wikilink.Parse(body)
		nlOffsets := buildNewlineOffsets(body)
		lineOffset := bodyLineOffset(n.Path)
		for _, link := range links {
			title := link.Title()
			if title == "" || IsMediaTarget(title) {
				continue
			}
			if brokenLinkResolves(link, g) {
				continue
			}
			line := lineOffset + lineFromOffset(nlOffsets, link.Start)
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
			if title == "" || IsMediaTarget(title) {
				continue
			}
			if brokenLinkResolves(link, g) {
				continue
			}
			*out = append(*out, BrokenLink{Link: link, FrontmatterKey: key, Line: 0})
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

func brokenLinkResolves(link wikilink.Link, g *Graph) bool {
	return len(g.resolveLink(link)) > 0
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

func bodyLineOffset(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	raw := strings.TrimPrefix(string(data), "\ufeff")
	startLen, ok := frontmatterOpenDelimLen(raw)
	if !ok {
		return 0
	}
	bodyOff, ok := frontmatterBodyOffset(raw[startLen:])
	if !ok {
		return 0
	}
	return strings.Count(raw[:startLen+bodyOff], "\n")
}

func frontmatterOpenDelimLen(s string) (int, bool) {
	if !strings.HasPrefix(s, "---") {
		return 0, false
	}
	nl := strings.IndexByte(s, '\n')
	if nl < 0 {
		return 0, false
	}
	if isFrontmatterDelimLine(s[:nl]) {
		return nl + 1, true
	}
	return 0, false
}

func frontmatterBodyOffset(s string) (int, bool) {
	for i := 0; i < len(s); {
		eol := strings.IndexByte(s[i:], '\n')
		var line string
		var lineEnd int
		if eol == -1 {
			line = s[i:]
			lineEnd = len(s)
		} else {
			line = s[i : i+eol]
			lineEnd = i + eol + 1
		}
		if isFrontmatterDelimLine(line) {
			return lineEnd, true
		}
		if eol == -1 {
			break
		}
		i = lineEnd
	}
	return 0, false
}

func isFrontmatterDelimLine(line string) bool {
	line = strings.TrimRight(line, "\r")
	line = strings.TrimRight(line, " \t")
	return line == "---"
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
