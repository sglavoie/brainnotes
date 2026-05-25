package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
)

// movePlan captures every edit required to move a note across folders. OldRel
// and NewRel are the vault-relative target paths *without* extension — that is
// the form wikilink Targets are matched against (e.g. "Books/Dune").
type movePlan struct {
	Target    string // absolute current path of the note being moved
	NewPath   string // absolute destination path
	OldRel    string // vault-relative path without extension
	NewRel    string // same, post-move
	Changes   []renameChange
	FileOrder []string // affected files in walk order
}

// buildMovePlan walks the vault and collects every file with folder-prefixed
// wikilinks that match the old path. Bare-title links and unrelated links are
// ignored. The moved note itself is included so folder-prefixed self-references
// get rewritten.
func buildMovePlan(v *vault.Vault, targetPath, newPath, oldRel, newRel string, cfg *config.Config) (*movePlan, error) {
	plan := &movePlan{
		Target:  targetPath,
		NewPath: newPath,
		OldRel:  oldRel,
		NewRel:  newRel,
	}
	seenFiles := make(map[string]struct{})
	addFile := func(path string) {
		if _, ok := seenFiles[path]; ok {
			return
		}
		seenFiles[path] = struct{}{}
		plan.FileOrder = append(plan.FileOrder, path)
	}

	err := v.Walk(vault.WalkFilter{}, func(n *vault.Note) error {
		if err := n.Load(); err != nil {
			return err
		}
		body, err := n.Body()
		if err != nil {
			return err
		}
		bodyChanges := scanBodyMove(n.Path, body, oldRel, newRel)
		if len(bodyChanges) > 0 {
			plan.Changes = append(plan.Changes, bodyChanges...)
			addFile(n.Path)
		}

		front, err := n.Front()
		if err != nil {
			return err
		}
		fmChanges := scanFrontmatterMove(n.Path, front, oldRel, newRel, cfg.Frontmatter.WikilinkProperties)
		if len(fmChanges) > 0 {
			plan.Changes = append(plan.Changes, fmChanges...)
			addFile(n.Path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("move: scan vault: %w", err)
	}
	return plan, nil
}

// scanBodyMove returns one renameChange per body line whose content shifts
// after applying RewriteMove. Line numbers are 1-based. See scanBodyChanges
// for why the rewrite runs on the whole body before being diffed per line.
func scanBodyMove(file, body, oldRel, newRel string) []renameChange {
	if body == "" {
		return nil
	}
	newBody := wikilink.RewriteMove(body, oldRel, newRel)
	if newBody == body {
		return nil
	}
	return diffBodyLines(file, body, newBody)
}

// scanFrontmatterMove inspects every wikilink-typed property and records a
// change when any string value (scalar or list element) contains a folder-
// prefixed link whose Target equals oldRel.
func scanFrontmatterMove(file string, front *frontmatter.Map, oldRel, newRel string, wikilinkProps []string) []renameChange {
	var changes []renameChange
	for _, key := range wikilinkProps {
		value, ok := front.Get(key)
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			newValue := wikilink.RewriteMove(typed, oldRel, newRel)
			if newValue != typed {
				changes = append(changes, renameChange{
					File:    file,
					Kind:    changeFrontmatter,
					Key:     key,
					OldText: typed,
					NewText: newValue,
				})
			}
		case []any:
			for _, item := range typed {
				text, ok := item.(string)
				if !ok {
					continue
				}
				newValue := wikilink.RewriteMove(text, oldRel, newRel)
				if newValue != text {
					changes = append(changes, renameChange{
						File:    file,
						Kind:    changeFrontmatter,
						Key:     key,
						OldText: text,
						NewText: newValue,
					})
				}
			}
		}
	}
	return changes
}

// printMovePlan renders the plan in source order: file move first, then each
// change as `<relpath>:<line>: <old> -> <new>` (body) or
// `<relpath>:frontmatter[<key>]: <old> -> <new>` (frontmatter).
func printMovePlan(out io.Writer, vaultRoot string, plan *movePlan) error {
	relTarget, err := filepath.Rel(vaultRoot, plan.Target)
	if err != nil {
		relTarget = plan.Target
	}
	relNew, err := filepath.Rel(vaultRoot, plan.NewPath)
	if err != nil {
		relNew = plan.NewPath
	}
	if _, err := fmt.Fprintf(out, "move: %s -> %s\n", relTarget, relNew); err != nil {
		return err
	}

	sorted := make([]renameChange, len(plan.Changes))
	copy(sorted, plan.Changes)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].File != sorted[j].File {
			return sorted[i].File < sorted[j].File
		}
		if sorted[i].Kind != sorted[j].Kind {
			return sorted[i].Kind < sorted[j].Kind
		}
		if sorted[i].Line != sorted[j].Line {
			return sorted[i].Line < sorted[j].Line
		}
		return sorted[i].Key < sorted[j].Key
	})
	for _, change := range sorted {
		rel, err := filepath.Rel(vaultRoot, change.File)
		if err != nil {
			rel = change.File
		}
		switch change.Kind {
		case changeBody:
			if _, err := fmt.Fprintf(out, "%s:%d: %s -> %s\n", rel, change.Line, change.OldText, change.NewText); err != nil {
				return err
			}
		case changeFrontmatter:
			if _, err := fmt.Fprintf(out, "%s:frontmatter[%s]: %s -> %s\n", rel, change.Key, change.OldText, change.NewText); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyMovePlan rewrites every affected file in walk order. The actual file
// move of the target is performed by the caller after this returns
// successfully.
func applyMovePlan(vaultRoot string, plan *movePlan, fmCfg *config.FrontmatterConfig, out io.Writer) error {
	for _, file := range plan.FileOrder {
		if err := rewriteFileMove(file, plan.OldRel, plan.NewRel, fmCfg); err != nil {
			return fmt.Errorf("move: rewrite %s: %w", file, err)
		}
		rel, err := filepath.Rel(vaultRoot, file)
		if err != nil {
			rel = file
		}
		if _, err := fmt.Fprintf(out, "%s: updated\n", rel); err != nil {
			return err
		}
	}
	return nil
}

// rewriteFileMove applies RewriteMove to the body and to every wikilink-typed
// frontmatter property, then atomically saves.
func rewriteFileMove(path, oldRel, newRel string, fmCfg *config.FrontmatterConfig) error {
	note := &vault.Note{Path: path}
	if err := note.Load(); err != nil {
		return err
	}
	body, err := note.Body()
	if err != nil {
		return err
	}
	newBody := wikilink.RewriteMove(body, oldRel, newRel)
	if newBody != body {
		if err := note.SetBody(newBody); err != nil {
			return err
		}
	}
	front, err := note.Front()
	if err != nil {
		return err
	}
	rewriteFrontmatterMove(front, oldRel, newRel, fmCfg)
	return note.Save(fmCfg)
}

// rewriteFrontmatterMove mutates front in place: every string value (scalar or
// list element) under a wikilink-typed key whose embedded Target equals oldRel
// is rewritten to newRel.
func rewriteFrontmatterMove(front *frontmatter.Map, oldRel, newRel string, fmCfg *config.FrontmatterConfig) {
	if fmCfg == nil {
		return
	}
	for _, key := range fmCfg.WikilinkProperties {
		value, ok := front.Get(key)
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			newValue := wikilink.RewriteMove(typed, oldRel, newRel)
			if newValue != typed {
				front.Set(key, newValue)
			}
		case []any:
			changed := false
			newList := make([]any, len(typed))
			for i, item := range typed {
				if text, ok := item.(string); ok {
					newValue := wikilink.RewriteMove(text, oldRel, newRel)
					if newValue != text {
						changed = true
					}
					newList[i] = newValue
				} else {
					newList[i] = item
				}
			}
			if changed {
				front.Set(key, newList)
			}
		}
	}
}
