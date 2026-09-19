package config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// autosaveSettingKey is the one editable setting that already had a patcher of
// its own before this one existed. It keeps it: PatchAutosaveMinInterval
// canonicalizes the duration and treats any spelling of the policy already in
// force as a no-op, which no generic value comparison can reproduce, and losing
// that would make a script reasserting a vault's policy write history on every
// run.
const autosaveSettingKey = "autosave.min_interval"

// PatchSetting binds key to value in a portable configuration body, preserving
// every unrelated byte.
//
// Like the autosave patcher it rewrites (or inserts) exactly one assignment, so
// the comments around it — and the inline comment on it — survive. The
// assignment is one line whatever the value it replaces spanned: a multi-line
// array collapses rather than leaving its continuation lines behind as a body
// nothing can parse.
//
// A request naming the value the file already assigns is a no-op with no diff.
// A request naming the *default* for a key the file does not assign is not:
// writing the default down changes what the file says, and an editing surface
// that silently declined to record a deliberate choice would be lying about
// what it saved. Only autosave.min_interval differs, because its own patcher
// decided otherwise for its own reasons.
//
// The patched body is decoded before it is returned, so this can never hand back
// bytes the loader would refuse, and a source that does not decode at all is
// refused up front rather than patched blind.
func PatchSetting(source []byte, key string, value SettingValue) (PatchResult, error) {
	spec, err := checkSettingRequest(key, value)
	if err != nil {
		return PatchResult{}, err
	}
	if key == autosaveSettingKey {
		return PatchAutosaveMinInterval(source, value.Text)
	}
	rendered, err := renderSettingValue(value)
	if err != nil {
		return PatchResult{}, err
	}
	current, assigned, err := readSetting(source, spec)
	if err != nil {
		return PatchResult{}, err
	}

	lines, trailingNewline := splitConfigLines(source)
	if assigned && settingValuesEqual(current, value) {
		return settingPatchResult(source, lines, lines, trailingNewline), nil
	}
	// An empty file has no final-newline state to preserve, so the assignment
	// this call creates is written the way every other config file ends.
	if len(lines) == 0 {
		trailingNewline = true
	}
	result := settingPatchResult(source, lines, setSettingAssignment(lines, spec, rendered), trailingNewline)
	if err := checkSettingPatched(result.Patched, spec, &value); err != nil {
		return PatchResult{}, err
	}
	return result, nil
}

// UnsetSetting removes key's assignment, restoring whatever the key defaults to.
// The table and every comment around the removed line stay where they were, so
// unsetting and setting again leaves the file as the user wrote it. Removing a
// key that is not assigned is a no-op with no diff rather than an error: the
// resulting configuration is the one the caller asked for either way.
func UnsetSetting(source []byte, key string) (PatchResult, error) {
	spec, ok := SettingSpecFor(key)
	if !ok {
		return PatchResult{}, unknownSettingError(key)
	}
	if key == autosaveSettingKey {
		return UnsetAutosaveMinInterval(source), nil
	}
	if _, _, err := readSetting(source, spec); err != nil {
		return PatchResult{}, err
	}

	lines, trailingNewline := splitConfigLines(source)
	result := settingPatchResult(source, lines, cutKeySite(lines, findSettingSite(lines, spec)), trailingNewline)
	if err := checkSettingPatched(result.Patched, spec, nil); err != nil {
		return PatchResult{}, err
	}
	return result, nil
}

// checkSettingRequest resolves the key and refuses a value whose kind is not the
// one the key holds. A count arriving as a quoted string would otherwise be
// written as one, and the file would stop loading on the next read rather than
// on this write.
func checkSettingRequest(key string, value SettingValue) (SettingSpec, error) {
	spec, ok := SettingSpecFor(key)
	if !ok {
		return SettingSpec{}, unknownSettingError(key)
	}
	if value.Kind != spec.Kind {
		return SettingSpec{}, fmt.Errorf(
			"config: %s holds a %s value, not a %s", key, spec.Kind, value.Kind)
	}
	return spec, nil
}

func settingPatchResult(source []byte, before, after []string, trailingNewline bool) PatchResult {
	patched := joinConfigLines(after, trailingNewline)
	result := PatchResult{Patched: patched, Changed: string(patched) != string(source)}
	if result.Changed {
		result.Diff = diffLines(before, after)
	}
	return result
}

// renderSettingValue writes one value as the TOML it will be stored as. Every
// string goes through quoteTOMLString, which escapes control characters, so no
// value a caller supplies can introduce a newline into the file and turn one
// assignment into two.
func renderSettingValue(value SettingValue) (string, error) {
	switch value.Kind {
	case SettingString:
		return quoteTOMLString(value.Text), nil
	case SettingBool:
		return strconv.FormatBool(value.Flag), nil
	case SettingInt:
		return strconv.FormatInt(value.Number, 10), nil
	case SettingStringList:
		items := make([]string, 0, len(value.Items))
		for _, item := range value.Items {
			items = append(items, quoteTOMLString(item))
		}
		return "[" + strings.Join(items, ", ") + "]", nil
	}
	return "", fmt.Errorf("config: unsupported setting kind %q", string(value.Kind))
}

func settingValuesEqual(a, b SettingValue) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case SettingString:
		return a.Text == b.Text
	case SettingBool:
		return a.Flag == b.Flag
	case SettingInt:
		return a.Number == b.Number
	case SettingStringList:
		if len(a.Items) != len(b.Items) {
			return false
		}
		for i := range a.Items {
			if a.Items[i] != b.Items[i] {
				return false
			}
		}
		return true
	}
	return false
}

// readSetting reports the value a body currently assigns to the key, through the
// same decoder the loader uses rather than a scan of the assignment's bytes: a
// no-op must not depend on which legal spelling the file happened to choose.
//
// A body that does not decode is an error rather than an absent value, and it is
// deliberately the first thing every entry point checks. Treating a malformed
// file as "the key is unassigned" would have the patcher append an assignment to
// a file that already cannot load, leaving two problems where there was one and
// a diff that looks like a successful edit.
func readSetting(source []byte, spec SettingSpec) (SettingValue, bool, error) {
	var body map[string]any
	if _, err := toml.Decode(string(source), &body); err != nil {
		return SettingValue{}, false, fmt.Errorf(
			"config: %s is not valid TOML, so %s cannot be edited until it is repaired: %w",
			FileName, spec.Key, err)
	}
	raw, assigned := lookupSettingRaw(body, spec)
	if !assigned {
		return SettingValue{}, false, nil
	}
	value, ok := settingValueFromRaw(spec, raw)
	// A value of the wrong type is a body Decode already refuses, so there is no
	// value in force to compare a request against and the assignment is rewritten
	// rather than declined against an answer this does not have.
	return value, ok, nil
}

func lookupSettingRaw(body map[string]any, spec SettingSpec) (any, bool) {
	table := body
	if path := spec.Table(); path != "" {
		for _, segment := range strings.Split(path, ".") {
			nested, ok := table[segment].(map[string]any)
			if !ok {
				return nil, false
			}
			table = nested
		}
	}
	raw, ok := table[spec.Name()]
	return raw, ok
}

func settingValueFromRaw(spec SettingSpec, raw any) (SettingValue, bool) {
	switch spec.Kind {
	case SettingString:
		text, ok := raw.(string)
		return StringSetting(text), ok
	case SettingBool:
		flag, ok := raw.(bool)
		return BoolSetting(flag), ok
	case SettingInt:
		number, ok := raw.(int64)
		return IntSetting(number), ok
	case SettingStringList:
		return stringListFromRaw(raw)
	}
	return SettingValue{}, false
}

func stringListFromRaw(raw any) (SettingValue, bool) {
	elements, ok := raw.([]any)
	if !ok {
		return SettingValue{}, false
	}
	items := make([]string, 0, len(elements))
	for _, element := range elements {
		text, ok := element.(string)
		if !ok {
			return SettingValue{}, false
		}
		items = append(items, text)
	}
	return ListSetting(items), true
}

// checkSettingPatched proves the patched body still loads and now says exactly
// what the patch meant. want is nil for an unset, where the key must be absent.
//
// It is the guard that makes every other refusal in this file safe to relax: the
// patchers work on lines, and a line-level edit that is wrong about where a value
// ended would otherwise hand back a body whose first reader is the loader.
func checkSettingPatched(patched []byte, spec SettingSpec, want *SettingValue) error {
	value, assigned, err := readSetting(patched, spec)
	if err != nil {
		return fmt.Errorf("config: patching %s produced a body that no longer loads: %w", spec.Key, err)
	}
	if want == nil {
		if assigned {
			return fmt.Errorf("config: unsetting %s left the key assigned", spec.Key)
		}
		return nil
	}
	if !assigned || !settingValuesEqual(value, *want) {
		return fmt.Errorf("config: patching %s did not bind the requested value", spec.Key)
	}
	return nil
}

func setSettingAssignment(lines []string, spec SettingSpec, rendered string) []string {
	site := findSettingSite(lines, spec)
	if !site.found() {
		return insertSettingAssignment(lines, spec, rendered, site)
	}
	key := spec.Name()
	if site.dotted {
		key = spec.Key
	}
	out := append([]string(nil), lines[:site.assign]...)
	out = append(out, rewriteAssignmentValue(lines[site.assign], key, rendered, site.comment))
	return append(out, lines[site.assignEnd+1:]...)
}

func findSettingSite(lines []string, spec SettingSpec) keySite {
	if spec.Table() == "" {
		return findRootKeySite(lines, spec.Name())
	}
	return findKeySite(lines, spec.Table(), spec.Name())
}

// findRootKeySite locates the one assignment of a root-level key: a key that
// belongs to no table, which in TOML means it has to appear before the first
// table header.
//
// Assignments are skipped span-wise rather than line by line, for the reason
// findKeySite does the same: a line inside a multi-line array can begin with
// `[`, and reading it as the table header that ends the root section would have
// this report "not assigned" for a key the file does bind — and the caller would
// then insert a second binding, which is a file that stops loading.
func findRootKeySite(lines []string, key string) keySite {
	site := keySite{tableStart: -1, tableEnd: len(lines), assign: -1, assignEnd: -1}
	for i := 0; i < len(lines); i++ {
		if _, _, isHeader := parseTOMLTableHeader(lines[i]); isHeader {
			site.tableEnd = i
			return site
		}
		if !looksLikeAssignment(lines[i]) {
			continue
		}
		end, comment := assignmentValueSpan(lines, i)
		if matchesTOMLKeyPath(lines[i], key) {
			site.assign, site.assignEnd, site.comment = i, end, comment
			return site
		}
		i = end
	}
	return site
}

func looksLikeAssignment(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return false
	}
	return strings.IndexByte(trimmed, '=') > 0
}

func insertSettingAssignment(lines []string, spec SettingSpec, rendered string, site keySite) []string {
	eol := configLineEnding(lines)
	assignment := spec.Name() + " = " + rendered + eol
	if spec.Table() == "" {
		// site.tableEnd is where the root section stops, found by the same
		// span-aware scan that failed to find the key, so a `["a"]` line inside
		// another key's array cannot be mistaken for the first table header.
		return insertRootAssignment(lines, site.tableEnd, assignment)
	}
	return insertKeyAssignment(lines, spec.Table(), []string{assignment})
}

// insertRootAssignment places a root-level key above the first table header,
// which is the only position where it still binds to the root table. The blank
// lines separating the preamble from that header stay below the new line, so a
// file's sections keep their spacing.
func insertRootAssignment(lines []string, before int, assignment string) []string {
	if before < 0 || before > len(lines) {
		before = len(lines)
	}
	at := before - len(trailingBlankLines(lines[:before]))
	out := append([]string(nil), lines[:at]...)
	out = append(out, assignment)
	return append(out, lines[at:]...)
}
