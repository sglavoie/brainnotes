// Package wikilink parses and rewrites Obsidian-style [[wikilinks]] in markdown
// text. Parsing is code-block aware: links inside fenced, indented, or inline
// code are ignored so that documentation samples and code snippets are never
// touched by rewrites. All offsets are byte offsets in the input string.
package wikilink

import (
	"regexp"
	"strings"
)

// Link is a single wikilink occurrence.
//
// Target is the full path-or-title before the optional `#heading` and `|alias`
// (e.g. "Books/Foo" for `[[Books/Foo]]`). Folder is the prefix up to the last
// `/`, empty when the link has no folder component. Heading and Alias capture
// the optional fragment and display text. Start/End are byte offsets in the
// source text — End is exclusive (`text[Start:End]` returns the full `[[...]]`
// span).
//
// EscapedPipe records the Obsidian table form `[[Target\|Alias]]`, where the
// alias separator is written `\|` so it does not split a Markdown table cell.
// The backslash belongs to the separator, not to Target (or Heading), and a
// rewrite must write the separator back the same way: see WithTarget.
type Link struct {
	Raw         string
	Target      string
	Folder      string
	Heading     string
	Alias       string
	EscapedPipe bool
	Start       int
	End         int
}

// WithTarget renders l pointing at target, keeping its heading, alias and
// alias-separator form, so a rename inside a Markdown table keeps `\|`.
func (l Link) WithTarget(target string) string {
	return formatLink(target, l.Heading, l.Alias, l.EscapedPipe)
}

// Title returns the last path segment of Target — the bit after the final `/`,
// or Target itself when there is no folder prefix.
func (l Link) Title() string {
	idx := strings.LastIndex(l.Target, "/")
	if idx < 0 {
		return l.Target
	}
	return l.Target[idx+1:]
}

// wikilinkRE matches `[[Target]]`, `[[Target#Heading]]`, `[[Target|Alias]]`,
// or any combination. Capture groups: 1=Target, 2=Heading, 3=Alias.
//
// The negated character classes exclude `\n` and `\r` so a stray `[[` followed
// by a `]]` on a later line is not mistaken for a single multi-line wikilink.
// Obsidian wikilinks are single-line by convention, so this restriction
// removes a class of false positives without losing real matches.
//
// `[` is also excluded from the target, heading, and alias so that strings
// like `[[[Foo]]]` (a literal `[` followed by a `[[Foo]]` wikilink and a
// stray `]`) do not get parsed as a single wikilink whose target begins with
// `[`. Obsidian rejects `[` and `]` in note titles, headings, and aliases,
// so this restriction matches real-world data.
var wikilinkRE = regexp.MustCompile(`\[\[([^\]\[|#\r\n]+)(?:#([^\]\[|\r\n]+))?(?:\|([^\]\[\r\n]+))?\]\]`)

// Parse returns every wikilink in s, in source order, skipping any match that
// falls inside a fenced code block, indented code block, or inline code span.
// The returned slice is nil when s contains no links.
func Parse(s string) []Link {
	mask := buildCodeMask(s)
	matches := wikilinkRE.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return nil
	}
	var links []Link
	for _, m := range matches {
		start, end := m[0], m[1]
		if rangeOverlapsAny(start, end, mask) {
			continue
		}
		// Obsidian trims surrounding whitespace around the target, heading,
		// and alias when resolving the link, so we normalize them here. This
		// keeps Title()-based comparisons (used by rewrite, backlinks, orphan
		// detection) consistent for inputs like `[[ Foo ]]` or `[[Foo | a ]]`.
		// Raw/Start/End preserve the original byte range for rewrite spans.
		rawTarget, rawHeading, escapedPipe := splitEscapedPipe(s, m)
		target := trimASCIISpace(rawTarget)
		var heading, alias string
		if m[4] != -1 {
			heading = trimASCIISpace(rawHeading)
		}
		if m[6] != -1 {
			alias = trimASCIISpace(s[m[6]:m[7]])
		}
		// An all-whitespace target is not a real link.
		if target == "" {
			continue
		}
		// A heading or alias that is all whitespace after trimming means the
		// separator (`#` or `|`) was present but the content was empty or
		// blank. These are not valid Obsidian links; more importantly,
		// FormatLink would silently drop the separator on rewrite, changing
		// the link form. Reject them so rewrites never alter link semantics.
		if m[4] != -1 && heading == "" {
			continue
		}
		if m[6] != -1 && alias == "" {
			continue
		}
		folder := ""
		if idx := strings.LastIndex(target, "/"); idx >= 0 {
			folder = target[:idx]
			// A target like "Folder/" has an empty title component after the
			// last slash. Obsidian does not allow empty note names, so reject.
			if idx == len(target)-1 {
				continue
			}
		}
		links = append(links, Link{
			Raw:         s[start:end],
			Target:      target,
			Folder:      folder,
			Heading:     heading,
			Alias:       alias,
			EscapedPipe: escapedPipe,
			Start:       start,
			End:         end,
		})
	}
	return links
}

// splitEscapedPipe returns the raw target and heading of match m, with the one
// backslash of an Obsidian table-escaped alias separator (`\|`) removed from
// whichever of them precedes the `|`. Without an alias there is no separator
// to escape, so a trailing backslash is left where it is.
func splitEscapedPipe(s string, m []int) (target, heading string, escaped bool) {
	target = s[m[2]:m[3]]
	if m[4] != -1 {
		heading = s[m[4]:m[5]]
	}
	if m[6] == -1 {
		return target, heading, false
	}
	if m[4] != -1 {
		if strings.HasSuffix(heading, `\`) {
			return target, heading[:len(heading)-1], true
		}
		return target, heading, false
	}
	if strings.HasSuffix(target, `\`) {
		return target[:len(target)-1], heading, true
	}
	return target, heading, false
}

// byteRange is a half-open [start, end) span in the source.
type byteRange struct {
	start, end int
}

// rangeOverlapsAny reports whether [start, end) overlaps any range in ranges.
func rangeOverlapsAny(start, end int, ranges []byteRange) bool {
	for _, r := range ranges {
		if start < r.end && end > r.start {
			return true
		}
	}
	return false
}

// buildCodeMask returns the byte ranges in s that should not be scanned for
// wikilinks: fenced code blocks, indented code blocks, and inline backtick
// spans.
func buildCodeMask(s string) []byteRange {
	var ranges []byteRange
	lines := splitLineSpans(s)

	var (
		fenceOpen      bool
		fenceChar      byte
		fenceLen       int
		fenceQuoted    bool // true when the opener line was inside a blockquote
		inIndentedCode bool
		prevBlank      = true // treat document start as following a blank line
		prevQuoteDepth int
		inlineStart    = -1
		inlineEnd      int
	)

	flushInline := func() {
		if inlineStart < 0 {
			return
		}
		for _, sp := range inlineCodeSpans(s[inlineStart:inlineEnd]) {
			ranges = append(ranges, byteRange{inlineStart + sp.start, inlineStart + sp.end})
		}
		inlineStart = -1
		inlineEnd = 0
	}

	for _, ln := range lines {
		text := s[ln.start:ln.end]
		strippedQuote, quoteDepth, quoted := stripBlockquotePrefixesDepth(text)
		inlineText := text
		if quoted {
			inlineText = strippedQuote
		}

		if fenceOpen {
			ranges = append(ranges, byteRange{ln.start, ln.end})
			if isFenceCloser(text, fenceChar, fenceLen, fenceQuoted) {
				fenceOpen = false
			}
			prevBlank = false
			prevQuoteDepth = quoteDepth
			continue
		}

		if ch, n, quoted, ok := isFenceOpener(text); ok {
			flushInline()
			ranges = append(ranges, byteRange{ln.start, ln.end})
			fenceOpen = true
			fenceChar = ch
			fenceLen = n
			fenceQuoted = quoted
			inIndentedCode = false
			prevBlank = false
			prevQuoteDepth = quoteDepth
			continue
		}

		if isBlankLine(text) || (quoted && isBlankLine(strippedQuote)) {
			flushInline()
			inIndentedCode = false
			prevBlank = true
			prevQuoteDepth = quoteDepth
			continue
		}

		quotedIndented := quoted && startsWithIndentedCodePlain(strippedQuote)
		quoteDepthChanged := quoteDepth != prevQuoteDepth
		startsQuotedBlock := quoted && quoteDepth > prevQuoteDepth
		if (startsWithIndentedCode(text) && (prevBlank || inIndentedCode)) ||
			(quotedIndented && (prevBlank || inIndentedCode || startsQuotedBlock)) {
			flushInline()
			ranges = append(ranges, byteRange{ln.start, ln.end})
			inIndentedCode = true
			prevBlank = false
			prevQuoteDepth = quoteDepth
			continue
		}

		if quoteDepthChanged {
			// An inline code span may continue across a soft line break inside the
			// same paragraph, but entering, leaving, or changing blockquote
			// nesting depth starts a new block. Flush any pending inline scan so
			// a stray backtick on the previous line cannot hide real wikilinks
			// in the new block context.
			flushInline()
		}
		startsBlock := startsNonParagraphBlock(inlineText)
		if startsBlock {
			// CommonMark only lets inline code spans continue across soft line
			// breaks inside the same paragraph. Starting a heading, list item,
			// or similar block closes the previous paragraph, so an unmatched
			// backtick above must not hide real wikilinks here.
			flushInline()
		}

		inIndentedCode = false
		if inlineStart < 0 {
			inlineStart = ln.start
		}
		inlineEnd = ln.end
		if isStandaloneInlineBlock(inlineText) {
			flushInline()
		}
		prevBlank = false
		prevQuoteDepth = quoteDepth
	}

	flushInline()
	return ranges
}

// splitLineSpans returns the [start, end) byte spans of each line in s. The
// trailing newline is not included in the span. Both `\n` and `\r\n` are
// handled (`\r` ends up as the last byte of the line's span and is harmless
// for the checks we perform).
func splitLineSpans(s string) []byteRange {
	if s == "" {
		return nil
	}
	var out []byteRange
	pos := 0
	for pos <= len(s) {
		nl := strings.IndexByte(s[pos:], '\n')
		if nl < 0 {
			if pos < len(s) {
				out = append(out, byteRange{pos, len(s)})
			}
			break
		}
		end := pos + nl
		// strip trailing \r so isBlankLine and fence checks see clean content
		trimEnd := end
		if trimEnd > pos && s[trimEnd-1] == '\r' {
			trimEnd--
		}
		out = append(out, byteRange{pos, trimEnd})
		pos = end + 1
	}
	return out
}

// isBlankLine reports whether the line contains only whitespace.
func isBlankLine(line string) bool {
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c != ' ' && c != '\t' {
			return false
		}
	}
	return true
}

// stripBlockquotePrefixes removes one or more leading CommonMark block quote
// markers so fence detection also works for quoted fenced code blocks.
func stripBlockquotePrefixes(line string) (string, bool) {
	stripped, _, ok := stripBlockquotePrefixesDepth(line)
	return stripped, ok
}

func stripBlockquotePrefixesDepth(line string) (string, int, bool) {
	stripped := false
	depth := 0
	for {
		i := 0
		for i < 4 && i < len(line) && line[i] == ' ' {
			i++
		}
		if i >= 4 || i >= len(line) || line[i] != '>' {
			return line, depth, stripped
		}
		i++
		if i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		line = line[i:]
		stripped = true
		depth++
	}
}

// trimASCIISpace strips ASCII spaces and tabs from both ends of s. We avoid
// strings.TrimSpace because it also strips newlines, but the wikilink regex
// already rejects `\n` and `\r` inside the target/heading/alias so there is
// nothing to gain from trimming those.
func trimASCIISpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

func skipBlockIndent(line string) int {
	i := 0
	for i < 3 && i < len(line) && line[i] == ' ' {
		i++
	}
	return i
}
