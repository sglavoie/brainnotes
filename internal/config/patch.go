package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// propertyTablePrefix is the TOML table path every property definition lives
// under.
const propertyTablePrefix = "frontmatter.properties"

// PatchOp names one requested change to the [frontmatter.properties] section.
type PatchOp string

const (
	// PatchUpsert replaces a property's definition block, or appends one when
	// the property is not declared yet.
	PatchUpsert PatchOp = "upsert"
	// PatchDelete removes a property's definition block.
	PatchDelete PatchOp = "delete"
	// PatchRename retargets a property's block header, preserving its body.
	PatchRename PatchOp = "rename"
)

// PropertyPatch is one property-level edit to brainnotes.toml.
type PropertyPatch struct {
	Op  PatchOp
	Key string
	// NewKey is the destination name for PatchRename.
	NewKey string
	// Definition is the replacement definition for PatchUpsert.
	Definition PropertyDefinition
}

// PatchLineKind classifies one line in a patch preview.
type PatchLineKind string

const (
	PatchLineRemoved PatchLineKind = "-"
	PatchLineAdded   PatchLineKind = "+"
)

// PatchLine is one previewable line-level difference.
type PatchLine struct {
	Kind PatchLineKind
	// Line is the 1-based line number in the source (removed) or in the result
	// (added).
	Line int
	Text string
}

// PatchResult is the outcome of patching a configuration file.
type PatchResult struct {
	// Patched is the complete new file content.
	Patched []byte
	// Changed reports whether Patched differs from the input.
	Changed bool
	// Diff is the exact line-level difference, in source order.
	Diff []PatchLine
}

var barePropertyKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// PatchProperties applies property-level edits to a brainnotes.toml body while
// preserving every unrelated line byte-for-byte.
//
// Only the targeted `[frontmatter.properties.<name>]` blocks are rewritten, so
// comments and formatting elsewhere survive. Comments inside a rewritten block
// do not: an upsert replaces the whole block, which is why the returned diff is
// the exact preview the caller must show before writing.
func PatchProperties(source []byte, patches []PropertyPatch) (PatchResult, error) {
	lines, trailingNewline := splitConfigLines(source)
	blocks := findPropertyBlocks(lines)

	out := lines
	for _, patch := range patches {
		var err error
		out, err = applyPropertyPatch(out, blocks, patch)
		if err != nil {
			return PatchResult{}, err
		}
		blocks = findPropertyBlocks(out)
	}

	patched := joinConfigLines(out, trailingNewline)
	result := PatchResult{
		Patched: patched,
		Changed: string(patched) != string(source),
	}
	if result.Changed {
		result.Diff = diffLines(lines, out)
	}
	return result, nil
}

// RenderPropertyBlock renders one property definition as canonical TOML.
func RenderPropertyBlock(key string, def PropertyDefinition) []string {
	lines := []string{"[" + propertyTablePrefix + "." + quoteTOMLKey(key) + "]"}
	lines = append(lines, "type = "+quoteTOMLString(string(def.Type)))
	if def.IsList() && def.Items != "" {
		lines = append(lines, "items = "+quoteTOMLString(string(def.Items)))
	}
	if def.Required {
		lines = append(lines, "required = true")
	}
	if len(def.Allowed) > 0 {
		quoted := make([]string, len(def.Allowed))
		for i, value := range def.Allowed {
			quoted[i] = quoteTOMLString(value)
		}
		lines = append(lines, "allowed = ["+strings.Join(quoted, ", ")+"]")
	}
	return lines
}

// propertyBlock locates one property definition inside a configuration file.
// The block runs from its table header up to (but excluding) the next table
// header, so the blank lines that separate it from the next section travel with
// it and deletion cannot glue two unrelated sections together.
type propertyBlock struct {
	key   string
	start int
	end   int
}

func applyPropertyPatch(lines []string, blocks []propertyBlock, patch PropertyPatch) ([]string, error) {
	key := strings.TrimSpace(patch.Key)
	if key == "" {
		return nil, fmt.Errorf("config: patch requires a property name")
	}
	existing, found := lookupPropertyBlock(blocks, key)

	switch patch.Op {
	case PatchDelete:
		if !found {
			return nil, fmt.Errorf("config: property %q is not declared", key)
		}
		return append(append([]string(nil), lines[:existing.start]...), lines[existing.end:]...), nil

	case PatchRename:
		newKey := strings.TrimSpace(patch.NewKey)
		if newKey == "" {
			return nil, fmt.Errorf("config: rename of property %q requires a new name", key)
		}
		if newKey == key {
			return lines, nil
		}
		if !found {
			return nil, fmt.Errorf("config: property %q is not declared", key)
		}
		if _, taken := lookupPropertyBlock(blocks, newKey); taken {
			return nil, fmt.Errorf("config: property %q is already declared", newKey)
		}
		out := append([]string(nil), lines...)
		out[existing.start] = "[" + propertyTablePrefix + "." + quoteTOMLKey(newKey) + "]"
		return out, nil

	case PatchUpsert:
		if err := validatePatchDefinition(key, patch.Definition); err != nil {
			return nil, err
		}
		rendered := RenderPropertyBlock(key, patch.Definition)
		if found {
			// Keep the block's trailing blank lines so the section spacing the
			// user chose survives a definition rewrite.
			rendered = append(rendered, trailingBlankLines(lines[existing.start:existing.end])...)
			out := append([]string(nil), lines[:existing.start]...)
			out = append(out, rendered...)
			return append(out, lines[existing.end:]...), nil
		}
		return insertPropertyBlock(lines, blocks, rendered), nil

	default:
		return nil, fmt.Errorf("config: unknown patch operation %q", patch.Op)
	}
}

// insertPropertyBlock appends a new block after the last existing property
// block so the section stays contiguous, or at end of file when none exists.
func insertPropertyBlock(lines []string, blocks []propertyBlock, rendered []string) []string {
	at := len(lines)
	if len(blocks) > 0 {
		at = blocks[len(blocks)-1].end
	}
	block := append([]string(nil), rendered...)
	if at > 0 && strings.TrimSpace(lines[at-1]) != "" {
		block = append([]string{""}, block...)
	}
	if at < len(lines) {
		block = append(block, "")
	}
	out := append([]string(nil), lines[:at]...)
	out = append(out, block...)
	return append(out, lines[at:]...)
}

func validatePatchDefinition(key string, def PropertyDefinition) error {
	if !ValidPropertyKind(def.Type) {
		return fmt.Errorf("config: property %q has unsupported type %q", key, def.Type)
	}
	if def.IsList() && !ValidScalarKind(def.Items) {
		return fmt.Errorf("config: list property %q needs a supported items kind, got %q", key, def.Items)
	}
	if !def.IsList() && def.Items != "" {
		return fmt.Errorf("config: property %q sets items but is not a list", key)
	}
	return nil
}

func lookupPropertyBlock(blocks []propertyBlock, key string) (propertyBlock, bool) {
	for _, block := range blocks {
		if block.key == key {
			return block, true
		}
	}
	return propertyBlock{}, false
}

func findPropertyBlocks(lines []string) []propertyBlock {
	var blocks []propertyBlock
	var current *propertyBlock
	for i, line := range lines {
		key, isProperty, isHeader := parseTableHeader(line)
		if !isHeader {
			continue
		}
		if current != nil {
			current.end = i
			blocks = append(blocks, *current)
			current = nil
		}
		if isProperty {
			current = &propertyBlock{key: key, start: i}
		}
	}
	if current != nil {
		current.end = len(lines)
		blocks = append(blocks, *current)
	}
	sort.SliceStable(blocks, func(i, j int) bool { return blocks[i].start < blocks[j].start })
	return blocks
}

// parseTableHeader reports whether line is a TOML table header and, when it
// declares a property, the property name it declares.
func parseTableHeader(line string) (key string, isProperty, isHeader bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "[[") {
		return "", false, false
	}
	close := strings.Index(trimmed, "]")
	if close < 0 {
		return "", false, false
	}
	// Anything after the closing bracket other than a comment means this is not
	// a plain table header.
	rest := strings.TrimSpace(trimmed[close+1:])
	if rest != "" && !strings.HasPrefix(rest, "#") {
		return "", false, false
	}
	path := strings.TrimSpace(trimmed[1:close])
	prefix := propertyTablePrefix + "."
	if !strings.HasPrefix(path, prefix) {
		return "", false, true
	}
	name, ok := unquoteTOMLKey(strings.TrimSpace(path[len(prefix):]))
	if !ok || name == "" {
		return "", false, true
	}
	return name, true, true
}

func trailingBlankLines(block []string) []string {
	count := 0
	for i := len(block) - 1; i >= 0; i-- {
		if strings.TrimSpace(block[i]) != "" {
			break
		}
		count++
	}
	return make([]string, count)
}

func splitConfigLines(source []byte) (lines []string, trailingNewline bool) {
	text := string(source)
	if text == "" {
		return nil, false
	}
	trailingNewline = strings.HasSuffix(text, "\n")
	if trailingNewline {
		text = strings.TrimSuffix(text, "\n")
	}
	return strings.Split(text, "\n"), trailingNewline
}

func joinConfigLines(lines []string, trailingNewline bool) []byte {
	if len(lines) == 0 {
		return nil
	}
	text := strings.Join(lines, "\n")
	if trailingNewline {
		text += "\n"
	}
	return []byte(text)
}

func quoteTOMLKey(key string) string {
	if barePropertyKeyRe.MatchString(key) {
		return key
	}
	return quoteTOMLString(key)
}

func quoteTOMLString(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				b.WriteString(fmt.Sprintf(`\u%04X`, r))
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func unquoteTOMLKey(key string) (string, bool) {
	if barePropertyKeyRe.MatchString(key) {
		return key, true
	}
	if len(key) >= 2 && strings.HasPrefix(key, `"`) && strings.HasSuffix(key, `"`) {
		return strings.ReplaceAll(key[1:len(key)-1], `\"`, `"`), true
	}
	return "", false
}

// diffLines produces a line-level diff via a longest-common-subsequence walk.
// Configuration files are small, so the quadratic table is cheaper than the
// risk of a preview that does not match the bytes actually written.
func diffLines(before, after []string) []PatchLine {
	lcs := make([][]int, len(before)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(after)+1)
	}
	for i := len(before) - 1; i >= 0; i-- {
		for j := len(after) - 1; j >= 0; j-- {
			if before[i] == after[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
				continue
			}
			if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var out []PatchLine
	i, j := 0, 0
	for i < len(before) && j < len(after) {
		switch {
		case before[i] == after[j]:
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, PatchLine{Kind: PatchLineRemoved, Line: i + 1, Text: before[i]})
			i++
		default:
			out = append(out, PatchLine{Kind: PatchLineAdded, Line: j + 1, Text: after[j]})
			j++
		}
	}
	for ; i < len(before); i++ {
		out = append(out, PatchLine{Kind: PatchLineRemoved, Line: i + 1, Text: before[i]})
	}
	for ; j < len(after); j++ {
		out = append(out, PatchLine{Kind: PatchLineAdded, Line: j + 1, Text: after[j]})
	}
	return out
}
