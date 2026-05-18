package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

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

// NewMoveCmd returns the `move` subcommand. Exported for tests.
func NewMoveCmd() *cobra.Command {
	var (
		dryRun bool
		force  bool
	)

	cmd := &cobra.Command{
		Use:   "move <note> <new-folder>",
		Short: "Move a note to a different folder, rewriting folder-prefixed [[wikilinks]]",
		Long: `Move changes a note's containing folder. The filename (title) stays the same;
only the folder portion changes. Every folder-prefixed wikilink in the vault
that named the old folder/title — including aliased and headed forms — is
rewritten to the new folder. Bare [[Title]] links without a folder prefix are
left untouched (Obsidian resolves them by title alone, so they remain valid).

<note> may be a path or a bare title; bare titles are resolved case-insensitively
against the vault. <new-folder> is vault-relative ("." or empty means the vault
root); intermediate directories are created with mode 0755. Use 'bn rename' to
change the title.

Atomicity: each affected file is rewritten via a temp-file + rename. There is
no transaction across files: if a mid-apply error occurs, prior writes stand.
Inspect with 'git diff' to recover.`,
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("move: no config in context")
			}
			v, err := vault.Open(cfg)
			if err != nil {
				return err
			}

			noteArg := args[0]
			newFolderArg := args[1]

			target, err := resolveRenameTarget(v, noteArg)
			if err != nil {
				// resolveRenameTarget wraps with "rename: ..." — relabel.
				return fmt.Errorf("move: %s", strings.TrimPrefix(err.Error(), "rename: "))
			}

			title := target.Title()
			oldFolder, err := target.Folder(v.Root)
			if err != nil {
				return fmt.Errorf("move: %w", err)
			}

			newFolderRel, newFolderAbs, err := resolveNewFolder(v.Root, newFolderArg)
			if err != nil {
				return err
			}

			ext := filepath.Ext(target.Path)
			newPath := filepath.Join(newFolderAbs, title+ext)

			if err := checkMoveDestination(target.Path, newPath, force); err != nil {
				return err
			}

			oldRel := joinFolderTitle(oldFolder, title)
			newRel := joinFolderTitle(newFolderRel, title)
			if oldRel == newRel {
				return fmt.Errorf("move: destination is the same as source: %s", oldRel)
			}

			plan, err := buildMovePlan(v, target.Path, newPath, oldRel, newRel, cfg)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if dryRun {
				return printMovePlan(out, v.Root, plan)
			}

			if err := os.MkdirAll(newFolderAbs, 0o755); err != nil {
				return fmt.Errorf("move: mkdir %q: %w", newFolderAbs, err)
			}

			if err := applyMovePlan(v.Root, plan, &cfg.Frontmatter, out); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "WARN: move interrupted; run 'git diff' to inspect partial state")
				return err
			}
			if err := renameFile(plan.Target, plan.NewPath); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "WARN: move interrupted after content rewrites; run 'git diff' to inspect partial state")
				return err
			}
			_, err = fmt.Fprintf(out, "moved: %s -> %s\n", plan.Target, plan.NewPath)
			return err
		},
	}

	f := cmd.Flags()
	f.BoolVar(&dryRun, "dry-run", false, "print the plan without modifying any files")
	f.BoolVar(&force, "force", false, "overwrite the destination file if it already exists")
	return cmd
}

// resolveNewFolder normalizes the <new-folder> argument. Returns the vault-
// relative slash-separated folder path (empty for the vault root) and its
// absolute equivalent. Paths that escape the vault are rejected.
func resolveNewFolder(vaultRoot, arg string) (rel, abs string, err error) {
	if arg == "" {
		return "", vaultRoot, nil
	}
	cleaned := filepath.Clean(filepath.FromSlash(arg))
	if cleaned == "." {
		return "", vaultRoot, nil
	}
	var fullPath string
	if filepath.IsAbs(cleaned) {
		fullPath = cleaned
	} else {
		fullPath = filepath.Join(vaultRoot, cleaned)
	}
	relP, relErr := filepath.Rel(vaultRoot, fullPath)
	if relErr != nil {
		return "", "", fmt.Errorf("move: resolve %q: %w", arg, relErr)
	}
	if relP == ".." || strings.HasPrefix(relP, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("move: new folder %q is outside the vault", arg)
	}
	if relP == "." {
		return "", vaultRoot, nil
	}
	if info, statErr := os.Stat(fullPath); statErr == nil && !info.IsDir() {
		return "", "", fmt.Errorf("move: new folder %q exists but is not a directory", arg)
	}
	return filepath.ToSlash(relP), fullPath, nil
}

// joinFolderTitle returns folder + "/" + title, or just title when folder is
// empty. Folders are slash-separated to match wikilink Target syntax.
func joinFolderTitle(folder, title string) string {
	if folder == "" {
		return title
	}
	return folder + "/" + title
}

// checkMoveDestination errors when newPath already names a different file
// unless force is set. A case-only move on a case-insensitive filesystem
// (where stat reports the same inode) is allowed.
func checkMoveDestination(targetPath, newPath string, force bool) error {
	if newPath == targetPath {
		return fmt.Errorf("move: new path is identical to current path: %s", newPath)
	}
	newInfo, err := os.Lstat(newPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("move: stat %q: %w", newPath, err)
	}
	targetInfo, terr := os.Lstat(targetPath)
	if terr == nil && os.SameFile(targetInfo, newInfo) {
		return nil
	}
	if force {
		return nil
	}
	return fmt.Errorf("move: destination %s already exists (use --force to overwrite)", newPath)
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
	addFile := func(p string) {
		if _, ok := seenFiles[p]; ok {
			return
		}
		seenFiles[p] = struct{}{}
		plan.FileOrder = append(plan.FileOrder, p)
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
// after applying RewriteMove. Line numbers are 1-based.
func scanBodyMove(file, body, oldRel, newRel string) []renameChange {
	if body == "" {
		return nil
	}
	var changes []renameChange
	line := 1
	start := 0
	for i := 0; i <= len(body); i++ {
		if i == len(body) || body[i] == '\n' {
			lineText := body[start:i]
			newLineText := wikilink.RewriteMove(lineText, oldRel, newRel)
			if newLineText != lineText {
				changes = append(changes, renameChange{
					File:    file,
					Kind:    changeBody,
					Line:    line,
					OldText: lineText,
					NewText: newLineText,
				})
			}
			line++
			start = i + 1
		}
	}
	return changes
}

// scanFrontmatterMove inspects every wikilink-typed property and records a
// change when any string value (scalar or list element) contains a folder-
// prefixed link whose Target equals oldRel.
func scanFrontmatterMove(file string, front *frontmatter.Map, oldRel, newRel string, wikilinkProps []string) []renameChange {
	var changes []renameChange
	for _, key := range wikilinkProps {
		v, ok := front.Get(key)
		if !ok {
			continue
		}
		switch vv := v.(type) {
		case string:
			ns := wikilink.RewriteMove(vv, oldRel, newRel)
			if ns != vv {
				changes = append(changes, renameChange{
					File:    file,
					Kind:    changeFrontmatter,
					Key:     key,
					OldText: vv,
					NewText: ns,
				})
			}
		case []any:
			for _, item := range vv {
				s, ok := item.(string)
				if !ok {
					continue
				}
				ns := wikilink.RewriteMove(s, oldRel, newRel)
				if ns != s {
					changes = append(changes, renameChange{
						File:    file,
						Kind:    changeFrontmatter,
						Key:     key,
						OldText: s,
						NewText: ns,
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
	for _, c := range sorted {
		rel, err := filepath.Rel(vaultRoot, c.File)
		if err != nil {
			rel = c.File
		}
		switch c.Kind {
		case changeBody:
			if _, err := fmt.Fprintf(out, "%s:%d: %s -> %s\n", rel, c.Line, c.OldText, c.NewText); err != nil {
				return err
			}
		case changeFrontmatter:
			if _, err := fmt.Fprintf(out, "%s:frontmatter[%s]: %s -> %s\n", rel, c.Key, c.OldText, c.NewText); err != nil {
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
	n := &vault.Note{Path: path}
	if err := n.Load(); err != nil {
		return err
	}
	body, err := n.Body()
	if err != nil {
		return err
	}
	newBody := wikilink.RewriteMove(body, oldRel, newRel)
	if newBody != body {
		if err := n.SetBody(newBody); err != nil {
			return err
		}
	}
	front, err := n.Front()
	if err != nil {
		return err
	}
	rewriteFrontmatterMove(front, oldRel, newRel, fmCfg)
	return n.Save(fmCfg)
}

// rewriteFrontmatterMove mutates front in place: every string value (scalar or
// list element) under a wikilink-typed key whose embedded Target equals oldRel
// is rewritten to newRel.
func rewriteFrontmatterMove(front *frontmatter.Map, oldRel, newRel string, fmCfg *config.FrontmatterConfig) {
	if fmCfg == nil {
		return
	}
	for _, key := range fmCfg.WikilinkProperties {
		v, ok := front.Get(key)
		if !ok {
			continue
		}
		switch vv := v.(type) {
		case string:
			ns := wikilink.RewriteMove(vv, oldRel, newRel)
			if ns != vv {
				front.Set(key, ns)
			}
		case []any:
			changed := false
			newList := make([]any, len(vv))
			for i, item := range vv {
				if s, ok := item.(string); ok {
					ns := wikilink.RewriteMove(s, oldRel, newRel)
					if ns != s {
						changed = true
					}
					newList[i] = ns
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
