package queryexpr

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sglavoie/brainnotes/internal/textnorm"
)

// tokenKind classifies one lexeme.
type tokenKind int

const (
	tokEOF tokenKind = iota
	tokWord
	tokString
	tokWikilink
	tokLParen
	tokRParen
	tokLBracket
	tokRBracket
	tokComma
	tokColon
	tokMinus
	tokOp
)

// token is one lexeme plus the byte span it occupied in the source. Text is
// the decoded, NFC-normalized content: quotes stripped, escapes resolved, and
// wikilink brackets removed, so a parser never re-slices the source.
type token struct {
	Kind tokenKind
	Text string
	Span Span
}

// describe names a token for an error message.
func (t token) describe() string {
	switch t.Kind {
	case tokEOF:
		return "end of query"
	case tokWord:
		return "word " + quote(t.Text)
	case tokString:
		return "phrase " + quote(t.Text)
	case tokWikilink:
		return "wikilink [[" + t.Text + "]]"
	case tokOp:
		return "operator " + quote(t.Text)
	default:
		return quote(t.Text)
	}
}

// isWordRune reports whether r may appear inside a bare word. Everything that
// is not whitespace or structural punctuation qualifies, which keeps paths,
// dates, durations, globs, and hyphenated field names (`links-to`,
// `cluster-size`) unquoted. A leading `-` is still negation: the lexer only
// reaches word scanning once it has ruled that out.
func isWordRune(r rune) bool {
	if unicode.IsSpace(r) {
		return false
	}
	switch r {
	case '(', ')', '[', ']', ',', ':', '"', '<', '>', '=', '!':
		return false
	}
	return true
}

// lex splits src into tokens, always terminated by a tokEOF whose span is the
// empty range at the end of the source.
func lex(src string) ([]token, *ParseError) {
	var toks []token
	i := 0
	for i < len(src) {
		r, size := utf8.DecodeRuneInString(src[i:])
		switch {
		case unicode.IsSpace(r):
			i += size
		case r == '(':
			toks = append(toks, punct(tokLParen, src, i, size))
			i += size
		case r == ')':
			toks = append(toks, punct(tokRParen, src, i, size))
			i += size
		case r == ',':
			toks = append(toks, punct(tokComma, src, i, size))
			i += size
		case r == ':':
			toks = append(toks, punct(tokColon, src, i, size))
			i += size
		case r == '-':
			toks = append(toks, punct(tokMinus, src, i, size))
			i += size
		case r == ']':
			toks = append(toks, punct(tokRBracket, src, i, size))
			i += size
		case r == '[':
			// `[[` always opens a wikilink, so a list of wikilinks has to be
			// written `[ [[a]], [[b]] ]`; Value.String() emits that spacing.
			if strings.HasPrefix(src[i:], "[[") {
				tok, err := lexWikilink(src, i)
				if err != nil {
					return nil, err
				}
				toks = append(toks, tok)
				i = tok.Span.End
				continue
			}
			toks = append(toks, punct(tokLBracket, src, i, size))
			i += size
		case r == '"':
			tok, err := lexString(src, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, tok)
			i = tok.Span.End
		case r == '=' || r == '!' || r == '<' || r == '>':
			tok, err := lexOperator(src, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, tok)
			i = tok.Span.End
		default:
			tok := lexWord(src, i)
			toks = append(toks, tok)
			i = tok.Span.End
		}
	}
	toks = append(toks, token{Kind: tokEOF, Span: Span{Start: len(src), End: len(src)}})
	return toks, nil
}

func punct(kind tokenKind, src string, start, size int) token {
	return token{Kind: kind, Text: src[start : start+size], Span: Span{Start: start, End: start + size}}
}

// lexWikilink consumes `[[Target#Heading|Alias]]`, keeping the inner text
// verbatim so wikilink resolution — not the lexer — decides what it means.
func lexWikilink(src string, start int) (token, *ParseError) {
	rest := src[start+2:]
	end := strings.Index(rest, "]]")
	if end < 0 {
		return token{}, NewParseError(src, Span{Start: start, End: len(src)}, "unterminated wikilink: missing \"]]\"", nil)
	}
	inner := rest[:end]
	span := Span{Start: start, End: start + 2 + end + 2}
	return token{Kind: tokWikilink, Text: textnorm.NFC(inner), Span: span}, nil
}

// lexString consumes a double-quoted phrase. Only `\"` and `\\` are escapes;
// every other backslash is literal, so a Windows-style path inside a phrase
// survives untouched.
func lexString(src string, start int) (token, *ParseError) {
	var b strings.Builder
	i := start + 1
	for i < len(src) {
		r, size := utf8.DecodeRuneInString(src[i:])
		switch r {
		case '\\':
			if i+size < len(src) {
				next, nextSize := utf8.DecodeRuneInString(src[i+size:])
				if next == '"' || next == '\\' {
					b.WriteRune(next)
					i += size + nextSize
					continue
				}
			}
			b.WriteRune(r)
			i += size
		case '"':
			span := Span{Start: start, End: i + size}
			return token{Kind: tokString, Text: textnorm.NFC(b.String()), Span: span}, nil
		default:
			b.WriteRune(r)
			i += size
		}
	}
	return token{}, NewParseError(src, Span{Start: start, End: len(src)}, "unterminated quoted phrase: missing closing \"", nil)
}

// lexOperator consumes one comparison operator. `==` is accepted and
// canonicalized to `=`.
func lexOperator(src string, start int) (token, *ParseError) {
	two := ""
	if start+2 <= len(src) {
		two = src[start : start+2]
	}
	switch two {
	case "!=", "<=", ">=":
		return token{Kind: tokOp, Text: two, Span: Span{Start: start, End: start + 2}}, nil
	case "==":
		return token{Kind: tokOp, Text: "=", Span: Span{Start: start, End: start + 2}}, nil
	}
	switch src[start] {
	case '=':
		return token{Kind: tokOp, Text: "=", Span: Span{Start: start, End: start + 1}}, nil
	case '<', '>':
		return token{Kind: tokOp, Text: src[start : start+1], Span: Span{Start: start, End: start + 1}}, nil
	}
	// Only '!' can reach here, and it is meaningless on its own.
	return token{}, NewParseError(src, Span{Start: start, End: start + 1}, "unexpected \"!\": did you mean \"!=\"?", nil)
}

// lexWord consumes a bare word. It cannot fail: the caller only dispatches
// here for a rune that is already known to be a word rune.
func lexWord(src string, start int) token {
	i := start
	for i < len(src) {
		r, size := utf8.DecodeRuneInString(src[i:])
		if !isWordRune(r) {
			break
		}
		i += size
	}
	span := Span{Start: start, End: i}
	return token{Kind: tokWord, Text: textnorm.NFC(src[start:i]), Span: span}
}
