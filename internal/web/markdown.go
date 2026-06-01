package web

import (
	"bytes"
	"fmt"
	"html/template"
	"net/url"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/yuin/goldmark"
	"go.abhg.dev/goldmark/wikilink"
)

type markdownRenderer struct {
	md    goldmark.Markdown
	state *vaultState
}

func newMarkdownRenderer(state *vaultState) (*markdownRenderer, error) {
	if state == nil {
		return nil, fmt.Errorf("web: nil vault state")
	}

	md := goldmark.New(
		goldmark.WithExtensions(&wikilink.Extender{
			Resolver: wikilinkResolver{
				state: state,
				root:  state.vault.Root,
			},
		}),
	)
	return &markdownRenderer{
		md:    md,
		state: state,
	}, nil
}

func (r *markdownRenderer) render(source string) (template.HTML, error) {
	return renderWithMarkdown(r.md, source)
}

func (r *markdownRenderer) renderWithGraph(source string, graph *query.Graph) (template.HTML, error) {
	if graph == nil {
		return "", errorsf("nil graph for markdown render")
	}
	md := goldmark.New(
		goldmark.WithExtensions(&wikilink.Extender{
			Resolver: wikilinkResolver{
				graph: graph,
				root:  r.state.vault.Root,
			},
		}),
	)
	return renderWithMarkdown(md, source)
}

func renderWithMarkdown(md goldmark.Markdown, source string) (template.HTML, error) {
	var buf bytes.Buffer
	if err := md.Convert([]byte(source), &buf); err != nil {
		return "", fmt.Errorf("web: render markdown: %w", err)
	}
	return template.HTML(buf.String()), nil
}

type wikilinkResolver struct {
	state *vaultState
	graph *query.Graph
	root  string
}

func (r wikilinkResolver) ResolveWikilink(node *wikilink.Node) ([]byte, error) {
	if node == nil {
		return nil, nil
	}

	target := string(node.Target)
	if target == "" {
		return nil, nil
	}

	if node.Embed && query.IsMediaTarget(target) {
		mediaPath, ok, err := mediaPathForTarget(r.root, target)
		if err != nil {
			return nil, err
		}
		if ok {
			return []byte(mediaPath), nil
		}
		return nil, nil
	}

	graph, err := r.currentGraph()
	if err != nil {
		return nil, err
	}
	if graph == nil {
		return nil, nil
	}

	resolved := graph.ResolveTarget(target)
	if len(resolved) == 1 {
		path, err := notePathForKey(resolved[0])
		if err != nil {
			return nil, err
		}
		return []byte(path), nil
	}

	key := graph.UnresolvedTargetKey(target)
	if key == "" {
		key = strings.TrimSpace(target)
	}
	if key == "" {
		return nil, nil
	}
	return []byte("/broken?" + url.Values{"q": []string{key}}.Encode()), nil
}

func (r wikilinkResolver) currentGraph() (*query.Graph, error) {
	if r.graph != nil {
		return r.graph, nil
	}
	if r.state == nil {
		return nil, nil
	}
	snapshot, err := r.state.currentSnapshot()
	if err != nil {
		return nil, err
	}
	return snapshot.graph, nil
}
