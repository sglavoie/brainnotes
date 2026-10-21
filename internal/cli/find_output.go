package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/brainnotes/internal/pins"
	"github.com/sglavoie/brainnotes/internal/query"
	"github.com/sglavoie/brainnotes/internal/textnorm"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// pinnedOrder re-sorts matched notes into the canonical pinned order (ascending
// rank, then unranked, path-tiebroken) instead of the default path sort. Any
// note pins.List does not place keeps its path-sorted position at the end,
// which cannot happen while --pinned is the only caller.
func pinnedOrder(root string, matched []*vault.Note) []*vault.Note {
	byRel := make(map[string]*vault.Note, len(matched))
	for _, n := range matched {
		rel, err := filepath.Rel(root, n.Path)
		if err != nil {
			continue
		}
		byRel[filepath.ToSlash(rel)] = n
	}
	ordered := make([]*vault.Note, 0, len(matched))
	placed := make(map[*vault.Note]bool, len(matched))
	for _, e := range pins.List(root, matched) {
		n, ok := byRel[e.RelPath]
		if !ok || placed[n] {
			continue
		}
		placed[n] = true
		ordered = append(ordered, n)
	}
	for _, n := range matched {
		if !placed[n] {
			ordered = append(ordered, n)
		}
	}
	return ordered
}

type brokenRecord struct {
	relpath        string
	line           int
	frontmatterKey string
	raw            string
}

func printShowBroken(out io.Writer, matched []*vault.Note, g *query.Graph, root string) error {
	var records []brokenRecord
	for _, n := range matched {
		rel, err := filepath.Rel(root, n.Path)
		if err != nil {
			rel = n.Path
		}
		for _, bl := range query.UnresolvedLinks(n, g) {
			records = append(records, brokenRecord{
				relpath:        rel,
				line:           bl.Line,
				frontmatterKey: bl.FrontmatterKey,
				raw:            bl.Link.Raw,
			})
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].relpath != records[j].relpath {
			return records[i].relpath < records[j].relpath
		}
		if records[i].line != records[j].line {
			return records[i].line < records[j].line
		}
		if records[i].frontmatterKey != records[j].frontmatterKey {
			return records[i].frontmatterKey < records[j].frontmatterKey
		}
		return records[i].raw < records[j].raw
	})
	for _, r := range records {
		var err error
		if r.frontmatterKey != "" {
			_, err = fmt.Fprintf(out, "%s:frontmatter[%s]: %s\n", r.relpath, r.frontmatterKey, r.raw)
		} else {
			_, err = fmt.Fprintf(out, "%s:%d: %s\n", r.relpath, r.line, r.raw)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// printListBrokenTargets prints each distinct unresolved target title once.
// Titles are deduplicated by the graph's own unresolved-link key, which
// NFC-normalizes as well as folding case, so `[[Café]]` typed composed in one
// note and decomposed in another is one broken target rather than two lines
// that look identical.
func printListBrokenTargets(out io.Writer, matched []*vault.Note, g *query.Graph) error {
	seen := make(map[string]string)
	for _, n := range matched {
		for _, bl := range query.UnresolvedLinks(n, g) {
			title := bl.Link.Title()
			key := brokenTargetKey(g, title)
			if _, ok := seen[key]; !ok {
				seen[key] = title
			}
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	targets := make([]string, 0, len(keys))
	for _, key := range keys {
		targets = append(targets, seen[key])
	}
	for _, t := range targets {
		if _, err := fmt.Fprintf(out, "%s\n", t); err != nil {
			return err
		}
	}
	return nil
}

// brokenTargetKey is the dedup key of one unresolved title: the graph's key
// when it has one, else the same NFC-and-lowercase form it would build.
func brokenTargetKey(g *query.Graph, title string) string {
	if key := g.UnresolvedTargetKey(title); key != "" {
		return key
	}
	return strings.ToLower(textnorm.NFC(title))
}
