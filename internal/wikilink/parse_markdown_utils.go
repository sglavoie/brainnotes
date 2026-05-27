package wikilink

import "strings"

// startsWithIndentedCode reports whether the line begins with 4+ spaces or a
// leading tab — the CommonMark indented-code-block trigger.
func startsWithIndentedCode(line string) bool {
	return startsWithIndentedCodePlain(line)
}

// startsWithIndentedCodePlain reports whether line begins with 4+ spaces or a
// leading tab — the CommonMark indented-code-block trigger.
func startsWithIndentedCodePlain(line string) bool {
	if len(line) == 0 {
		return false
	}
	if line[0] == '\t' {
		return true
	}
	if len(line) >= 4 && line[0] == ' ' && line[1] == ' ' && line[2] == ' ' && line[3] == ' ' {
		return true
	}
	return false
}

// isFenceOpener reports whether line opens a fenced code block. It returns the
// fence character (" ` " or `~`), its length, a flag indicating whether the
// opener appeared inside a blockquote, and true. Up to 3 leading spaces of
// indentation are tolerated per CommonMark.
func isFenceOpener(line string) (byte, int, bool, bool) {
	if ch, n, ok := isFenceOpenerPlain(line); ok {
		return ch, n, false, true
	}
	if stripped, ok := stripBlockquotePrefixes(line); ok {
		ch, n, ok2 := isFenceOpenerPlain(stripped)
		return ch, n, ok2, ok2
	}
	return 0, 0, false, false
}

func isFenceOpenerPlain(line string) (byte, int, bool) {
	i := 0
	for i < 4 && i < len(line) && line[i] == ' ' {
		i++
	}
	if i >= 4 || i >= len(line) {
		return 0, 0, false
	}
	ch := line[i]
	if ch != '`' && ch != '~' {
		return 0, 0, false
	}
	n := 0
	for i+n < len(line) && line[i+n] == ch {
		n++
	}
	if n < 3 {
		return 0, 0, false
	}
	// Per CommonMark, backtick fence info strings may not contain backtick
	// characters. Tilde fences have no such restriction.
	if ch == '`' && strings.IndexByte(line[i+n:], '`') >= 0 {
		return 0, 0, false
	}
	return ch, n, true
}

// isFenceCloser reports whether line closes a fenced block opened with ch
// repeated openLen times. A closer may not have a trailing info string — only
// whitespace after the fence run. When openerQuoted is true the opener appeared
// inside a blockquote, in which case either a plain or blockquote-prefixed
// closer is accepted (lazy continuation / block-quote termination is treated
// conservatively). When openerQuoted is false the opener was unquoted, and
// only a plain closer is accepted — a blockquote-prefixed `> ```` does not
// terminate a fence opened outside the quote per CommonMark.
func isFenceCloser(line string, ch byte, openLen int, openerQuoted bool) bool {
	if isFenceCloserPlain(line, ch, openLen) {
		return true
	}
	if !openerQuoted {
		return false
	}
	if stripped, ok := stripBlockquotePrefixes(line); ok {
		return isFenceCloserPlain(stripped, ch, openLen)
	}
	return false
}

func isFenceCloserPlain(line string, ch byte, openLen int) bool {
	i := 0
	for i < 4 && i < len(line) && line[i] == ' ' {
		i++
	}
	if i >= 4 || i >= len(line) || line[i] != ch {
		return false
	}
	n := 0
	for i+n < len(line) && line[i+n] == ch {
		n++
	}
	if n < openLen {
		return false
	}
	rest := line[i+n:]
	return strings.TrimSpace(rest) == ""
}

// startsNonParagraphBlock reports whether line starts a block that interrupts
// paragraph continuation for inline parsing. This keeps inline-code masking from
// leaking from one markdown block into the next.
func startsNonParagraphBlock(line string) bool {
	return isATXHeading(line) || isListItem(line) || isStandaloneInlineBlock(line)
}

// isStandaloneInlineBlock reports whether line is a standalone block whose
// inline parsing must not carry to the next line.
func isStandaloneInlineBlock(line string) bool {
	return isATXHeading(line) || isRuleOrSetextUnderline(line)
}

func isATXHeading(line string) bool {
	i := skipBlockIndent(line)
	if i >= len(line) || line[i] != '#' {
		return false
	}
	n := 0
	for i+n < len(line) && line[i+n] == '#' {
		n++
	}
	if n == 0 || n > 6 {
		return false
	}
	j := i + n
	return j == len(line) || line[j] == ' ' || line[j] == '\t'
}

func isListItem(line string) bool {
	i := skipBlockIndent(line)
	if i >= len(line) {
		return false
	}
	switch line[i] {
	case '-', '+', '*':
		j := i + 1
		return j == len(line) || line[j] == ' ' || line[j] == '\t'
	}
	j := i
	for j < len(line) && line[j] >= '0' && line[j] <= '9' {
		j++
	}
	if j == i || j-i > 9 || j >= len(line) {
		return false
	}
	if line[j] != '.' && line[j] != ')' {
		return false
	}
	j++
	return j == len(line) || line[j] == ' ' || line[j] == '\t'
}

func isRuleOrSetextUnderline(line string) bool {
	i := skipBlockIndent(line)
	if i >= len(line) {
		return false
	}
	ch := line[i]
	if ch != '-' && ch != '*' && ch != '_' && ch != '=' {
		return false
	}
	count := 0
	for ; i < len(line); i++ {
		switch line[i] {
		case ch:
			count++
		case ' ', '\t':
		default:
			return false
		}
	}
	return count >= 3
}
