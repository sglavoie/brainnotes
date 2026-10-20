package wikilink

// FenceScanner follows fenced code blocks one line at a time with the CommonMark
// rules the wikilink scanner itself applies: an opener is three or more
// backticks or tildes behind at most three spaces, and only a run of the same
// character, at least as long, with nothing but whitespace after it closes the
// block. A toggle on any fence-shaped line gets this wrong in both directions —
// a "~~~" inside a backtick block, or a shorter run inside a longer one, would
// end the block early and expose its contents as Markdown.
//
// It deliberately follows unquoted fences only. Line scanners that use it look
// for unquoted structure (a heading, a list item), and a lazy continuation line
// cannot continue a fenced block opened inside a blockquote, so treating such a
// fence as open past its quote would hide real structure.
//
// The zero value is outside any fence.
type FenceScanner struct {
	open bool
	ch   byte
	n    int
}

// Line advances the scanner over line and reports whether line belongs to a
// fenced code block: its opener, its content, or its closer. A trailing
// carriage return is tolerated.
func (f *FenceScanner) Line(line string) bool {
	if f.open {
		if isFenceCloserPlain(line, f.ch, f.n) {
			f.open = false
		}
		return true
	}
	if ch, n, ok := isFenceOpenerPlain(line); ok {
		f.open, f.ch, f.n = true, ch, n
		return true
	}
	return false
}
