package cli

import (
	"sort"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
)

func applyRelink(n *vault.Note, idx *relinkIndex, g *query.Graph, fmCfg *config.FrontmatterConfig, vaultRoot string, dry *DryRun, journaler *noteops.Journal) (bool, []relinkResidue, error) {
	preBroken := query.UnresolvedLinks(n, g)
	if len(preBroken) == 0 {
		return false, nil, nil
	}
	bodyLineByStart := make(map[int]int, len(preBroken))
	for _, bl := range preBroken {
		if bl.FrontmatterKey == "" {
			bodyLineByStart[bl.Link.Start] = bl.Line
		}
	}

	var changes []relinkChange

	body, err := n.Body()
	if err != nil {
		return false, nil, err
	}
	newBody, bodyChanges := rewriteRelinkText(body, idx, "", bodyLineByStart)
	if newBody != body {
		if err := n.SetBody(newBody); err != nil {
			return false, nil, err
		}
		changes = append(changes, bodyChanges...)
	}

	front, err := n.Front()
	if err != nil {
		return false, nil, err
	}
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

	relPath := displayPath(n.Path, vaultRoot)
	if len(changes) == 0 {
		return false, relinkResiduesForNote(relPath, preBroken), nil
	}
	sortRelinkChanges(changes)

	if dry != nil {
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
		if err := noteops.RenderNotePreview(dry.Out, preview); err != nil {
			return false, nil, err
		}
	} else {
		if err := journaler.Capture(n.Path); err != nil {
			return false, nil, err
		}
		if err := n.Save(fmCfg); err != nil {
			return false, nil, err
		}
	}

	postBroken := query.UnresolvedLinks(n, g)
	return true, relinkResiduesForNote(relPath, postBroken), nil
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
		out := make([]any, len(vv))
		changed := false
		var changes []relinkChange
		for i, item := range vv {
			newItem, itemChanges, itemChanged := rewriteRelinkValue(item, topKey, idx)
			out[i] = newItem
			if itemChanged {
				changed = true
			}
			changes = append(changes, itemChanges...)
		}
		if !changed {
			return v, nil, false
		}
		return out, changes, true
	case map[string]any:
		keys := make([]string, 0, len(vv))
		for key := range vv {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(vv))
		changed := false
		var changes []relinkChange
		for _, key := range keys {
			newItem, itemChanges, itemChanged := rewriteRelinkValue(vv[key], topKey, idx)
			out[key] = newItem
			if itemChanged {
				changed = true
			}
			changes = append(changes, itemChanges...)
		}
		if !changed {
			return v, nil, false
		}
		return out, changes, true
	default:
		return v, nil, false
	}
}

func rewriteRelinkText(text string, idx *relinkIndex, frontmatterKey string, bodyLineByStart map[int]int) (string, []relinkChange) {
	var changes []relinkChange
	out := wikilink.Rewrite(text, func(link wikilink.Link) (string, bool) {
		newTarget, ok := idx.deterministicTarget(link)
		if !ok {
			return "", false
		}
		replacement := wikilink.FormatLink(newTarget, link.Heading, link.Alias)
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
