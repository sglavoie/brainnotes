package queryexpr

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// ParseError is the only error this package returns. It carries enough to
// point at the offending text: the original Source, the byte Span inside it,
// the 1-based rune Column that span starts at, the message, and any
// did-you-mean candidates.
//
// Column is rune-based, not byte-based. A query is user-typed text and
// accented titles are ordinary in this vault, so a byte column would point
// into the middle of a character the moment a query contains "é".
type ParseError struct {
	Source  string
	Span    Span
	Column  int
	Msg     string
	Suggest []string
}

// NewParseError builds a ParseError, deriving Column from span.Start by rune
// count. Callers pass the whole query as source so Render can print it.
func NewParseError(source string, span Span, msg string, suggest []string) *ParseError {
	if span.Start < 0 {
		span.Start = 0
	}
	if span.Start > len(source) {
		span.Start = len(source)
	}
	if span.End < span.Start {
		span.End = span.Start
	}
	if span.End > len(source) {
		span.End = len(source)
	}
	return &ParseError{
		Source:  source,
		Span:    span,
		Column:  utf8.RuneCountInString(source[:span.Start]) + 1,
		Msg:     msg,
		Suggest: append([]string(nil), suggest...),
	}
}

// Error reports the message with its column, keeping the one-line form Go
// error handling expects. Render is the multi-line form for a terminal.
func (e *ParseError) Error() string {
	return "query: " + e.Msg + " at column " + strconv.Itoa(e.Column)
}

// Render draws the message, the source, and a caret run under the offending
// span:
//
//	query: unknown field "backlnks"
//	  tag:foo and backlnks>3
//	              ^^^^^^^^
//	  did you mean: backlinks
//
// Padding and caret width are rune counts, so the carets stay under the span
// even when earlier text is accented.
func (e *ParseError) Render() string {
	var b strings.Builder
	b.WriteString("query: ")
	b.WriteString(e.Msg)
	if e.Source != "" {
		// Clamp defensively: a ParseError built by hand rather than through
		// NewParseError may carry a span that does not fit its source.
		start := clamp(e.Span.Start, 0, len(e.Source))
		end := clamp(e.Span.End, start, len(e.Source))
		b.WriteString("\n  ")
		b.WriteString(e.Source)
		b.WriteString("\n  ")
		b.WriteString(strings.Repeat(" ", utf8.RuneCountInString(e.Source[:start])))
		width := utf8.RuneCountInString(e.Source[start:end])
		if width < 1 {
			width = 1
		}
		b.WriteString(strings.Repeat("^", width))
	}
	if len(e.Suggest) > 0 {
		b.WriteString("\n  did you mean: ")
		b.WriteString(strings.Join(e.Suggest, ", "))
	}
	return b.String()
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
