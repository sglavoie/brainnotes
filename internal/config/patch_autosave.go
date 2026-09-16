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
	out := cutKeySite(lines, findAutosaveSite(lines))
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
	if site.found() {
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

	// No [autosave] table: append one at end of file, which is where
	// DefaultTemplate puts it. Any other position would have to guess which of
	// the user's sections it may split.
	eol := configLineEnding(lines)
	assignment := autosaveMinIntervalKey + " = " + quoteTOMLString(canonical) + eol
	return insertKeyAssignment(lines, autosaveTablePath, []string{assignment})
}

// findAutosaveSite locates the [autosave] table and the one line assigning
// min_interval, through the same key-site walk every other whole-assignment
// patch uses.
func findAutosaveSite(lines []string) keySite {
	return findKeySite(lines, autosaveTablePath, autosaveMinIntervalKey)
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
