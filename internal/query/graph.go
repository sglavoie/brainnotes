package query

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
)

type Graph struct {
	Forward        map[string][]string
	Backward       map[string][]string
	Titles         map[string]struct{}
	PathsByTitle   map[string]string
	OriginalTitles map[string]string
}

func BuildGraph(notes []*vault.Note) *Graph {
	g := &Graph{
		Forward:        make(map[string][]string),
		Backward:       make(map[string][]string),
		Titles:         make(map[string]struct{}),
		PathsByTitle:   make(map[string]string),
		OriginalTitles: make(map[string]string),
	}

	for _, n := range notes {
		if n == nil {
			continue
		}
		base := filepath.Base(n.Path)
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		key := strings.ToLower(stem)
		g.Titles[key] = struct{}{}
		g.PathsByTitle[key] = n.Path
		g.OriginalTitles[key] = stem
	}

	for _, n := range notes {
		if n == nil {
			continue
		}
		srcBase := filepath.Base(n.Path)
		srcStem := strings.TrimSuffix(srcBase, filepath.Ext(srcBase))
		srcKey := strings.ToLower(srcStem)

		targets := make(map[string]struct{})

		body, err := n.Body()
		if err == nil {
			for _, link := range wikilink.Parse(body) {
				title := link.Title()
				if title == "" {
					continue
				}
				tKey := strings.ToLower(title)
				if tKey == srcKey {
					continue
				}
				g.Backward[tKey] = append(g.Backward[tKey], n.Path)
				if _, exists := g.Titles[tKey]; exists {
					targets[tKey] = struct{}{}
				}
			}
		}

		fm, err := n.Front()
		if err == nil {
			for _, k := range fm.Keys() {
				v, _ := fm.Get(k)
				g.scanValue(v, n.Path, srcKey, targets)
			}
		}

		if len(targets) > 0 {
			sorted := make([]string, 0, len(targets))
			for t := range targets {
				sorted = append(sorted, t)
			}
			sort.Strings(sorted)
			g.Forward[srcKey] = sorted
		}
	}

	return g
}

func (g *Graph) scanValue(v any, path, srcKey string, targets map[string]struct{}) {
	switch vv := v.(type) {
	case string:
		for _, link := range wikilink.Parse(vv) {
			title := link.Title()
			if title == "" {
				continue
			}
			tKey := strings.ToLower(title)
			if tKey == srcKey {
				continue
			}
			g.Backward[tKey] = append(g.Backward[tKey], path)
			if _, exists := g.Titles[tKey]; exists {
				targets[tKey] = struct{}{}
			}
		}
	case []any:
		for _, item := range vv {
			g.scanValue(item, path, srcKey, targets)
		}
	case map[string]any:
		for _, item := range vv {
			g.scanValue(item, path, srcKey, targets)
		}
	}
}

func (g *Graph) ForwardLinks(title string) []string {
	return g.Forward[strings.ToLower(title)]
}

func (g *Graph) BackLinks(title string) []string {
	return g.Backward[strings.ToLower(title)]
}

func (g *Graph) OutDegree(title string) int {
	return len(g.ForwardLinks(title))
}

func (g *Graph) InDegree(title string) int {
	paths := g.BackLinks(title)
	seen := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		seen[p] = struct{}{}
	}
	return len(seen)
}
