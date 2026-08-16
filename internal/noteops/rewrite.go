package noteops

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
)

// ChangeKind identifies the locus rewritten by a rename or move operation.
type ChangeKind int

const (
	ChangeBody ChangeKind = iota
	ChangeFrontmatter
)

// Change is one line-level or frontmatter-property edit produced by a plan.
type Change struct {
	File    string
	Kind    ChangeKind
	Line    int
	Key     string
	OldText string
	NewText string
}

type rewriteFunc func(text, oldVal, newVal string) string

func scanBodyWithRewrite(file, body, oldVal, newVal string, rewrite rewriteFunc) []Change {
	if body == "" {
		return nil
	}
	newBody := rewrite(body, oldVal, newVal)
	if newBody == body {
		return nil
	}
	return diffBodyLines(file, body, newBody)
}

func diffBodyLines(file, oldBody, newBody string) []Change {
	var changes []Change
	line := 1
	oStart, nStart := 0, 0
	for oStart <= len(oldBody) && nStart <= len(newBody) {
		oEnd := strings.IndexByte(oldBody[oStart:], '\n')
		nEnd := strings.IndexByte(newBody[nStart:], '\n')
		var oldLine, newLine string
		if oEnd < 0 {
			oldLine = oldBody[oStart:]
		} else {
			oldLine = oldBody[oStart : oStart+oEnd]
		}
		if nEnd < 0 {
			newLine = newBody[nStart:]
		} else {
			newLine = newBody[nStart : nStart+nEnd]
		}
		if oldLine != newLine {
			changes = append(changes, Change{
				File:    file,
				Kind:    ChangeBody,
				Line:    line,
				OldText: oldLine,
				NewText: newLine,
			})
		}
		if oEnd < 0 || nEnd < 0 {
			break
		}
		oStart += oEnd + 1
		nStart += nEnd + 1
		line++
	}
	return changes
}

func scanFrontmatterWithRewrite(file string, front *frontmatter.Map, oldVal, newVal string, rewrite rewriteFunc) []Change {
	return transformFrontmatter(file, front, oldVal, newVal, rewrite, false)
}

func rewriteFrontmatterProps(front *frontmatter.Map, oldVal, newVal string, fmCfg *config.FrontmatterConfig, rewrite rewriteFunc) {
	_ = fmCfg // Retained for API compatibility and Save-time wrapping.
	transformFrontmatter("", front, oldVal, newVal, rewrite, true)
}

// transformFrontmatter is the shared recursive traversal used by both plan
// previews and applies. It rewrites only text recognized by the supplied
// wikilink rewriter, so plain metadata remains untouched even though every
// top-level property and nested value is visited.
func transformFrontmatter(file string, front *frontmatter.Map, oldVal, newVal string, rewrite rewriteFunc, apply bool) []Change {
	if front == nil {
		return nil
	}
	var changes []Change
	for _, key := range front.Keys() {
		value, ok := front.Get(key)
		if !ok {
			continue
		}
		newValue, valueChanges, changed := transformFrontmatterValue(value, oldVal, newVal, rewrite)
		for _, change := range valueChanges {
			changes = append(changes, Change{
				File:    file,
				Kind:    ChangeFrontmatter,
				Key:     key,
				OldText: change.old,
				NewText: change.new,
			})
		}
		if apply && changed {
			front.Set(key, newValue)
		}
	}
	return changes
}

type frontmatterTextChange struct {
	old string
	new string
}

func transformFrontmatterValue(value any, oldVal, newVal string, rewrite rewriteFunc) (any, []frontmatterTextChange, bool) {
	switch current := value.(type) {
	case string:
		next := rewrite(current, oldVal, newVal)
		if next == current {
			return value, nil, false
		}
		return next, []frontmatterTextChange{{old: current, new: next}}, true
	case []any:
		next := make([]any, len(current))
		changed := false
		var changes []frontmatterTextChange
		for i, item := range current {
			newItem, itemChanges, itemChanged := transformFrontmatterValue(item, oldVal, newVal, rewrite)
			next[i] = newItem
			changed = changed || itemChanged
			changes = append(changes, itemChanges...)
		}
		if !changed {
			return value, nil, false
		}
		return next, changes, true
	case []string:
		next := make([]string, len(current))
		changed := false
		var changes []frontmatterTextChange
		for i, item := range current {
			newItem := rewrite(item, oldVal, newVal)
			next[i] = newItem
			if newItem != item {
				changed = true
				changes = append(changes, frontmatterTextChange{old: item, new: newItem})
			}
		}
		if !changed {
			return value, nil, false
		}
		return next, changes, true
	case map[string]any:
		keys := make([]string, 0, len(current))
		for key := range current {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		next := make(map[string]any, len(current))
		changed := false
		var changes []frontmatterTextChange
		for _, key := range keys {
			newItem, itemChanges, itemChanged := transformFrontmatterValue(current[key], oldVal, newVal, rewrite)
			next[key] = newItem
			changed = changed || itemChanged
			changes = append(changes, itemChanges...)
		}
		if !changed {
			return value, nil, false
		}
		return next, changes, true
	default:
		return value, nil, false
	}
}

// relOrAbs renders p relative to vaultRoot, falling back to the absolute path
// when the two share no common base.
func relOrAbs(vaultRoot, p string) string {
	rel, err := filepath.Rel(vaultRoot, p)
	if err != nil {
		return p
	}
	return rel
}

// planPreviews groups a rename/move plan's line-level changes into canonical
// NotePreviews, one per affected file. The note at targetFile additionally
// carries a leading path change (pathOld -> pathNew) describing the file
// rename/move itself, so the target always appears even when it has no
// self-references. Files are ordered alphabetically by vault-relative path;
// within a note the path change comes first, then body changes by line, then
// frontmatter changes by key — the same deterministic ordering the previous
// printChanges output used.
func planPreviews(vaultRoot, targetFile, pathOld, pathNew string, changes []Change) []NotePreview {
	byFile := make(map[string][]Change)
	for _, c := range changes {
		byFile[c.File] = append(byFile[c.File], c)
	}
	if _, ok := byFile[targetFile]; !ok {
		byFile[targetFile] = nil
	}

	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool {
		return relOrAbs(vaultRoot, files[i]) < relOrAbs(vaultRoot, files[j])
	})

	previews := make([]NotePreview, 0, len(files))
	for _, f := range files {
		p := NotePreview{RelPath: relOrAbs(vaultRoot, f)}
		if f == targetFile {
			p.Changes = append(p.Changes, PreviewChange{
				Kind: PreviewPath,
				Old:  PresentText(pathOld),
				New:  PresentText(pathNew),
			})
		}
		fchanges := append([]Change(nil), byFile[f]...)
		sort.SliceStable(fchanges, func(i, j int) bool {
			if fchanges[i].Kind != fchanges[j].Kind {
				return fchanges[i].Kind < fchanges[j].Kind
			}
			if fchanges[i].Line != fchanges[j].Line {
				return fchanges[i].Line < fchanges[j].Line
			}
			return fchanges[i].Key < fchanges[j].Key
		})
		for _, c := range fchanges {
			switch c.Kind {
			case ChangeBody:
				p.Changes = append(p.Changes, PreviewChange{
					Kind: PreviewBody,
					Line: c.Line,
					Old:  PresentText(c.OldText),
					New:  PresentText(c.NewText),
				})
			case ChangeFrontmatter:
				p.Changes = append(p.Changes, PreviewChange{
					Kind: PreviewFrontmatter,
					Key:  c.Key,
					Old:  PresentText(c.OldText),
					New:  PresentText(c.NewText),
				})
			}
		}
		previews = append(previews, p)
	}
	return previews
}
