package cli

import (
	"fmt"
	"io"
	"path/filepath"

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
// after applying RewriteMove. Delegates to the shared scanBodyWithRewrite.
func scanBodyMove(file, body, oldRel, newRel string) []renameChange {
	return scanBodyWithRewrite(file, body, oldRel, newRel, wikilink.RewriteMove)
}

// scanFrontmatterMove inspects every wikilink-typed property and records a
// change when any string value (scalar or list element) contains a folder-
// prefixed link whose Target equals oldRel. Delegates to the shared
// scanFrontmatterWithRewrite.
func scanFrontmatterMove(file string, front *frontmatter.Map, oldRel, newRel string, wikilinkProps []string) []renameChange {
	return scanFrontmatterWithRewrite(file, front, oldRel, newRel, wikilinkProps, wikilink.RewriteMove)
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
	return printChanges(out, vaultRoot, plan.Changes)
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
// is rewritten to newRel. Delegates to the shared rewriteFrontmatterProps.
func rewriteFrontmatterMove(front *frontmatter.Map, oldRel, newRel string, fmCfg *config.FrontmatterConfig) {
	rewriteFrontmatterProps(front, oldRel, newRel, fmCfg, wikilink.RewriteMove)
}
