package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// renameChangeKind tags a change with the locus that was rewritten so the
// dry-run output can render an informative source location.
type renameChangeKind int

const (
	changeBody renameChangeKind = iota
	changeFrontmatter
)

// renameChange is one line-level edit produced by the pre-scan. Body changes
// carry the 1-based line number within the note; frontmatter changes carry the
// affected property key instead.
type renameChange struct {
	File    string
	Kind    renameChangeKind
	Line    int    // body only
	Key     string // frontmatter only
	OldText string
	NewText string
}

// renamePlan is the full set of edits required to rename a note plus rewrite
// every reference to it. NewPath is the on-disk path the target file will be
// moved to once the plan is applied.
type renamePlan struct {
	Target    string
	NewPath   string
	OldTitle  string
	NewTitle  string
	Changes   []renameChange
	FileOrder []string // affected files in walk order, for deterministic apply
}

// NewRenameCmd returns the `rename` subcommand. Exported for tests.
func NewRenameCmd() *cobra.Command {
	var (
		dryRun bool
		force  bool
	)

	cmd := &cobra.Command{
		Use:   "rename [note] <new-title>",
		Short: "Rename a note and rewrite every [[wikilink]] pointing at it",
		Long: `Rename changes a note's filename to <new-title>.md (same folder) and rewrites
every wikilink in the vault that pointed at the old title — including
folder-prefixed forms (e.g. [[Books/OldTitle]]), aliased forms ([[OldTitle|Alt]]),
heading forms ([[OldTitle#Section]]), and wikilink-typed frontmatter values
(categories, parent, ...).

<note> may be a path or a bare title; bare titles are resolved
case-insensitively against the vault. When omitted, an fzf picker lets you
select the note interactively.

Atomicity: each affected file is rewritten via a temp-file + rename. There is
no transaction across files: if a mid-apply error occurs, prior writes stand.
Inspect with 'git diff' to recover.`,
		Args:          cobra.RangeArgs(1, 2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("rename: no config in context")
			}
			v, err := vault.Open(cfg)
			if err != nil {
				return err
			}

			var noteArg, newTitle string
			if len(args) == 2 {
				noteArg = args[0]
				newTitle = args[1]
			} else {
				newTitle = args[0]
				if err := validateNewTitle(newTitle); err != nil {
					return err
				}
				selected, pickErr := pickNotes(v, cfg, false)
				if pickErr != nil {
					return pickErr
				}
				if len(selected) == 0 {
					return nil
				}
				noteArg = selected[0]
			}
			if err := validateNewTitle(newTitle); err != nil {
				return err
			}

			target, err := resolveRenameTarget(v, noteArg)
			if err != nil {
				return err
			}

			oldTitle := target.Title()
			if oldTitle == newTitle {
				return fmt.Errorf("rename: new title %q is identical to current title", newTitle)
			}
			if err := ensureUniqueRenameSourceTitle(v, target.Path, oldTitle); err != nil {
				return err
			}

			newPath, err := computeNewPath(target.Path, newTitle, cfg)
			if err != nil {
				return err
			}

			if err := checkDestination(target.Path, newPath, force); err != nil {
				return err
			}

			plan, err := buildRenamePlan(v, target.Path, newPath, oldTitle, newTitle, cfg)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if dryRun {
				return printPlan(out, v.Root, plan)
			}

			if err := applyRenamePlan(v.Root, plan, &cfg.Frontmatter, out); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "WARN: rename interrupted; run 'git diff' to inspect partial state")
				return err
			}
			if err := renameFile(plan.Target, plan.NewPath); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "WARN: rename interrupted after content rewrites; run 'git diff' to inspect partial state")
				return err
			}
			rel, relErr := filepath.Rel(v.Root, plan.NewPath)
			if relErr != nil {
				rel = plan.NewPath
			}
			_, err = fmt.Fprintf(out, "renamed: %s -> %s\n", plan.Target, filepath.Join(v.Root, rel))
			return err
		},
	}

	f := cmd.Flags()
	f.BoolVar(&dryRun, "dry-run", false, "print the plan without modifying any files")
	f.BoolVar(&force, "force", false, "overwrite the destination file if it already exists")
	return cmd
}

// validateNewTitle rejects titles that would map to an invalid filename: empty
// or whitespace-only, contain `/` (use `move`), contain a NUL byte or any
// other ASCII control character (including newline/tab), have leading or
// trailing whitespace, or begin with `.` (hidden).
func validateNewTitle(t string) error {
	if t == "" {
		return errors.New("rename: empty new title")
	}
	if strings.TrimSpace(t) == "" {
		return errors.New("rename: new title may not be whitespace-only")
	}
	if t != strings.TrimSpace(t) {
		return errors.New("rename: new title may not have leading or trailing whitespace")
	}
	if strings.ContainsRune(t, '/') {
		return errors.New("rename: new title may not contain '/'; use 'bn move' to change folders")
	}
	for _, r := range t {
		if r == 0 {
			return errors.New("rename: new title contains a NUL byte")
		}
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("rename: new title contains a control character (U+%04X)", r)
		}
	}
	if strings.HasPrefix(t, ".") {
		return errors.New("rename: new title may not start with '.'")
	}
	return nil
}

// resolveRenameTarget treats arg as a path when it looks like one (contains a
// path separator, is absolute, or ends in .md and exists), otherwise resolves
// it case-insensitively as a bare title.
func resolveRenameTarget(v *vault.Vault, arg string) (*vault.Note, error) {
	looksLikePath := filepath.IsAbs(arg) ||
		strings.ContainsRune(arg, filepath.Separator) ||
		strings.HasSuffix(strings.ToLower(arg), ".md")
	if looksLikePath {
		abs, err := absInVault(v.Root, arg)
		if err != nil {
			return nil, fmt.Errorf("rename: %w", err)
		}
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("rename: %w", err)
		}
		return &vault.Note{Path: abs}, nil
	}
	n, err := v.ResolveByTitle(arg, "")
	if err != nil {
		return nil, fmt.Errorf("rename: %w", err)
	}
	return n, nil
}

// computeNewPath returns the destination path: same directory as target, new
// filename = <newTitle><source-extension>. The source file's extension is
// preserved so a `.markdown` or other non-default-extension note keeps its
// extension across a rename. This mirrors `bn move`, which also reuses
// filepath.Ext(target.Path). Falls back to cfg.Templates.DefaultExtension
// (then ".md") when the source has no extension.
func computeNewPath(targetPath, newTitle string, cfg *config.Config) (string, error) {
	dir := filepath.Dir(targetPath)
	ext := filepath.Ext(targetPath)
	if ext == "" {
		ext = cfg.Templates.DefaultExtension
		if ext == "" {
			ext = ".md"
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
	}
	return filepath.Join(dir, newTitle+ext), nil
}

// checkDestination errors when newPath already names a different file unless
// --force is set. A case-only rename on a case-insensitive filesystem (where
// stat returns the same file) is allowed.
func checkDestination(targetPath, newPath string, force bool) error {
	if newPath == targetPath {
		return fmt.Errorf("rename: new path is identical to current path: %s", newPath)
	}
	newInfo, err := os.Lstat(newPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("rename: stat %q: %w", newPath, err)
	}
	targetInfo, terr := os.Lstat(targetPath)
	if terr == nil && os.SameFile(targetInfo, newInfo) {
		// Case-only rename on case-insensitive filesystem — allowed.
		return nil
	}
	if newInfo.IsDir() {
		// A directory at the destination is never a valid rename target:
		// os.Rename cannot replace a non-empty directory with a regular file,
		// so --force would still fail mid-apply (after wikilink rewrites had
		// already touched other notes). Reject upfront with a clear error.
		return fmt.Errorf("rename: destination %s is an existing directory", newPath)
	}
	if force {
		return nil
	}
	return fmt.Errorf("rename: destination %s already exists (use --force to overwrite)", newPath)
}

// buildRenamePlan walks the vault and collects every file that needs an edit.
// Both body wikilinks and wikilink-typed frontmatter properties are scanned.
// The renamed note itself is included so self-references update.
func buildRenamePlan(v *vault.Vault, targetPath, newPath, oldTitle, newTitle string, cfg *config.Config) (*renamePlan, error) {
	plan := &renamePlan{
		Target:   targetPath,
		NewPath:  newPath,
		OldTitle: oldTitle,
		NewTitle: newTitle,
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
		bodyChanges := scanBodyChanges(n.Path, body, oldTitle, newTitle)
		if len(bodyChanges) > 0 {
			plan.Changes = append(plan.Changes, bodyChanges...)
			addFile(n.Path)
		}

		front, err := n.Front()
		if err != nil {
			return err
		}
		fmChanges := scanFrontmatterChanges(n.Path, front, oldTitle, newTitle, cfg.Frontmatter.WikilinkProperties)
		if len(fmChanges) > 0 {
			plan.Changes = append(plan.Changes, fmChanges...)
			addFile(n.Path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("rename: scan vault: %w", err)
	}
	return plan, nil
}

func ensureUniqueRenameSourceTitle(v *vault.Vault, targetPath, title string) error {
	var duplicates []string
	err := v.Walk(vault.WalkFilter{}, func(n *vault.Note) error {
		if n.Path == targetPath || !strings.EqualFold(n.Title(), title) {
			return nil
		}
		rel, err := filepath.Rel(v.Root, n.Path)
		if err != nil {
			rel = n.Path
		}
		duplicates = append(duplicates, rel)
		return nil
	})
	if err != nil {
		return fmt.Errorf("rename: scan vault for duplicate titles: %w", err)
	}
	if len(duplicates) == 0 {
		return nil
	}
	sort.Strings(duplicates)
	return fmt.Errorf("rename: title %q is duplicated elsewhere in the vault (%s); refusing title-only rewrite", title, strings.Join(duplicates, ", "))
}
