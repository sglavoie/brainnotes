package cli

import (
	"sort"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/query"
	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/wikilink"
)

// relinkChange is one rewritten link, carrying the body line it sits on or the
// frontmatter key it was stored under so the dry-run preview can render it.
type relinkChange struct {
	line           int
	frontmatterKey string
	oldRaw         string
	newRaw         string
}

func applyRelink(n *vault.Note, idx *relinkIndex, g *query.Graph, fmCfg *config.FrontmatterConfig, vaultRoot string, dry *DryRun, journaler *noteops.Journal) (bool, []relinkResidue, error) {
	preBroken := query.UnresolvedLinks(n, g)
	if len(preBroken) == 0 {
		return false, nil, nil
	}
	changes, err := rewriteRelinkNote(n, idx, preBroken)
	if err != nil {
		return false, nil, err
	}

	relPath := displayPath(n.Path, vaultRoot)
	if len(changes) == 0 {
		return false, relinkResiduesForNote(relPath, preBroken), nil
	}
	sortRelinkChanges(changes)

	if err := commitRelink(n, fmCfg, relPath, changes, dry, journaler); err != nil {
		return false, nil, err
	}

	postBroken := query.UnresolvedLinks(n, g)
	return true, relinkResiduesForNote(relPath, postBroken), nil
}

// commitRelink is the one branch between rendering the preview and writing the
// note. The journal capture precedes the save, so the pre-image exists even when
// the save itself fails.
func commitRelink(n *vault.Note, fmCfg *config.FrontmatterConfig, relPath string, changes []relinkChange, dry *DryRun, journaler *noteops.Journal) error {
	if dry != nil {
		return renderRelinkPreview(dry, relPath, changes)
	}
	if err := journaler.Capture(n.Path); err != nil {
		return err
	}
	return n.Save(fmCfg)
}

// rewriteRelinkNote rewrites the body first and the frontmatter second, which is
// the order the changes are collected in and the order sortRelinkChanges then
// normalizes. The note is mutated in memory; the caller decides whether to save
// it or render it.
func rewriteRelinkNote(n *vault.Note, idx *relinkIndex, preBroken []query.BrokenLink) ([]relinkChange, error) {
	bodyChanges, err := rewriteRelinkBody(n, idx, preBroken)
	if err != nil {
		return nil, err
	}
	frontChanges, err := rewriteRelinkFront(n, idx)
	if err != nil {
		return nil, err
	}
	return append(bodyChanges, frontChanges...), nil
}

// rewriteRelinkBody rewrites the note body. preBroken supplies the line number
// each body link sits on, keyed by its byte offset, because the rewriter sees
// offsets rather than lines.
func rewriteRelinkBody(n *vault.Note, idx *relinkIndex, preBroken []query.BrokenLink) ([]relinkChange, error) {
	bodyLineByStart := make(map[int]int, len(preBroken))
	for _, bl := range preBroken {
		if bl.FrontmatterKey == "" {
			bodyLineByStart[bl.Link.Start] = bl.Line
		}
	}
	body, err := n.Body()
	if err != nil {
		return nil, err
	}
	newBody, changes := rewriteRelinkText(body, idx, "", bodyLineByStart)
	if newBody == body {
		return nil, nil
	}
	if err := n.SetBody(newBody); err != nil {
		return nil, err
	}
	return changes, nil
}

// rewriteRelinkFront rewrites every frontmatter value, in key order.
func rewriteRelinkFront(n *vault.Note, idx *relinkIndex) ([]relinkChange, error) {
	front, err := n.Front()
	if err != nil {
		return nil, err
	}
	var changes []relinkChange
	for _, key := range front.Keys() {
		value, ok := front.Get(key)
		if !ok {
			continue
		}
		newValue, fmChanges, changed := rewriteRelinkValue(value, key, idx)
		if !changed {
			continue
		}
		front.Set(key, newValue)
		changes = append(changes, fmChanges...)
	}
	return changes, nil
}

// renderRelinkPreview renders the --dry-run view through the canonical
// NotePreview model, so relink's preview cannot drift from every other
// mutating command's.
func renderRelinkPreview(dry *DryRun, relPath string, changes []relinkChange) error {
	preview := noteops.NotePreview{RelPath: relPath}
	for _, change := range changes {
		if change.frontmatterKey != "" {
			preview.Changes = append(preview.Changes, noteops.PreviewChange{
				Kind: noteops.PreviewFrontmatter,
				Key:  change.frontmatterKey,
				Old:  noteops.PresentText(change.oldRaw),
				New:  noteops.PresentText(change.newRaw),
			})
			continue
		}
		preview.Changes = append(preview.Changes, noteops.PreviewChange{
			Kind: noteops.PreviewBody,
			Line: change.line,
			Old:  noteops.PresentText(change.oldRaw),
			New:  noteops.PresentText(change.newRaw),
		})
	}
	return noteops.RenderNotePreview(dry.Out, preview)
}

func rewriteRelinkValue(v any, topKey string, idx *relinkIndex) (any, []relinkChange, bool) {
	switch vv := v.(type) {
	case string:
		out, changes := rewriteRelinkText(vv, idx, topKey, nil)
		if out == vv {
			return v, nil, false
		}
		return out, changes, true
	case []any:
		return rewriteRelinkList(vv, topKey, idx)
	case map[string]any:
		return rewriteRelinkMap(vv, topKey, idx)
	default:
		return v, nil, false
	}
}

// rewriteRelinkList rewrites every element, preserving order. An unchanged list
// is returned as-is rather than as a copy, so a frontmatter value nothing
// rewrote is not re-marshalled.
func rewriteRelinkList(list []any, topKey string, idx *relinkIndex) (any, []relinkChange, bool) {
	out := make([]any, len(list))
	changed := false
	var changes []relinkChange
	for i, item := range list {
		newItem, itemChanges, itemChanged := rewriteRelinkValue(item, topKey, idx)
		out[i] = newItem
		if itemChanged {
			changed = true
		}
		changes = append(changes, itemChanges...)
	}
	if !changed {
		return list, nil, false
	}
	return out, changes, true
}

// rewriteRelinkMap rewrites every value, walking the keys in sorted order so the
// changes a nested map contributes are reported deterministically.
func rewriteRelinkMap(m map[string]any, topKey string, idx *relinkIndex) (any, []relinkChange, bool) {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make(map[string]any, len(m))
	changed := false
	var changes []relinkChange
	for _, key := range keys {
		newItem, itemChanges, itemChanged := rewriteRelinkValue(m[key], topKey, idx)
		out[key] = newItem
		if itemChanged {
			changed = true
		}
		changes = append(changes, itemChanges...)
	}
	if !changed {
		return m, nil, false
	}
	return out, changes, true
}

func rewriteRelinkText(text string, idx *relinkIndex, frontmatterKey string, bodyLineByStart map[int]int) (string, []relinkChange) {
	var changes []relinkChange
	out := wikilink.Rewrite(text, func(link wikilink.Link) (string, bool) {
		newTarget, ok := idx.deterministicTarget(link)
		if !ok {
			return "", false
		}
		replacement := link.WithTarget(newTarget)
		if replacement == link.Raw {
			return "", false
		}
		change := relinkChange{
			frontmatterKey: frontmatterKey,
			oldRaw:         link.Raw,
			newRaw:         replacement,
		}
		if frontmatterKey == "" {
			change.line = bodyLineByStart[link.Start]
		}
		changes = append(changes, change)
		return replacement, true
	})
	return out, changes
}
