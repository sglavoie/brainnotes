package query

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

func (g *Graph) ExportDot(w io.Writer, nodes map[string]struct{}) error {
	if _, err := fmt.Fprintln(w, "digraph vault {"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "  rankdir=LR;"); err != nil {
		return err
	}

	sorted := sortedKeys(nodes)

	edgeNodes := make(map[string]struct{})
	type edge struct{ from, to string }
	var edges []edge
	for _, src := range sorted {
		for _, dst := range g.Forward[src] {
			if _, ok := nodes[dst]; !ok {
				continue
			}
			edges = append(edges, edge{src, dst})
			edgeNodes[src] = struct{}{}
			edgeNodes[dst] = struct{}{}
		}
	}

	for _, key := range sorted {
		if _, ok := edgeNodes[key]; ok {
			continue
		}
		label := g.originalTitle(key)
		if _, err := fmt.Fprintf(w, "  %q;\n", label); err != nil {
			return err
		}
	}

	for _, e := range edges {
		srcLabel := g.originalTitle(e.from)
		dstLabel := g.originalTitle(e.to)
		if _, err := fmt.Fprintf(w, "  %q -> %q;\n", srcLabel, dstLabel); err != nil {
			return err
		}
	}

	_, err := fmt.Fprintln(w, "}")
	return err
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	slug := slugRe.ReplaceAllString(strings.ToLower(s), "-")
	return strings.Trim(slug, "-")
}

func mermaidEscape(s string) string {
	return strings.ReplaceAll(s, `"`, "#quot;")
}

func (g *Graph) ExportMermaid(w io.Writer, nodes map[string]struct{}) error {
	if _, err := fmt.Fprintln(w, "graph LR"); err != nil {
		return err
	}

	sorted := sortedKeys(nodes)

	// Build a collision-free slug map: each lowercase title key gets a
	// unique Mermaid node identifier. When two titles slugify to the same
	// string (e.g. "A-B" and "A B" both produce "a-b"), a numeric suffix
	// is appended to disambiguate.
	slugMap := buildSlugMap(sorted, g)

	defined := make(map[string]struct{})

	type edge struct{ from, to string }
	var edges []edge
	edgeNodes := make(map[string]struct{})
	for _, src := range sorted {
		for _, dst := range g.Forward[src] {
			if _, ok := nodes[dst]; !ok {
				continue
			}
			edges = append(edges, edge{src, dst})
			edgeNodes[src] = struct{}{}
			edgeNodes[dst] = struct{}{}
		}
	}

	for _, key := range sorted {
		if _, ok := edgeNodes[key]; ok {
			continue
		}
		label := g.originalTitle(key)
		slug := slugMap[key]
		if _, err := fmt.Fprintf(w, "  %s[\"%s\"]\n", slug, mermaidEscape(label)); err != nil {
			return err
		}
		defined[key] = struct{}{}
	}

	for _, e := range edges {
		srcLabel := g.originalTitle(e.from)
		dstLabel := g.originalTitle(e.to)
		srcSlug := slugMap[e.from]
		dstSlug := slugMap[e.to]

		var srcPart, dstPart string
		if _, ok := defined[e.from]; !ok {
			srcPart = fmt.Sprintf("%s[\"%s\"]", srcSlug, mermaidEscape(srcLabel))
			defined[e.from] = struct{}{}
		} else {
			srcPart = srcSlug
		}
		if _, ok := defined[e.to]; !ok {
			dstPart = fmt.Sprintf("%s[\"%s\"]", dstSlug, mermaidEscape(dstLabel))
			defined[e.to] = struct{}{}
		} else {
			dstPart = dstSlug
		}

		if _, err := fmt.Fprintf(w, "  %s --> %s\n", srcPart, dstPart); err != nil {
			return err
		}
	}

	return nil
}

// buildSlugMap assigns a unique Mermaid node ID to each title key. When two
// keys produce the same slug, the second (and subsequent) get a numeric suffix.
func buildSlugMap(keys []string, g *Graph) map[string]string {
	m := make(map[string]string, len(keys))
	used := make(map[string]int) // slug -> count of times assigned
	for _, key := range keys {
		base := slugify(g.originalTitle(key))
		if base == "" {
			base = "node"
		}
		count := used[base]
		used[base] = count + 1
		if count == 0 {
			m[key] = base
		} else {
			m[key] = fmt.Sprintf("%s-%d", base, count)
		}
	}
	return m
}

func (g *Graph) originalTitle(key string) string {
	if t, ok := g.OriginalTitles[key]; ok {
		return t
	}
	return key
}

func sortedKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
