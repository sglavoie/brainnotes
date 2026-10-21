package query

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/wikilink"
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
	edges := sortedExportEdges(g, nodes, sorted)
	for _, e := range edges {
		edgeNodes[e.from] = struct{}{}
		edgeNodes[e.to] = struct{}{}
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
	return strings.ReplaceAll(s, `"`, "&quot;")
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

	edges := sortedExportEdges(g, nodes, sorted)
	edgeNodes := make(map[string]struct{})
	for _, e := range edges {
		edgeNodes[e.from] = struct{}{}
		edgeNodes[e.to] = struct{}{}
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

type exportEdge struct{ from, to string }

func sortedExportEdges(g *Graph, nodes map[string]struct{}, sources []string) []exportEdge {
	var edges []exportEdge
	seen := make(map[exportEdge]struct{})
	for _, src := range sources {
		dsts := append([]string(nil), g.Forward[src]...)
		sort.Strings(dsts)
		for _, dst := range dsts {
			if _, ok := nodes[dst]; !ok {
				continue
			}
			e := exportEdge{from: src, to: dst}
			if _, ok := seen[e]; ok {
				continue
			}
			seen[e] = struct{}{}
			edges = append(edges, e)
		}
	}
	return edges
}

// buildSlugMap assigns a unique Mermaid node ID to each title key. When two
// keys produce the same slug, the second (and subsequent) get a numeric suffix.
// The generated suffix is itself checked against all assigned slugs to avoid
// collisions with naturally occurring slugs (e.g. "a-b" and "a-b-1" both
// existing as distinct titles).
func buildSlugMap(keys []string, g *Graph) map[string]string {
	m := make(map[string]string, len(keys))
	assigned := make(map[string]struct{}) // all slugs assigned so far
	counter := make(map[string]int)       // base slug -> next suffix to try
	for _, key := range keys {
		base := slugify(g.originalTitle(key))
		if base == "" {
			base = "node"
		}
		if _, taken := assigned[base]; !taken {
			m[key] = base
			assigned[base] = struct{}{}
		} else {
			n := counter[base]
			if n == 0 {
				n = 1
			}
			candidate := fmt.Sprintf("%s-%d", base, n)
			for {
				if _, taken := assigned[candidate]; !taken {
					break
				}
				n++
				candidate = fmt.Sprintf("%s-%d", base, n)
			}
			m[key] = candidate
			assigned[candidate] = struct{}{}
			counter[base] = n + 1
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

// codeEdge is one emitted edge line in the `code` format.
type codeEdge struct {
	srcPath string
	srcLine int
	rawLink string
	fmKey   string // empty for body edges
	dstPath string
}

// ExportCode writes the graph as a list of edges in a VS Code-navigable
// "path:line" format. Unlike ExportDot / ExportMermaid, this exporter needs to
// re-walk each source note to recover per-link line numbers (the Graph struct
// deliberately does not carry them). Body edges include the line of the link;
// frontmatter edges go to source line 1 with a `frontmatter[<key>]` tag so
// they remain distinguishable. Isolated nodes (keys in nodes that contribute
// no edges) are emitted first as `path:1` lines.
func (g *Graph) ExportCode(w io.Writer, notes []*vault.Note, nodes map[string]struct{}) error {
	var edges []codeEdge
	sourcesWithEdges := make(map[string]struct{})

	for _, n := range notes {
		if n == nil {
			continue
		}
		srcKey := g.keyForNote(n)
		if srcKey == "" {
			continue
		}
		if _, ok := nodes[srcKey]; !ok {
			continue
		}

		body, err := n.Body()
		if err == nil && body != "" {
			links := wikilink.Parse(body)
			nlOffsets := buildNewlineOffsets(body)
			lineOffset := bodyLineOffset(n.Path)
			for _, link := range links {
				for _, e := range g.codeEdgesForLink(link, n.Path, srcKey, "", nodes) {
					e.srcLine = lineOffset + lineFromOffset(nlOffsets, link.Start)
					edges = append(edges, e)
					sourcesWithEdges[srcKey] = struct{}{}
				}
			}
		}

		fm, err := n.Front()
		if err == nil {
			for _, k := range fm.Keys() {
				v, _ := fm.Get(k)
				g.scanCodeValue(v, k, n.Path, srcKey, nodes, &edges, sourcesWithEdges)
			}
		}
	}

	sort.Slice(edges, func(i, j int) bool {
		if edges[i].srcPath != edges[j].srcPath {
			return edges[i].srcPath < edges[j].srcPath
		}
		if edges[i].srcLine != edges[j].srcLine {
			return edges[i].srcLine < edges[j].srcLine
		}
		if edges[i].fmKey != edges[j].fmKey {
			return edges[i].fmKey < edges[j].fmKey
		}
		return edges[i].rawLink < edges[j].rawLink
	})

	var isolated []string
	for _, key := range sortedKeys(nodes) {
		if _, ok := sourcesWithEdges[key]; ok {
			continue
		}
		p, ok := g.PathsByTitle[key]
		if !ok || p == "" {
			continue
		}
		isolated = append(isolated, p)
	}
	sort.Strings(isolated)

	for _, p := range isolated {
		if _, err := fmt.Fprintf(w, "%s:1\n", p); err != nil {
			return err
		}
	}

	for _, e := range edges {
		var line string
		if e.fmKey != "" {
			line = fmt.Sprintf("%s:%d: frontmatter[%s] %s -> %s:1\n", e.srcPath, e.srcLine, e.fmKey, e.rawLink, e.dstPath)
		} else {
			line = fmt.Sprintf("%s:%d: %s -> %s:1\n", e.srcPath, e.srcLine, e.rawLink, e.dstPath)
		}
		if _, err := fmt.Fprint(w, line); err != nil {
			return err
		}
	}

	return nil
}

func (g *Graph) codeEdgesForLink(link wikilink.Link, srcPath, srcKey, fmKey string, nodes map[string]struct{}) []codeEdge {
	title := link.Title()
	if title == "" {
		return nil
	}
	targetKeys := g.resolveLink(link)
	if len(targetKeys) == 0 {
		return nil
	}
	var out []codeEdge
	seen := make(map[string]struct{})
	for _, tk := range targetKeys {
		if tk == "" || tk == srcKey {
			continue
		}
		if _, ok := nodes[tk]; !ok {
			continue
		}
		dstPath, ok := g.PathsByTitle[tk]
		if !ok || dstPath == "" {
			continue
		}
		if _, dup := seen[tk]; dup {
			continue
		}
		seen[tk] = struct{}{}
		out = append(out, codeEdge{
			srcPath: srcPath,
			rawLink: link.Raw,
			fmKey:   fmKey,
			dstPath: dstPath,
		})
	}
	return out
}

func (g *Graph) scanCodeValue(v any, fmKey, srcPath, srcKey string, nodes map[string]struct{}, edges *[]codeEdge, sourcesWithEdges map[string]struct{}) {
	switch vv := v.(type) {
	case string:
		for _, link := range wikilink.Parse(vv) {
			for _, e := range g.codeEdgesForLink(link, srcPath, srcKey, fmKey, nodes) {
				e.srcLine = 1
				*edges = append(*edges, e)
				sourcesWithEdges[srcKey] = struct{}{}
			}
		}
	case []any:
		for _, item := range vv {
			g.scanCodeValue(item, fmKey, srcPath, srcKey, nodes, edges, sourcesWithEdges)
		}
	case map[string]any:
		for _, item := range vv {
			g.scanCodeValue(item, fmKey, srcPath, srcKey, nodes, edges, sourcesWithEdges)
		}
	}
}
