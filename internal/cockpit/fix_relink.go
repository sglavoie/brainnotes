package cockpit

import (
	"path"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
)

type relinkRewrite struct {
	changes  []noteops.PreviewChange
	newBody  *string
	newFront map[string]any
	skips    []FixSkip
}

// planNoteRelink rewrites only the broken links in note whose unresolved target
// key matches targetKey and that have a unique deterministic destination. Links
// that match the task but have no deterministic target are recorded as skips so
// callers know to fall back to stub creation.
func planNoteRelink(note *vault.Note, rel string, g *query.Graph, idx *relinkIndex, targetKey string) (relinkRewrite, error) {
	var rewrite relinkRewrite

	broken := query.UnresolvedLinks(note, g)
	bodyLineByStart := make(map[int]int, len(broken))
	matchedResidue := false
	for _, bl := range broken {
		if brokenTargetKey(g, bl.Link.Target) != targetKey {
			continue
		}
		if bl.FrontmatterKey == "" {
			bodyLineByStart[bl.Link.Start] = bl.Line
		}
	}

	replace := func(link wikilink.Link, key string) (string, bool) {
		if brokenTargetKey(g, link.Target) != targetKey {
			return "", false
		}
		newTarget, ok := idx.deterministicTarget(link)
		if !ok {
			matchedResidue = true
			return "", false
		}
		replacement := link.WithTarget(newTarget)
		if replacement == link.Raw {
			return "", false
		}
		change := noteops.PreviewChange{Old: noteops.PresentText(link.Raw), New: noteops.PresentText(replacement)}
		if key == "" {
			change.Kind = noteops.PreviewBody
			change.Line = bodyLineByStart[link.Start]
		} else {
			change.Kind = noteops.PreviewFrontmatter
			change.Key = key
		}
		rewrite.changes = append(rewrite.changes, change)
		return replacement, true
	}

	body, err := note.Body()
	if err != nil {
		return relinkRewrite{}, err
	}
	newBody := wikilink.Rewrite(body, func(link wikilink.Link) (string, bool) {
		return replace(link, "")
	})
	if newBody != body {
		rewrite.newBody = &newBody
	}

	front, err := note.Front()
	if err != nil {
		return relinkRewrite{}, err
	}
	for _, key := range front.Keys() {
		value, ok := front.Get(key)
		if !ok {
			continue
		}
		newValue, changed := rewriteRelinkValue(value, key, replace)
		if !changed {
			continue
		}
		if rewrite.newFront == nil {
			rewrite.newFront = map[string]any{}
		}
		rewrite.newFront[key] = newValue
	}

	sortPreviewChanges(rewrite.changes)
	if len(rewrite.changes) == 0 && matchedResidue {
		rewrite.skips = append(rewrite.skips, FixSkip{RelPath: rel, Target: targetKey, Reason: "no deterministic relink target"})
	}
	return rewrite, nil
}

// rewriteRelinkValue applies replace to every string leaf of a frontmatter
// value, returning the rewritten value and whether anything changed.
func rewriteRelinkValue(v any, key string, replace func(wikilink.Link, string) (string, bool)) (any, bool) {
	switch vv := v.(type) {
	case string:
		out := wikilink.Rewrite(vv, func(link wikilink.Link) (string, bool) {
			return replace(link, key)
		})
		if out == vv {
			return v, false
		}
		return out, true
	case []any:
		out := make([]any, len(vv))
		changed := false
		for i, item := range vv {
			newItem, itemChanged := rewriteRelinkValue(item, key, replace)
			out[i] = newItem
			if itemChanged {
				changed = true
			}
		}
		if !changed {
			return v, false
		}
		return out, true
	case map[string]any:
		keys := make([]string, 0, len(vv))
		for k := range vv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(vv))
		changed := false
		for _, k := range keys {
			newItem, itemChanged := rewriteRelinkValue(vv[k], key, replace)
			out[k] = newItem
			if itemChanged {
				changed = true
			}
		}
		if !changed {
			return v, false
		}
		return out, true
	default:
		return v, false
	}
}

// brokenTargetKey mirrors brokenTargetTasks' grouping key so relink and stub
// planning select exactly the links a task represents.
func brokenTargetKey(g *query.Graph, target string) string {
	key := g.UnresolvedTargetKey(target)
	if key == "" {
		key = strings.ToLower(strings.TrimSpace(target))
	}
	return key
}

func sortPreviewChanges(changes []noteops.PreviewChange) {
	sort.SliceStable(changes, func(i, j int) bool {
		if changes[i].Kind != changes[j].Kind {
			return changes[i].Kind < changes[j].Kind
		}
		if changes[i].Key != changes[j].Key {
			return changes[i].Key < changes[j].Key
		}
		if changes[i].Line != changes[j].Line {
			return changes[i].Line < changes[j].Line
		}
		return changes[i].Old.Text < changes[j].Old.Text
	})
}

// --- create stub -----------------------------------------------------------

func (f *Fixer) planCreateStub(plan FixPlan, task Task, data vaultData, byRel map[string]*vault.Note) (FixPlan, error) {
	sourceRel, rawTarget, ok := brokenTargetSource(task, data, byRel)
	if !ok {
		plan.Skipped = append(plan.Skipped, FixSkip{Target: task.Detail, Reason: "no source note links to this target"})
		return plan, nil
	}
	preview, err := noteops.PreviewStubFromBrokenTarget(f.vault.Root, sourceRel, rawTarget)
	if err != nil {
		return plan, err
	}
	plan.Previews = append(plan.Previews, noteops.NotePreview{
		RelPath: preview.Destination.RelPath,
		Changes: []noteops.PreviewChange{{
			Kind: noteops.PreviewPath,
			Old:  noteops.AbsentText(),
			New:  noteops.PresentText(preview.Destination.RelPath),
		}},
	})
	return plan, nil
}

func (f *Fixer) applyCreateStub(out FixOutcome, task Task, data vaultData, byRel map[string]*vault.Note) (FixOutcome, error) {
	sourceRel, rawTarget, ok := brokenTargetSource(task, data, byRel)
	if !ok {
		out.Skipped = append(out.Skipped, FixSkip{Target: task.Detail, Reason: "no source note links to this target"})
		return out, nil
	}
	result, err := noteops.CreateStubFromBrokenTarget(f.vault.Root, sourceRel, rawTarget,
		noteops.WithCreateCommand(f.command))
	if err != nil {
		return out, err
	}
	out.Created = append(out.Created, result.Destination.RelPath)
	out.Journal = result.Journal
	return out, nil
}

// brokenTargetSource finds the first affected note that still links to the
// task's target and returns its relative path plus the raw (original-case)
// wikilink target to seed a stub note.
func brokenTargetSource(task Task, data vaultData, byRel map[string]*vault.Note) (string, string, bool) {
	for _, affected := range task.Affected {
		note, ok := byRel[affected.RelPath]
		if !ok {
			continue
		}
		for _, bl := range query.UnresolvedLinks(note, data.graph) {
			if brokenTargetKey(data.graph, bl.Link.Target) == task.Detail {
				return affected.RelPath, bl.Link.Target, true
			}
		}
	}
	return "", "", false
}

// --- relink index ----------------------------------------------------------

// relinkTarget is a note the relink index can rewrite a broken link to.
type relinkTarget struct {
	title    string
	relNoExt string
}

// relinkIndex maps note path keys and title keys to their targets so the relink
// planner can find a unique, deterministic destination for a broken link. It is
// built from the same loaded snapshot the queue uses.
type relinkIndex struct {
	byPathKey  map[string]relinkTarget
	byTitleKey map[string][]relinkTarget
}

func newRelinkIndex(notes []loadedNote) *relinkIndex {
	idx := &relinkIndex{
		byPathKey:  make(map[string]relinkTarget),
		byTitleKey: make(map[string][]relinkTarget),
	}
	for _, item := range notes {
		relNoExt := strings.TrimSuffix(item.rel, path.Ext(item.rel))
		if relNoExt == "" {
			continue
		}
		title := item.note.Title()
		target := relinkTarget{title: title, relNoExt: relNoExt}
		idx.byPathKey[strings.ToLower(relNoExt)] = target
		titleKey := strings.ToLower(strings.TrimSpace(title))
		if titleKey == "" {
			continue
		}
		idx.byTitleKey[titleKey] = append(idx.byTitleKey[titleKey], target)
	}
	for key := range idx.byTitleKey {
		sort.Slice(idx.byTitleKey[key], func(i, j int) bool {
			return idx.byTitleKey[key][i].relNoExt < idx.byTitleKey[key][j].relNoExt
		})
	}
	return idx
}

// deterministicTarget returns the rewritten target string for a broken link
// when exactly one destination matches, and false otherwise. Folder-qualified
// links prefer an exact path match; bare links resolve by unique title.
func (idx *relinkIndex) deterministicTarget(link wikilink.Link) (string, bool) {
	title := linkBaseTitle(link.Target)
	if title == "" || query.IsMediaTarget(title) {
		return "", false
	}
	if strings.ContainsAny(link.Target, `/\`) {
		if target, ok := idx.byPathKey[relinkPathKey(link.Target)]; ok {
			return target.relNoExt, true
		}
	}
	titleKey := strings.ToLower(strings.TrimSpace(stripMarkdownExt(title)))
	targets := idx.byTitleKey[titleKey]
	if len(targets) != 1 {
		return "", false
	}
	if strings.ContainsAny(link.Target, `/\`) {
		return targets[0].relNoExt, true
	}
	return targets[0].title, true
}

func linkBaseTitle(target string) string {
	target = strings.TrimSpace(target)
	if idx := strings.LastIndexAny(target, `/\`); idx >= 0 {
		return target[idx+1:]
	}
	return target
}

func relinkPathKey(target string) string {
	target = strings.TrimSpace(strings.ReplaceAll(target, `\`, "/"))
	target = strings.TrimPrefix(path.Clean("/"+target), "/")
	target = stripMarkdownExt(target)
	if target == "" || target == "." {
		return ""
	}
	return strings.ToLower(target)
}

func stripMarkdownExt(s string) string {
	if len(s) >= 3 && strings.EqualFold(s[len(s)-3:], ".md") {
		return s[:len(s)-3]
	}
	return s
}
