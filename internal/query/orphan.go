package query

import (
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
)

// BacklinkIndex maps a lowercased note title to the set of note paths that
// reference it. A title with no entry (or an entry of length zero) is an
// orphan.
type BacklinkIndex map[string][]string

// BuildBacklinkIndex scans every note's body and frontmatter for wikilinks and
// returns a title → referrers index. Both the body and any frontmatter string
// or string-list value are scanned; titles are lowercased so the lookup is
// case-insensitive.
//
// Notes that fail to load are skipped silently — a malformed note shouldn't
// prevent the rest of the vault from being indexed.
func BuildBacklinkIndex(notes []*vault.Note) BacklinkIndex {
	idx := BacklinkIndex{}
	for _, n := range notes {
		if n == nil {
			continue
		}
		body, err := n.Body()
		if err == nil {
			for _, link := range wikilink.Parse(body) {
				idx.add(link.Title(), n.Path)
			}
		}
		fm, err := n.Front()
		if err != nil {
			continue
		}
		for _, k := range fm.Keys() {
			v, _ := fm.Get(k)
			idx.scanValue(v, n.Path)
		}
	}
	return idx
}

func (idx BacklinkIndex) add(title, path string) {
	if title == "" {
		return
	}
	key := strings.ToLower(title)
	idx[key] = append(idx[key], path)
}

func (idx BacklinkIndex) scanValue(v any, path string) {
	switch vv := v.(type) {
	case string:
		for _, link := range wikilink.Parse(vv) {
			idx.add(link.Title(), path)
		}
	case []any:
		for _, item := range vv {
			idx.scanValue(item, path)
		}
	case map[string]any:
		for _, item := range vv {
			idx.scanValue(item, path)
		}
	}
}
