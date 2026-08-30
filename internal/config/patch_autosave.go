package config

import (
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
)

// autosaveTablePath is the TOML table the autosave floor lives under, and
// autosaveMinIntervalKey the single key inside it this file may touch.
const (
	autosaveTablePath      = "autosave"
	autosaveMinIntervalKey = "min_interval"
)

// PatchAutosaveMinInterval sets [autosave].min_interval to the canonical
// spelling of value, preserving every unrelated byte.
//
// Unlike the property and view patchers this one never rewrites a block: it
// rewrites (or adds) exactly one assignment, so comments around it — and the
// inline comment on it — survive. That assignment is a single line except when
// the value it replaces spanned several, in which case all of them are the
// assignment and all of them go.
//
// The unit of change is the *policy*, not the text. A value naming the policy
// already in force is a no-op whatever spelling the file uses and whether or
// not the key is assigned at all: "5m" over a stored "300s", "5m" over a stored
// """5m""", and "5m" over a file that assigns nothing and so is already on the
// 5m default, all leave the bytes alone and report no change. That is what makes
// a script reasserting a vault's policy on every run produce history only when
// the policy moves. A policy that does move is still written in the canonical
// spelling.
//
// The value is validated by ParseAutosaveMinInterval, so a patch can never put
// a body on disk that Decode would then refuse to load. An empty value is
// rejected rather than silently written as the default: a patch must say what
// it writes, and "unset it" is UnsetAutosaveMinInterval.
func PatchAutosaveMinInterval(source []byte, value string) (PatchResult, error) {
	if strings.TrimSpace(value) == "" {
		return PatchResult{}, fmt.Errorf(
			"config: %s.%s requires a value", autosaveTablePath, autosaveMinIntervalKey)
	}
	policy, err := ParseAutosaveMinInterval(value)
	if err != nil {
		return PatchResult{}, err
	}

	lines, trailingNewline := splitConfigLines(source)
	if current, ok := effectiveAutosavePolicy(source); ok && current.Canonical == policy.Canonical {
		// Nothing to say: the file already means what the caller asked for, so no
		// candidate is rendered and no diff is reported.
		return autosavePatchResult(source, lines, lines, trailingNewline), nil
	}
	// An empty file has no final-newline state to preserve, so the section this
	// call creates is written the way every other config file ends.
	if len(lines) == 0 {
		trailingNewline = true
	}
	return autosavePatchResult(
		source, lines, setAutosaveMinInterval(lines, policy.Canonical), trailingNewline), nil
}

// effectiveAutosavePolicy resolves the policy a config body is already under:
// the value its min_interval assignment binds, or the default when the key is
// absent — the same two cases (*Config).AutosavePolicy answers from a loaded
// config, reached here from bytes a patch has not been applied to yet.
//
// The value is read by the decoder config loading itself uses, not by a scan of
// the assignment's own bytes, so every TOML spelling that reaches the loader as
// one string reaches this comparison as one policy: `"5m"`, `'5m'`, `"""5m"""`,
// and `"5m"` all name the 5m floor, and none of them is rewritten by a set
// that asks for it. Comparing bytes instead would make a no-op depend on which
// legal encoding the file happened to choose.
//
// ok is false when the body does not decode at all, or when the assignment
// binds something that is not a policy — a number, an array, a duration outside
// the bounds. Each of those is a body Decode already refuses, so there is no
// policy in force to compare a request against, and the caller rewrites the
// assignment rather than declining against an answer it does not have.
func effectiveAutosavePolicy(source []byte) (AutosavePolicy, bool) {
	var body map[string]any
	if _, err := toml.Decode(string(source), &body); err != nil {
		return AutosavePolicy{}, false
	}
	value, assigned := body[autosaveTablePath]
	if assigned {
		table, isTable := value.(map[string]any)
		if !isTable {
			return AutosavePolicy{}, false
		}
		value, assigned = table[autosaveMinIntervalKey]
	}
	if !assigned {
		// ParseAutosaveMinInterval reads an empty value as the default, which is a
		// policy like any other: assigning the default explicitly over an absent
		// key changes what the file says and not what the vault does.
		policy, _ := ParseAutosaveMinInterval("")
		return policy, true
	}
	raw, isString := value.(string)
	if !isString {
		return AutosavePolicy{}, false
	}
	policy, err := ParseAutosaveMinInterval(raw)
	if err != nil {
		return AutosavePolicy{}, false
	}
	return policy, true
}

// UnsetAutosaveMinInterval removes the autosave.min_interval assignment,
// restoring the default floor. The [autosave] table and every comment around
// the removed line stay exactly where they were, so unsetting and setting again
// leaves the file as the user wrote it. Removing a key that is not there is a
// no-op with no diff, not an error — the resulting configuration is the one the
// caller asked for either way.
//
// What is removed is the whole assignment, which is more than one line when its
// value is: a multi-line string's remaining lines are part of the key being
// unset, not text below it.
func UnsetAutosaveMinInterval(source []byte) PatchResult {
	lines, trailingNewline := splitConfigLines(source)
	out := lines
	if site := findAutosaveSite(lines); site.assign >= 0 {
		out = append(append([]string(nil), lines[:site.assign]...), lines[site.assignEnd+1:]...)
	}
	return autosavePatchResult(source, lines, out, trailingNewline)
}

func autosavePatchResult(source []byte, before, after []string, trailingNewline bool) PatchResult {
	patched := joinConfigLines(after, trailingNewline)
	result := PatchResult{
		Patched: patched,
		Changed: string(patched) != string(source),
	}
	if result.Changed {
		result.Diff = diffLines(before, after)
	}
	return result
}

func setAutosaveMinInterval(lines []string, canonical string) []string {
	site := findAutosaveSite(lines)
	if site.assign >= 0 {
		key := autosaveMinIntervalKey
		if site.dotted {
			key = autosaveTablePath + "." + autosaveMinIntervalKey
		}
		// The canonical value is one line whatever the value it replaces spanned,
		// so a multi-line string collapses to a single assignment rather than
		// leaving its continuation lines behind.
		out := append([]string(nil), lines[:site.assign]...)
		out = append(out, rewriteAssignmentLine(lines[site.assign], key, canonical, site.comment))
		return append(out, lines[site.assignEnd+1:]...)
	}

	eol := configLineEnding(lines)
	assignment := autosaveMinIntervalKey + " = " + quoteTOMLString(canonical) + eol
	if site.tableStart >= 0 {
		// Append inside the existing table, ahead of the blank lines separating
		// it from the next section, so the table's own comments stay above the
		// key they document.
		at := site.tableEnd - len(trailingBlankLines(lines[site.tableStart:site.tableEnd]))
		out := append([]string(nil), lines[:at]...)
		out = append(out, assignment)
		return append(out, lines[at:]...)
	}

	// No [autosave] table: append one at end of file, which is where
	// DefaultTemplate puts it. Any other position would have to guess which of
	// the user's sections it may split.
	block := []string{"[" + autosaveTablePath + "]" + eol, assignment}
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
		block = append([]string{eol}, block...)
	}
	return append(append([]string(nil), lines...), block...)
}

// autosaveSite locates the [autosave] table and the one line assigning
// min_interval. Both may be absent, and the assignment may be a root-level
// dotted key (`autosave.min_interval = "5m"`) rather than a key inside the
// table — a spelling that must be found rather than ignored, because adding a
// second assignment for a key TOML already binds would make the file
// undecodable.
type autosaveSite struct {
	// tableStart is the [autosave] header line, or -1 when the table is absent.
	tableStart int
	// tableEnd is the exclusive end of the table's block: the next table header,
	// or end of file.
	tableEnd int
	// assign is the first line of the min_interval assignment, or -1 when it is
	// absent.
	assign int
	// assignEnd is the last line that assignment spans: assign itself for a value
	// written on one line, a later line for a multi-line string, array, or inline
	// table. Rewriting or removing the assignment covers the whole span, because
	// its continuation lines are the key's value and not text below it.
	assignEnd int
	// comment is the trailing comment following the value on assignEnd, '#'
	// included, or "" when the assignment carries none.
	comment string
	// dotted reports that assign is a root-level `autosave.min_interval` key.
	dotted bool
}

func findAutosaveSite(lines []string) autosaveSite {
	site := autosaveSite{tableStart: -1, tableEnd: len(lines), assign: -1, assignEnd: -1}
	inTable, seenHeader := false, false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		path, isArray, isHeader := parseTOMLTableHeader(line)
		if isHeader {
			seenHeader = true
			if inTable {
				site.tableEnd = i
				inTable = false
			}
			// Only the first [autosave] table is targeted. A second one is a
			// duplicate TOML already rejects, so patching it would be patching a
			// file that cannot load.
			if !isArray && path == autosaveTablePath && site.tableStart < 0 {
				site.tableStart, site.tableEnd, inTable = i, len(lines), true
			}
			continue
		}
		if site.assign >= 0 {
			continue
		}
		switch {
		case inTable && matchesTOMLKeyPath(line, autosaveMinIntervalKey):
			site.assign = i
		case !seenHeader && matchesTOMLKeyPath(line, autosaveTablePath+"."+autosaveMinIntervalKey):
			// A dotted key binds to the root table, so it only counts before the
			// first header; the same text under [cockpit] names another key.
			site.assign, site.dotted = i, true
		default:
			continue
		}
		site.assignEnd, site.comment = assignmentValueSpan(lines, i)
		// The rest of a multi-line value is content, not structure: skipping it
		// keeps a bracketed line inside a string from reading as the table header
		// that would end this table.
		i = site.assignEnd
	}
	return site
}

// assignmentValueSpan reports the last line the assignment beginning at start
// spans, and the trailing comment that follows its value on that line.
//
// A TOML value begins on its key's own line but need not end there: a
// multi-line string, an array, or an inline table may run on. This patcher
// rewrites and removes whole assignments, so it needs the whole span —
// replacing only the first line of a multi-line value would leave the rest of
// that value behind as a body nothing can parse.
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
	// quote is the closing delimiter of the string being read (`"`, `'`, `"""`
	// or `'''`) or "" outside one; depth counts open brackets and braces, which
	// are the other way a value continues onto the next line.
	quote, depth, i := "", 0, eq+1
	for line := start; line < len(lines); line++ {
		if line != start {
			body, i = strings.TrimSuffix(lines[line], "\r"), 0
		}
		for i < len(body) {
			rest := body[i:]
			switch {
			case quote != "":
				switch {
				case quote[0] == '"' && body[i] == '\\':
					// An escape, including the backslash that continues a """ string
					// onto the next line. Literal strings have none.
					i += 2
				case strings.HasPrefix(rest, quote):
					i += len(quote)
					quote = ""
				default:
					i++
				}
			case body[i] == '#':
				if depth == 0 {
					return line, strings.TrimSpace(rest)
				}
				// A comment inside a multi-line array runs to end of line and the
				// value continues below it.
				i = len(body)
			case strings.HasPrefix(rest, `"""`), strings.HasPrefix(rest, `'''`):
				quote, i = rest[:3], i+3
			case body[i] == '"' || body[i] == '\'':
				quote, i = rest[:1], i+1
			case body[i] == '[' || body[i] == '{':
				depth, i = depth+1, i+1
			case body[i] == ']' || body[i] == '}':
				depth, i = depth-1, i+1
			default:
				i++
			}
		}
		switch {
		case quote == "" && depth <= 0:
			return line, ""
		case len(quote) == 1:
			// A single-line string cannot cross a newline, so the assignment is one
			// this cannot read rather than one that continues.
			return start, ""
		}
	}
	return start, ""
}

// rewriteAssignmentLine replaces an assignment's value while keeping the line's
// indentation, its trailing comment, and its CR — the line is re-rendered, but
// everything on it that was not the value survives. The comment is the one
// assignmentValueSpan found after the value, which for a multi-line value is on
// the last line of the span rather than on this one.
func rewriteAssignmentLine(line, key, value, comment string) string {
	body := strings.TrimSuffix(line, "\r")
	indent := body[:len(body)-len(strings.TrimLeft(body, " \t"))]
	rendered := indent + key + " = " + quoteTOMLString(value)
	if comment != "" {
		rendered += " " + comment
	}
	return rendered + strings.TrimPrefix(line, body)
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
