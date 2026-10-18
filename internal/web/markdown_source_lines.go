package web

import (
	"bytes"
	"strconv"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// Lines refer to the Markdown body, not its frontmatter. Both reading and
// preview use this renderer, so duplicate headings and Unicode need no second
// slug algorithm in the browser. Count newlines once for linear rendering.
type sourceLineTransformer struct{}

func (sourceLineTransformer) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	source := reader.Source()
	line, offset := 1, 0
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || (node.Kind() != ast.KindHeading && node.Kind() != ast.KindParagraph) || node.Lines().Len() == 0 {
			return ast.WalkContinue, nil
		}
		start := node.Lines().At(0).Start
		if start < offset {
			return ast.WalkContinue, nil
		}
		line += bytes.Count(source[offset:start], []byte{'\n'})
		offset = start
		node.SetAttributeString("data-source-line", []byte(strconv.Itoa(line)))
		return ast.WalkContinue, nil
	})
}
