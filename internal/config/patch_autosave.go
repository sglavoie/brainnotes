package config

import (
	"fmt"
	"strings"
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
// rewrites (or adds) exactly one assignment line, so comments around it — and
// the inline comment on the line itself — survive.
//
// The unit of change is the *policy*, not the text. A value naming the policy
// already in force is a no-op whatever spelling the file uses and whether or
// not the key is assigned at all: "5m" over a stored "300s", and "5m" over a
// file that assigns nothing and so is already on the 5m default, both leave the
// bytes alone and report no change. That is what makes a script reasserting a
// vault's policy on every run produce history only when the policy moves. A
// policy that does move is still written in the canonical spelling.
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
	if current, ok := effectiveAutosavePolicy(lines); ok && current.Canonical == policy.Canonical {
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
// ok is false when the assignment is present but its value cannot be read as a
// policy. That is a body Decode would refuse to load, so there is no policy in
// force to compare a request against, and the caller rewrites the line rather
// than declining against an answer it does not have.
func effectiveAutosavePolicy(lines []string) (AutosavePolicy, bool) {
	site := findAutosaveSite(lines)
	if site.assign < 0 {
		// ParseAutosaveMinInterval reads an empty value as the default, which is a
		// policy like any other: assigning the default explicitly over an absent
		// key changes what the file says and not what the vault does.
		policy, _ := ParseAutosaveMinInterval("")
		return policy, true
	}
	raw, ok := assignmentStringValue(lines[site.assign])
	if !ok {
		return AutosavePolicy{}, false
	}
	policy, err := ParseAutosaveMinInterval(raw)
	if err != nil {
		return AutosavePolicy{}, false
	}
	return policy, true
}

// assignmentStringValue reads the string an assignment line binds.
//
// It is deliberately narrow: a basic or literal string with no escape, followed
// by nothing but a comment, and nothing else. A multi-line string, a number, an
// array, or an escaped character all report false, because the only caller uses
// this to decline a rewrite and declining wrongly is the one outcome that has a
// cost. A value naming an autosave policy never needs an escape.
func assignmentStringValue(line string) (string, bool) {
	body := strings.TrimSuffix(line, "\r")
	eq := strings.IndexByte(body, '=')
	if eq < 0 {
		return "", false
	}
	rest := strings.TrimSpace(body[eq+1:])
	if len(rest) < 2 || (rest[0] != '"' && rest[0] != '\'') {
		return "", false
	}
	// A closing quote at offset 0 is an empty string, which is also how the
	// opening delimiter of a """ or ''' multi-line string reads from here;
	// neither is a policy.
	end := strings.IndexByte(rest[1:], rest[0])
	if end < 1 {
		return "", false
	}
	value := rest[1 : 1+end]
	if strings.ContainsRune(value, '\\') {
		return "", false
	}
	if after := strings.TrimSpace(rest[end+2:]); after != "" && !strings.HasPrefix(after, "#") {
		return "", false
	}
	return value, true
}

// UnsetAutosaveMinInterval removes the autosave.min_interval assignment,
// restoring the default floor. The [autosave] table and every comment around
// the removed line stay exactly where they were, so unsetting and setting again
// leaves the file as the user wrote it. Removing a key that is not there is a
// no-op with no diff, not an error — the resulting configuration is the one the
// caller asked for either way.
func UnsetAutosaveMinInterval(source []byte) PatchResult {
	lines, trailingNewline := splitConfigLines(source)
	out := lines
	if site := findAutosaveSite(lines); site.assign >= 0 {
		out = append(append([]string(nil), lines[:site.assign]...), lines[site.assign+1:]...)
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
		out := append([]string(nil), lines...)
		out[site.assign] = rewriteAssignmentLine(lines[site.assign], key, canonical)
		return out
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
	// assign is the min_interval assignment line, or -1 when it is absent.
	assign int
	// dotted reports that assign is a root-level `autosave.min_interval` key.
	dotted bool
}

func findAutosaveSite(lines []string) autosaveSite {
	site := autosaveSite{tableStart: -1, tableEnd: len(lines), assign: -1}
	inTable, seenHeader := false, false
	for i, line := range lines {
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
		}
	}
	return site
}

// rewriteAssignmentLine replaces an assignment's value while keeping the line's
// indentation, its trailing comment, and its CR — the line is re-rendered, but
// everything on it that was not the value survives.
func rewriteAssignmentLine(line, key, value string) string {
	body := strings.TrimSuffix(line, "\r")
	indent := body[:len(body)-len(strings.TrimLeft(body, " \t"))]
	rendered := indent + key + " = " + quoteTOMLString(value)
	if comment := assignmentComment(body); comment != "" {
		rendered += " " + comment
	}
	return rendered + strings.TrimPrefix(line, body)
}

// assignmentComment returns the trailing comment of an assignment line,
// including its '#', or "" when there is none. It tracks quoting so a '#'
// inside the value is not mistaken for the start of a comment.
func assignmentComment(line string) string {
	eq := strings.IndexByte(line, '=')
	if eq < 0 {
		return ""
	}
	var quote byte
	for i := eq + 1; i < len(line); i++ {
		switch c := line[i]; {
		case quote == '"' && c == '\\':
			i++
		case quote != 0 && c == quote:
			quote = 0
		case quote == 0 && (c == '"' || c == '\''):
			quote = c
		case quote == 0 && c == '#':
			return strings.TrimSpace(line[i:])
		}
	}
	return ""
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
