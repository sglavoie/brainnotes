package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

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
				if err := noteops.ValidateRenameTitle(newTitle); err != nil {
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
			if err := noteops.ValidateRenameTitle(newTitle); err != nil {
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
			if err := noteops.EnsureUniqueRenameSourceTitle(v, target.Path, oldTitle); err != nil {
				return err
			}

			newPath, err := noteops.ComputeRenamePath(target.Path, newTitle, cfg)
			if err != nil {
				return err
			}

			if err := noteops.CheckRenameDestination(target.Path, newPath, force); err != nil {
				return err
			}
			if err := noteops.EnsureUniqueRenameDestinationTitle(v, target.Path, newPath, newTitle, force); err != nil {
				return err
			}

			plan, err := noteops.BuildRenamePlan(v, target.Path, newPath, oldTitle, newTitle, cfg)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if dryRun {
				return noteops.PrintRenamePlan(out, v.Root, plan)
			}
			journaler := noteops.NewJournal(v.Root, commandInvocation(cmd, args))
			if err := journaler.Capture(plan.Target); err != nil {
				return err
			}
			if err := journaler.Capture(plan.NewPath); err != nil {
				return err
			}
			for _, p := range plan.FileOrder {
				if err := journaler.Capture(p); err != nil {
					return err
				}
			}

			if err := noteops.ApplyRenamePlan(v.Root, plan, &cfg.Frontmatter, out); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "WARN: rename interrupted; run 'git diff' to inspect partial state")
				return err
			}
			if err := noteops.RenameFile(v.Root, plan.Target, plan.NewPath); err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "WARN: rename interrupted after content rewrites; run 'git diff' to inspect partial state")
				return err
			}
			if _, err := journaler.Finalize(); err != nil {
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
