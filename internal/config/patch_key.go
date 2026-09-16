package config

import "strings"

// keySite locates one `[table]` and the one assignment of `key` inside it.
//
// Both may be absent, and the assignment may be a root-level dotted key
// (`autosave.min_interval = "5m"`) rather than a key inside the table — a
// spelling that must be found rather than ignored, because adding a second
// assignment for a key TOML already binds would make the file undecodable.
type keySite struct {
	// tableStart is the `[table]` header line, or -1 when the table is absent.
	tableStart int
	// tableEnd is the exclusive end of the table's block: the next table header,
	// or end of file.
	tableEnd int
	// assign is the first line of the assignment, or -1 when it is absent.
	assign int
	// assignEnd is the last line that assignment spans: assign itself for a value
	// written on one line, a later line for a multi-line string, array, or inline
	// table. Rewriting or removing the assignment covers the whole span, because
	// its continuation lines are the key's value and not text below it.
	assignEnd int
	// comment is the trailing comment following the value on assignEnd, '#'
	// included, or "" when the assignment carries none.
	comment string
	// dotted reports that assign is a root-level `table.key` assignment.
	dotted bool
}

// found reports whether the file assigns the key at all.
func (s keySite) found() bool { return s.assign >= 0 }

// findKeySite locates table and the single assignment of key within it.
//
// Table headers are compared by their *normalized* key path, so `[serve]` and
// `["serve"]` are the same table. Missing that equivalence would not merely skip
// an edit: the patchers built on this insert an assignment when they find none,
// and a second binding for a key TOML already binds is a file that stops
// loading.
func findKeySite(lines []string, table, key string) keySite {
	site := keySite{tableStart: -1, tableEnd: len(lines), assign: -1, assignEnd: -1}
	inTable, seenHeader := false, false
	for i := 0; i < len(lines); i++ {
		path, isArray, isHeader := parseTOMLTableHeader(lines[i])
		if isHeader {
			seenHeader = true
			inTable = site.openTable(i, path, table, isArray, inTable, len(lines))
			continue
		}
		if site.found() {
			continue
		}
		dotted, ok := assignsKey(lines[i], table, key, inTable, seenHeader)
		if !ok {
			continue
		}
		site.assign, site.dotted = i, dotted
		site.assignEnd, site.comment = assignmentValueSpan(lines, i)
		// The rest of a multi-line value is content, not structure: skipping it
		// keeps a bracketed line inside a string from reading as the table header
		// that would end this table.
		i = site.assignEnd
	}
	return site
}

// openTable records a table header at line i and reports whether the walk is
// now inside the table being looked for.
func (s *keySite) openTable(i int, path, table string, isArray, inTable bool, total int) bool {
	if inTable {
		s.tableEnd = i
	}
	// Only the first matching table is targeted. A second one is a duplicate
	// TOML already rejects, so patching it would be patching a file that cannot
	// load.
	if !isArray && s.tableStart < 0 && normalizedTableIs(path, table) {
		s.tableStart, s.tableEnd = i, total
		return true
	}
	return false
}

// assignsKey reports whether line binds `table.key`, and whether it does so as a
// root-level dotted assignment rather than as a key inside the table.
func assignsKey(line, table, key string, inTable, seenHeader bool) (dotted, ok bool) {
	switch {
	case inTable && matchesTOMLKeyPath(line, key):
		return false, true
	case !seenHeader && matchesTOMLKeyPath(line, table+"."+key):
		// A dotted key binds to the root table, so it only counts before the
		// first header; the same text under [cockpit] names another key.
		return true, true
	default:
		return false, false
	}
}

// normalizedTableIs reports whether a table header path names table, in any
// spelling a TOML key segment may take.
func normalizedTableIs(path, table string) bool {
	normalized, ok := normalizeTOMLKeyPath(path)
	return ok && normalized == table
}

// cutKeySite removes the whole assignment the site names, leaving the table
// header and every comment around it exactly where they were.
func cutKeySite(lines []string, site keySite) []string {
	if !site.found() {
		return lines
	}
	return append(append([]string(nil), lines[:site.assign]...), lines[site.assignEnd+1:]...)
}

// findTableBlock locates `[table]` and the exclusive end of its block. start is
// -1 when the table is absent.
func findTableBlock(lines []string, table string) (start, end int) {
	start, end = -1, len(lines)
	for i, line := range lines {
		path, isArray, isHeader := parseTOMLTableHeader(line)
		if !isHeader {
			continue
		}
		if start >= 0 {
			return start, i
		}
		if !isArray && normalizedTableIs(path, table) {
			start = i
		}
	}
	return start, end
}

// insertKeyAssignment places assignment — the verbatim lines of one key's
// assignment, already spelled with the bare key — inside `[table]`, creating the
// table at end of file when it is absent.
//
// It refuses nothing and validates nothing: the caller has already established
// that the file does not bind this key, because a second binding is a file that
// stops loading.
func insertKeyAssignment(lines []string, table string, assignment []string) []string {
	eol := configLineEnding(lines)
	adapted := make([]string, len(assignment))
	for i, line := range assignment {
		// The source file's line ending is not necessarily the destination's, and a
		// lone LF line in a CRLF file is the kind of drift a diff shows forever.
		adapted[i] = strings.TrimSuffix(line, "\r") + eol
	}

	start, end := findTableBlock(lines, table)
	if start >= 0 {
		// Append inside the existing table, ahead of the blank lines separating it
		// from the next section, so the table's own comments stay above the keys
		// they document.
		at := end - len(trailingBlankLines(lines[start:end]))
		out := append([]string(nil), lines[:at]...)
		out = append(out, adapted...)
		return append(out, lines[at:]...)
	}

	block := append([]string{"[" + table + "]" + eol}, adapted...)
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
		block = append([]string{eol}, block...)
	}
	return append(append([]string(nil), lines...), block...)
}

// rekeyAssignment respells the key an assignment line binds while leaving its
// value — and everything else on the line — byte-identical.
//
// It is what lets a root-level `serve.addr = "..."` move into `[serve]` as
// `addr = "..."` without the value ever being decoded and re-rendered. A value
// this file never parses is a value it cannot get wrong.
func rekeyAssignment(line, key string) string {
	body := strings.TrimSuffix(line, "\r")
	eq := strings.IndexByte(body, '=')
	if eq < 0 {
		return line
	}
	indent := body[:len(body)-len(strings.TrimLeft(body, " \t"))]
	return indent + key + " " + strings.TrimLeft(body[eq:], " \t") + strings.TrimPrefix(line, body)
}

// assignmentValueSpan reports the last line the assignment beginning at start
// spans, and the trailing comment that follows its value on that line.
//
// A TOML value begins on its key's own line but need not end there: a
// multi-line string, an array, or an inline table may run on. Every patcher
// built on a key site rewrites or removes whole assignments, so it needs the
// whole span — replacing only the first line of a multi-line value would leave
// the rest of that value behind as a body nothing can parse.
//
// A value this cannot follow to its end — an unterminated string, a bracket
// that never closes — reports the starting line and no comment. That is a body
// TOML already refuses; treating it as a single line is what this patcher did
// before spans existed, and it keeps a malformed file from having lines deleted
// on the strength of a guess about where its value stopped.
func assignmentValueSpan(lines []string, start int) (int, string) {
	body := strings.TrimSuffix(lines[start], "\r")
	eq := strings.IndexByte(body, '=')
	if eq < 0 {
		return start, ""
	}
	scan, from := valueScan{}, eq+1
	for line := start; line < len(lines); line++ {
		if line != start {
			body, from = strings.TrimSuffix(lines[line], "\r"), 0
		}
		comment, done := scan.scanLine(body, from)
		if done {
			return line, comment
		}
		if len(scan.quote) == 1 {
			// A single-line string cannot cross a newline, so the assignment is one
			// this cannot read rather than one that continues.
			return start, ""
		}
	}
	return start, ""
}

// valueScan is the state a TOML value carries from one line of an assignment to
// the next.
type valueScan struct {
	// quote is the closing delimiter of the string being read (`"`, `'`, `"""`
	// or `'''`), or "" outside one.
	quote string
	// depth counts open brackets and braces, which are the other way a value
	// continues onto the next line.
	depth int
}

// scanLine advances the scan across one line of a value, starting at from. It
// reports the trailing comment it found and whether the value ended here.
func (s *valueScan) scanLine(body string, from int) (comment string, done bool) {
	for i := from; i < len(body); {
		if s.quote == "" && s.depth == 0 && body[i] == '#' {
			// Outside a string and outside any bracket, a '#' ends the value:
			// what follows is the assignment's trailing comment.
			return strings.TrimSpace(body[i:]), true
		}
		i = s.advance(body, i)
	}
	return "", s.quote == "" && s.depth <= 0
}

// advance moves one step through a value and returns the next position to read.
func (s *valueScan) advance(body string, i int) int {
	if s.quote != "" {
		return s.scanQuoted(body, i)
	}
	if open := openingQuote(body[i:]); open != "" {
		s.quote = open
		return i + len(open)
	}
	switch body[i] {
	case '#':
		// Only reachable inside a bracketed value, where a comment runs to end
		// of line and the value continues below it.
		return len(body)
	case '[', '{':
		s.depth++
	case ']', '}':
		s.depth--
	}
	return i + 1
}

// openingQuote reports the string delimiter rest begins with, longest first so
// `"""` is never read as an empty `""` followed by a third quote, or "" when
// rest begins no string at all.
func openingQuote(rest string) string {
	for _, delim := range []string{`"""`, `'''`, `"`, `'`} {
		if strings.HasPrefix(rest, delim) {
			return delim
		}
	}
	return ""
}

// scanQuoted advances one step inside a string literal and returns the next
// position to read.
func (s *valueScan) scanQuoted(body string, i int) int {
	switch {
	case s.quote[0] == '"' && body[i] == '\\':
		// An escape, including the backslash that continues a """ string onto the
		// next line. Literal strings have none.
		return i + 2
	case strings.HasPrefix(body[i:], s.quote):
		next := i + len(s.quote)
		s.quote = ""
		return next
	default:
		return i + 1
	}
}

// matchesTOMLKeyPath reports whether line assigns exactly the dotted key path
// want. Every segment spelling a valid key can take — bare, "quoted", or
// 'literal' — is accepted, so a patch cannot miss an assignment and then add a
// duplicate of it.
func matchesTOMLKeyPath(line, want string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return false
	}
	eq := strings.IndexByte(trimmed, '=')
	if eq < 0 {
		return false
	}
	got, ok := normalizeTOMLKeyPath(trimmed[:eq])
	return ok && got == want
}

func normalizeTOMLKeyPath(raw string) (string, bool) {
	segments := strings.Split(raw, ".")
	out := make([]string, len(segments))
	for i, segment := range segments {
		segment = strings.TrimSpace(segment)
		if len(segment) >= 2 && segment[0] == '\'' && segment[len(segment)-1] == '\'' {
			out[i] = segment[1 : len(segment)-1]
			continue
		}
		name, ok := unquoteTOMLKey(segment)
		if !ok {
			return "", false
		}
		out[i] = name
	}
	return strings.Join(out, "."), true
}

// configLineEnding reports the carriage return a line added to this file needs.
// splitConfigLines splits on "\n" alone, so a CRLF file's lines each keep their
// "\r" and a line inserted without one would be the only LF line in the file.
func configLineEnding(lines []string) string {
	for _, line := range lines {
		if strings.HasSuffix(line, "\r") {
			return "\r"
		}
	}
	return ""
}
