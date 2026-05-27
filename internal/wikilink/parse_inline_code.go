package wikilink

// inlineCodeSpans returns the spans (relative to s) covered by inline code.
// A backtick run of length N is closed by the next run of exactly N backticks
// per CommonMark. Unmatched openings are ignored — they aren't code spans.
func inlineCodeSpans(s string) []byteRange {
	var spans []byteRange
	i := 0
	for i < len(s) {
		if s[i] != '`' {
			i++
			continue
		}
		start := i
		n := 0
		for i < len(s) && s[i] == '`' {
			n++
			i++
		}
		j := i
		for j < len(s) {
			if s[j] != '`' {
				j++
				continue
			}
			m := 0
			for j < len(s) && s[j] == '`' {
				m++
				j++
			}
			if m == n {
				spans = append(spans, byteRange{start, j})
				i = j
				break
			}
		}
	}
	return spans
}
