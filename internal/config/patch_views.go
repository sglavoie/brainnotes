package config

import (
	"fmt"
	"strconv"
	"strings"
)

// viewsTablePath is the TOML path every saved view lives under. Views are an
// array of tables — `[[views]]` — rather than `[views.<name>]` tables, because
// the list is ordered: `bn views` and the web nav present views in the order
// the file declares them, which a TOML table map does not preserve.
const viewsTablePath = "views"

// ViewPatch is one view-level edit to brainnotes.toml. It mirrors
// PropertyPatch, including its PatchOp vocabulary, so the two config patch
// surfaces stay one idiom.
type ViewPatch struct {
	Op   PatchOp
	Name string
	// NewName is the destination name for PatchRename.
	NewName string
	// View is the replacement view for PatchUpsert. Its Name field is ignored
	// in favour of Name, so a patch cannot disagree with itself.
	View SavedView
}

// PatchViews applies view-level edits to a brainnotes.toml body while
// preserving every unrelated line byte-for-byte.
//
// Only the targeted `[[views]]` blocks are rewritten, so comments and
// formatting elsewhere survive. Comments *inside* a rewritten block do not: an
// upsert replaces the whole block, which is why the returned diff is the exact
// preview the caller must show before writing. brainnotes.toml is the
// vault-root marker, so previewing is not optional politeness — a mangled patch
// is a vault-wide failure.
func PatchViews(source []byte, patches []ViewPatch) (PatchResult, error) {
	lines, trailingNewline := splitConfigLines(source)
	blocks := findViewBlocks(lines)

	out := lines
	for _, patch := range patches {
		var err error
		out, err = applyViewPatch(out, blocks, patch)
		if err != nil {
			return PatchResult{}, err
		}
		blocks = findViewBlocks(out)
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

// RenderViewBlock renders one saved view as canonical TOML. Optional fields are
// omitted at their zero value so a minimal view stays a three-line block.
func RenderViewBlock(view SavedView) []string {
	lines := []string{
		"[[" + viewsTablePath + "]]",
		"name = " + quoteTOMLString(view.Name),
		"query = " + quoteTOMLString(view.Query),
	}
	if view.Search != "" {
		lines = append(lines, "search = "+quoteTOMLString(view.Search))
	}
	if view.Description != "" {
		lines = append(lines, "description = "+quoteTOMLString(view.Description))
	}
	if view.Sort != "" {
		lines = append(lines, "sort = "+quoteTOMLString(view.Sort))
	}
	if view.RelativeTo != "" {
		lines = append(lines, "relative_to = "+quoteTOMLString(view.RelativeTo))
	}
	if view.Limit > 0 {
		lines = append(lines, "limit = "+strconv.Itoa(view.Limit))
	}
	return lines
}

// viewBlock locates one saved view inside a configuration file. As with
// propertyBlock, the block runs from its header up to (but excluding) the next
// table header, so the blank lines separating it from the next section travel
// with it and a deletion cannot glue two unrelated sections together.
//
// nameLine is where the block's `name` key sits, so a rename rewrites that one
// line and leaves the rest of the block — comments included — alone. It is -1
// for a block whose name this file could not read.
type viewBlock struct {
	name     string
	start    int
	end      int
	nameLine int
}

func applyViewPatch(lines []string, blocks []viewBlock, patch ViewPatch) ([]string, error) {
	name := strings.TrimSpace(patch.Name)
	if name == "" {
		return nil, fmt.Errorf("config: patch requires a view name")
	}
	existing, found := lookupViewBlock(blocks, name)

	switch patch.Op {
	case PatchDelete:
		if !found {
			return nil, fmt.Errorf("config: view %q is not declared", name)
		}
		return append(append([]string(nil), lines[:existing.start]...), lines[existing.end:]...), nil

	case PatchRename:
		newName := strings.TrimSpace(patch.NewName)
		if newName == "" {
			return nil, fmt.Errorf("config: rename of view %q requires a new name", name)
		}
		if newName == name {
			return lines, nil
		}
		if !found {
			return nil, fmt.Errorf("config: view %q is not declared", name)
		}
		if other, taken := lookupViewBlock(blocks, newName); taken && other.start != existing.start {
			return nil, fmt.Errorf("config: view %q is already declared", newName)
		}
		if err := validateSavedViewName("view", newName); err != nil {
			return nil, err
		}
		if existing.nameLine < 0 {
			return nil, fmt.Errorf("config: view %q has no readable name key to rename", name)
		}
		out := append([]string(nil), lines...)
		out[existing.nameLine] = "name = " + quoteTOMLString(newName)
		return out, nil

	case PatchUpsert:
		view := patch.View
		view.Name = name
		if err := validateSavedView("view", view); err != nil {
			return nil, err
		}
		rendered := RenderViewBlock(view)
		if found {
			// Keep the block's trailing blank lines so the section spacing the
			// user chose survives a rewrite.
			rendered = append(rendered, trailingBlankLines(lines[existing.start:existing.end])...)
			out := append([]string(nil), lines[:existing.start]...)
			out = append(out, rendered...)
			return append(out, lines[existing.end:]...), nil
		}
		return insertViewBlock(lines, blocks, rendered), nil

	default:
		return nil, fmt.Errorf("config: unknown patch operation %q", patch.Op)
	}
}

// insertViewBlock appends a new block after the last existing view block so the
// section stays contiguous, or at end of file when none exists.
func insertViewBlock(lines []string, blocks []viewBlock, rendered []string) []string {
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

// lookupViewBlock matches case-insensitively, exactly as Config.View and the
// uniqueness rule in validateViews do, so a patch can never target a view the
// loaded config would have resolved differently.
func lookupViewBlock(blocks []viewBlock, name string) (viewBlock, bool) {
	for _, block := range blocks {
		if block.name != "" && strings.EqualFold(block.name, name) {
			return block, true
		}
	}
	return viewBlock{}, false
}

func findViewBlocks(lines []string) []viewBlock {
	var blocks []viewBlock
	var current *viewBlock
	for i, line := range lines {
		path, isArray, isHeader := parseTOMLTableHeader(line)
		if isHeader {
			if current != nil {
				current.end = i
				blocks = append(blocks, *current)
				current = nil
			}
			if isArray && path == viewsTablePath {
				current = &viewBlock{start: i, nameLine: -1}
			}
			continue
		}
		if current == nil || current.nameLine >= 0 {
			continue
		}
		if value, ok := parseViewNameAssignment(line); ok {
			current.name = value
			current.nameLine = i
		}
	}
	if current != nil {
		current.end = len(lines)
		blocks = append(blocks, *current)
	}
	return blocks
}

// parseTOMLTableHeader is the array-of-table-aware sibling of
// parseTableHeader. It lives here rather than widening that function because
// the property patcher explicitly treats `[[` as "not a header" and its tests
// pin that; growing one function to serve both would put the two sections'
// behaviour on one code path for no gain.
//
// It reproduces parseTableHeader's trailing-text rule: anything after the
// closing bracket other than a comment means the line is not a table header at
// all, so a malformed line cannot be mistaken for a view header.
func parseTOMLTableHeader(line string) (path string, isArray, isHeader bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "[") {
		return "", false, false
	}
	open, closer := "[", "]"
	if strings.HasPrefix(trimmed, "[[") {
		open, closer, isArray = "[[", "]]", true
	}
	offset := strings.Index(trimmed[len(open):], closer)
	if offset < 0 {
		return "", false, false
	}
	end := len(open) + offset
	rest := strings.TrimSpace(trimmed[end+len(closer):])
	if rest != "" && !strings.HasPrefix(rest, "#") {
		return "", false, false
	}
	return strings.TrimSpace(trimmed[len(open):end]), isArray, true
}

// parseViewNameAssignment reads a `name = "..."` line from inside a view block.
//
// It understands the quoting RenderViewBlock emits plus TOML literal strings,
// which is everything a *valid* view name can be written as — validateSavedView
// rejects names needing a \u escape or a multi-line string. A name spelled in
// some other form simply leaves the block unnamed, which makes it invisible to
// lookup rather than mis-targeted by it.
func parseViewNameAssignment(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	eq := strings.IndexByte(trimmed, '=')
	if eq < 0 {
		return "", false
	}
	switch strings.TrimSpace(trimmed[:eq]) {
	case "name", `"name"`, `'name'`:
	default:
		return "", false
	}
	return unquoteTOMLStringValue(strings.TrimSpace(trimmed[eq+1:]))
}

// unquoteTOMLStringValue decodes a single-line TOML string, ignoring anything
// after its closing quote so a trailing comment does not defeat the read.
func unquoteTOMLStringValue(value string) (string, bool) {
	if len(value) < 2 {
		return "", false
	}
	if value[0] == '\'' {
		end := strings.IndexByte(value[1:], '\'')
		if end < 0 {
			return "", false
		}
		return value[1 : 1+end], true
	}
	if value[0] != '"' {
		return "", false
	}
	var b strings.Builder
	for i := 1; i < len(value); i++ {
		switch value[i] {
		case '"':
			return b.String(), true
		case '\\':
			if i+1 >= len(value) {
				return "", false
			}
			i++
			switch value[i] {
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			default:
				// \uXXXX and friends cannot appear in a name validateSavedView
				// accepts, so refusing is safer than half-decoding.
				return "", false
			}
		default:
			b.WriteByte(value[i])
		}
	}
	return "", false
}
