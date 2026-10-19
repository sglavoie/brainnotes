package web

import (
	"bytes"
	"fmt"
	"html/template"
	"net/url"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
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

	md := goldmarkWithResolver(wikilinkResolver{
		state: state,
		root:  state.vault.Root,
	})
	return &markdownRenderer{
		md:    md,
		state: state,
	}, nil
}

// focusablePre wraps a highlighted fence in a keyboard-focusable <pre>. A long
// line scrolls the block horizontally, and a scroll region a keyboard cannot
// reach is one only a mouse can read; Chroma used to emit this tabindex itself
// and stopped, so it is stated here rather than inherited.
type focusablePre struct{}

func (focusablePre) Start(code bool, styleAttr string) string {
	if code {
		return fmt.Sprintf(`<pre tabindex="0"%s><code>`, styleAttr)
	}
	return fmt.Sprintf(`<pre tabindex="0"%s>`, styleAttr)
}

func (focusablePre) End(code bool) string {
	if code {
		return `</code></pre>`
	}
	return `</pre>`
}

func goldmarkWithResolver(resolver wikilinkResolver) goldmark.Markdown {
	return goldmark.New(
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
			parser.WithASTTransformers(util.Prioritized(headingAnchorTransformer{}, 100), util.Prioritized(sourceLineTransformer{}, 110)),
		),
		goldmark.WithExtensions(
			highlighting.NewHighlighting(
				highlighting.WithStyle("nord"),
				highlighting.WithGuessLanguage(false),
				// Emit stable Chroma CSS classes instead of inline color
				// styles so light/dark syntax palettes can be themed from
				// app.css (scoped .chroma blocks) rather than baked into HTML.
				highlighting.WithFormatOptions(
					chromahtml.WithClasses(true),
					chromahtml.WithPreWrapper(focusablePre{}),
				),
			),
			// GFM bundles Linkify (bare URL support), Tables,
			// Strikethrough, and read-only task lists. Footnote,
			// DefinitionList, and Typographer round out the semantic
			// Markdown feature set shared by normal and graph-aware
			// rendering.
			extension.GFM,
			extension.Footnote,
			extension.DefinitionList,
			extension.Typographer,
			&wikilink.Extender{
				Resolver: resolver,
			},
		),
	)
}

func (r *markdownRenderer) render(source string) (template.HTML, error) {
	return renderWithMarkdown(r.md, source)
}

func (r *markdownRenderer) renderWithGraph(source string, graph *query.Graph) (template.HTML, error) {
	if graph == nil {
		return "", errorsf("nil graph for markdown render")
	}
	md := goldmarkWithResolver(wikilinkResolver{
		graph: graph,
		root:  r.state.vault.Root,
	})
	return renderWithMarkdown(md, source)
}

func renderWithMarkdown(md goldmark.Markdown, source string) (template.HTML, error) {
	var buf bytes.Buffer
	if err := md.Convert([]byte(source), &buf); err != nil {
		return "", fmt.Errorf("web: render markdown: %w", err)
	}
	return template.HTML(buf.String()), nil
}

type headingAnchorTransformer struct{}

func (headingAnchorTransformer) Transform(doc *ast.Document, _ text.Reader, _ parser.Context) {
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || node.Kind() != ast.KindHeading {
			return ast.WalkContinue, nil
		}

		id := nodeAttributeString(node, "id")
		if id == "" {
			return ast.WalkContinue, nil
		}

		link := ast.NewLink()
		link.Destination = []byte("#" + url.PathEscape(id))
		link.Title = []byte("Heading link")
		link.SetAttributeString("class", "heading-anchor")
		link.SetAttributeString("aria-label", "Heading link")
		link.AppendChild(link, ast.NewString([]byte("#")))

		node.AppendChild(node, ast.NewString([]byte(" ")))
		node.AppendChild(node, link)
		return ast.WalkContinue, nil
	})
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
