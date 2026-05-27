package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
)

var renameFileRename = os.Rename

// scanBodyChanges returns one renameChange per body line whose content shifts
// after applying RewriteTitle. Delegates to the shared scanBodyWithRewrite.
func scanBodyChanges(file, body, oldTitle, newTitle string) []renameChange {
	return scanBodyWithRewrite(file, body, oldTitle, newTitle, wikilink.RewriteTitle)
}

// diffBodyLines walks oldBody/newBody in lockstep (the rewrite never adds or
// removes newlines, so line counts match) and emits one renameChange per
// differing line. Line numbers are 1-based.
func diffBodyLines(file, oldBody, newBody string) []renameChange {
	var changes []renameChange
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
			changes = append(changes, renameChange{
				File:    file,
				Kind:    changeBody,
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

// scanFrontmatterChanges inspects every wikilink-typed property and records a
// change when its string value (or any list element) contains a link whose
// title matches oldTitle. Delegates to the shared scanFrontmatterWithRewrite.
func scanFrontmatterChanges(file string, front *frontmatter.Map, oldTitle, newTitle string, wikilinkProps []string) []renameChange {
	return scanFrontmatterWithRewrite(file, front, oldTitle, newTitle, wikilinkProps, wikilink.RewriteTitle)
}

// printPlan renders the plan in source order: file rename first, then each
// change as `<relpath>:<line>: <old> -> <new>` (body) or
// `<relpath>:frontmatter[<key>]: <old> -> <new>` (frontmatter).
func printPlan(out io.Writer, vaultRoot string, plan *renamePlan) error {
	relTarget, err := filepath.Rel(vaultRoot, plan.Target)
	if err != nil {
		relTarget = plan.Target
	}
	relNew, err := filepath.Rel(vaultRoot, plan.NewPath)
	if err != nil {
		relNew = plan.NewPath
	}
	if _, err := fmt.Fprintf(out, "rename: %s -> %s\n", relTarget, relNew); err != nil {
		return err
	}
	return printChanges(out, vaultRoot, plan.Changes)
}

// applyRenamePlan rewrites every affected file in order. The actual file
// rename of the target is performed by the caller after this returns
// successfully.
func applyRenamePlan(vaultRoot string, plan *renamePlan, fmCfg *config.FrontmatterConfig, out io.Writer) error {
	for _, file := range plan.FileOrder {
		if err := rewriteFile(file, plan.OldTitle, plan.NewTitle, fmCfg); err != nil {
			return fmt.Errorf("rename: rewrite %s: %w", file, err)
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

// rewriteFile applies RewriteTitle to the body and to every wikilink-typed
// frontmatter property, then atomically saves. The original note metadata
// (such as titles in plain frontmatter properties) is left untouched — only
// wikilink-typed values move.
func rewriteFile(path, oldTitle, newTitle string, fmCfg *config.FrontmatterConfig) error {
	n := &vault.Note{Path: path}
	if err := n.Load(); err != nil {
		return err
	}
	body, err := n.Body()
	if err != nil {
		return err
	}
	newBody := wikilink.RewriteTitle(body, oldTitle, newTitle)
	if newBody != body {
		if err := n.SetBody(newBody); err != nil {
			return err
		}
	}
	front, err := n.Front()
	if err != nil {
		return err
	}
	rewriteFrontmatter(front, oldTitle, newTitle, fmCfg)
	return n.Save(fmCfg)
}

// rewriteFrontmatter mutates front in place: every string value (scalar or
// list element) under a wikilink-typed key whose embedded title matches
// oldTitle is rewritten to newTitle. Delegates to the shared
// rewriteFrontmatterProps.
func rewriteFrontmatter(front *frontmatter.Map, oldTitle, newTitle string, fmCfg *config.FrontmatterConfig) {
	rewriteFrontmatterProps(front, oldTitle, newTitle, fmCfg, wikilink.RewriteTitle)
}

// renameFile moves target to newPath. When the two paths differ only in case
// the rename is performed via a temporary pivot so it works on filesystems
// that compare names case-insensitively (HFS+/APFS default).
func renameFile(target, newPath string) error {
	if target == newPath {
		return nil
	}
	if strings.EqualFold(target, newPath) && target != newPath {
		dir := filepath.Dir(target)
		tmp, err := os.CreateTemp(dir, ".rename-pivot-*")
		if err != nil {
			return fmt.Errorf("rename: tempfile in %q: %w", dir, err)
		}
		tmpPath := tmp.Name()
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		if err := renameFileRename(target, tmpPath); err != nil {
			return fmt.Errorf("rename: pivot %q -> %q: %w", target, tmpPath, err)
		}
		if err := renameFileRename(tmpPath, newPath); err != nil {
			if restoreErr := renameFileRename(tmpPath, target); restoreErr != nil {
				return fmt.Errorf("rename: pivot %q -> %q: %w; restore to %q failed: %v", tmpPath, newPath, err, target, restoreErr)
			}
			return fmt.Errorf("rename: pivot %q -> %q: %w", tmpPath, newPath, err)
		}
		return nil
	}
	if err := renameFileRename(target, newPath); err != nil {
		return fmt.Errorf("rename: %q -> %q: %w", target, newPath, err)
	}
	return nil
}
