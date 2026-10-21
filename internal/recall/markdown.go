package recall

import (
	"bytes"
	"strings"

	"github.com/sglavoie/brainnotes/internal/textnorm"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extensionast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

var markdownParser = goldmark.New(goldmark.WithExtensions(extension.Table))

func extractMarkdown(body string) ([]MarkdownBlock, []TextField) {
	source := []byte(body)
	parseSource := markdownParseSource(source)
	document := markdownParser.Parser().Parse(text.NewReader(parseSource))

	var blocks []MarkdownBlock
	var fields []TextField
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}

		switch typed := node.(type) {
		case *ast.CodeBlock, *ast.FencedCodeBlock, *ast.HTMLBlock, *ast.LinkReferenceDefinition:
			return ast.WalkSkipChildren, nil
		case *extensionast.Table:
			appendMarkdownBlock(&blocks, &fields, parseSource, typed, BlockTable, TextProse, ProseWeight, 0)
			return ast.WalkSkipChildren, nil
		case *ast.Heading:
			appendMarkdownBlock(&blocks, &fields, parseSource, typed, BlockHeading, TextHeading, HeadingWeight, typed.Level)
			return ast.WalkSkipChildren, nil
		case *ast.Paragraph:
			appendMarkdownBlock(&blocks, &fields, parseSource, typed, BlockProse, TextProse, ProseWeight, 0)
			return ast.WalkSkipChildren, nil
		case *ast.TextBlock:
			appendMarkdownBlock(&blocks, &fields, parseSource, typed, BlockProse, TextProse, ProseWeight, 0)
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return blocks, fields
}

func markdownParseSource(source []byte) []byte {
	if !bytes.HasPrefix(source, []byte{0xef, 0xbb, 0xbf}) {
		return source
	}
	// Goldmark treats a leading BOM as ordinary text. Replacing its three
	// bytes with the maximum legal Markdown indentation keeps every byte
	// offset stable while allowing an immediately following block marker (for
	// example, "# Heading") to parse normally. Document.Body remains untouched.
	parseSource := append([]byte(nil), source...)
	copy(parseSource[:3], []byte("   "))
	return parseSource
}

func appendMarkdownBlock(
	blocks *[]MarkdownBlock,
	fields *[]TextField,
	source []byte,
	node ast.Node,
	blockKind BlockKind,
	textKind TextKind,
	weight float64,
	headingLevel int,
) {
	plain := plainText(node, source)
	if plain == "" {
		return
	}
	location, ok := blockLocation(node, source)
	if !ok {
		return
	}
	blockIndex := len(*blocks)
	*blocks = append(*blocks, MarkdownBlock{
		Kind:         blockKind,
		Text:         plain,
		Location:     location,
		HeadingLevel: headingLevel,
	})
	*fields = append(*fields, TextField{
		Kind:       textKind,
		Text:       plain,
		Weight:     weight,
		BlockIndex: blockIndex,
	})
}

func plainText(root ast.Node, source []byte) string {
	var builder strings.Builder
	writeGap := func() {
		if builder.Len() == 0 {
			return
		}
		value := builder.String()
		if !isSpace(value[len(value)-1]) {
			builder.WriteByte(' ')
		}
	}

	_ = ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch typed := node.(type) {
		case *ast.CodeBlock, *ast.FencedCodeBlock, *ast.HTMLBlock, *ast.LinkReferenceDefinition, *ast.RawHTML:
			return ast.WalkSkipChildren, nil
		case *extensionast.TableCell:
			writeGap()
		case *ast.Text:
			builder.Write(typed.Value(source))
			if typed.SoftLineBreak() || typed.HardLineBreak() {
				writeGap()
			}
		case *ast.String:
			builder.Write(typed.Value)
		case *ast.AutoLink:
			builder.Write(typed.Label(source))
		}
		return ast.WalkContinue, nil
	})

	value := strings.TrimPrefix(builder.String(), "\ufeff")
	return textnorm.NFC(strings.Join(strings.Fields(value), " "))
}

func isSpace(value byte) bool {
	switch value {
	case ' ', '\t', '\n', '\r':
		return true
	default:
		return false
	}
}

func blockLocation(root ast.Node, source []byte) (SourceLocation, bool) {
	start := len(source)
	end := -1
	contentStart := len(source)
	_ = ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if node.Type() != ast.TypeBlock {
			return ast.WalkContinue, nil
		}
		lines := node.Lines()
		for index := range lines.Len() {
			segment := lines.At(index)
			if segment.Start < 0 || segment.Stop < segment.Start || segment.Stop > len(source) {
				continue
			}
			if segment.Start < start {
				start = segment.Start
			}
			if segment.Start < contentStart {
				contentStart = segment.Start
			}
			if segment.Stop > end {
				end = segment.Stop
			}
		}
		return ast.WalkContinue, nil
	})
	if start == len(source) || end < start {
		return SourceLocation{}, false
	}

	start = lineStart(source, start)
	end = lineEnd(source, segmentContentEnd(source, end))
	if _, ok := root.(*ast.Heading); ok && !headingIsATX(source, start, contentStart) {
		end = followingLineEnd(source, end)
	}
	if table, ok := root.(*extensionast.Table); ok && table.ChildCount() == 1 {
		end = followingLineEnd(source, end)
	}
	if end < start {
		return SourceLocation{}, false
	}

	return SourceLocation{
		Start:     start,
		End:       end,
		StartLine: bytes.Count(source[:start], []byte{'\n'}) + 1,
		EndLine:   bytes.Count(source[:end], []byte{'\n'}) + 1,
	}, true
}

func lineStart(source []byte, offset int) int {
	if offset > len(source) {
		offset = len(source)
	}
	if index := bytes.LastIndexByte(source[:offset], '\n'); index >= 0 {
		return index + 1
	}
	return 0
}

func lineEnd(source []byte, offset int) int {
	if offset < 0 {
		offset = 0
	}
	if offset > len(source) {
		offset = len(source)
	}
	if index := bytes.IndexByte(source[offset:], '\n'); index >= 0 {
		end := offset + index
		if end > 0 && source[end-1] == '\r' {
			end--
		}
		return end
	}
	end := len(source)
	if end > 0 && source[end-1] == '\r' {
		end--
	}
	return end
}

func segmentContentEnd(source []byte, offset int) int {
	if offset > len(source) {
		offset = len(source)
	}
	if offset > 0 && source[offset-1] == '\n' {
		offset--
	}
	if offset > 0 && source[offset-1] == '\r' {
		offset--
	}
	return offset
}

func followingLineEnd(source []byte, currentEnd int) int {
	if currentEnd >= len(source) {
		return currentEnd
	}
	newline := bytes.IndexByte(source[currentEnd:], '\n')
	if newline < 0 {
		return currentEnd
	}
	nextStart := currentEnd + newline + 1
	if nextStart >= len(source) {
		return currentEnd
	}
	return lineEnd(source, nextStart)
}

func headingIsATX(source []byte, rawStart, contentStart int) bool {
	if rawStart < 0 || contentStart < rawStart || contentStart > len(source) {
		return false
	}
	return bytes.Contains(source[rawStart:contentStart], []byte{'#'})
}
