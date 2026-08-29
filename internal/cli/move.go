package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

type moveOptions struct {
	dryRun bool
	force  bool
}

// NewMoveCmd returns the `move` subcommand. Exported for tests.
func NewMoveCmd() *cobra.Command {
	opts := &moveOptions{}

	cmd := &cobra.Command{
		Use:   "move [note] <new-folder>",
		Short: "Move a note to a different folder, rewriting folder-prefixed [[wikilinks]]",
		Long: `Move changes a note's containing folder. The filename (title) stays the same;
only the folder portion changes. Every folder-prefixed wikilink in the vault
that named the old folder/title — including aliased and headed forms — is
rewritten to the new folder. Bare [[Title]] links without a folder prefix are
left untouched (Obsidian resolves them by title alone, so they remain valid).

<note> may be a path or a bare title; bare titles are resolved case-insensitively
against the vault. When omitted, an fzf picker lets you select the note
interactively. <new-folder> is vault-relative ("." or empty means the vault
root); intermediate directories are created with mode 0755. Use 'bn rename' to
change the title.

Atomicity: each affected file is rewritten via a temp-file + rename. There is
no transaction across files: if a mid-apply error occurs, prior writes stand.
Inspect with 'git diff' to recover.`,
		Args:          cobra.RangeArgs(1, 2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMove(cmd, args, opts)
		},
	}

	flags := cmd.Flags()
	flags.BoolVar(&opts.dryRun, "dry-run", false, "print the plan without modifying any files")
	flags.BoolVar(&opts.force, "force", false, "overwrite the destination file if it already exists")
	return cmd
}

func runMove(cmd *cobra.Command, args []string, opts *moveOptions) error {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("move: no config in context")
	}
	v, err := vault.Open(cfg)
	if err != nil {
		return err
	}

	var noteArg, newFolderArg string
	if len(args) == 2 {
		noteArg = args[0]
		newFolderArg = args[1]
	} else {
		newFolderArg = args[0]
		selected, pickErr := pickNotes(v, cfg, false)
		if pickErr != nil {
			return pickErr
		}
		if len(selected) == 0 {
			return nil
		}
		noteArg = selected[0]
	}

	target, err := resolveMoveTarget(v, noteArg)
	if err != nil {
		return err
	}

	title := target.Title()
	oldFolder, err := target.Folder(v.Root)
	if err != nil {
		return fmt.Errorf("move: %w", err)
	}

	newFolderRel, newFolderAbs, err := noteops.ResolveNewFolder(v.Root, newFolderArg)
	if err != nil {
		return err
	}

	newPath := filepath.Join(newFolderAbs, title+filepath.Ext(target.Path))
	if err := noteops.CheckMoveDestination(target.Path, newPath, opts.force); err != nil {
		return err
	}

	oldRel := noteops.JoinFolderTitle(oldFolder, title)
	newRel := noteops.JoinFolderTitle(newFolderRel, title)
	if err := noteops.ValidateMoveTargetPath(newFolderArg, newFolderRel); err != nil {
		return err
	}
	if oldRel == newRel {
		return fmt.Errorf("move: destination is the same as source: %s", oldRel)
	}

	plan, err := noteops.BuildMovePlan(v, target.Path, newPath, oldRel, newRel, cfg)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if opts.dryRun {
		return noteops.PrintMovePlan(out, v.Root, plan)
	}
	// Ahead of the destination folder, which a refused move would otherwise
	// leave behind empty.
	journaler, err := noteops.BeginOperation(v.Root, "move", commandInvocation(cmd, args),
		[]string{plan.Target, plan.NewPath, newFolderAbs})
	if err != nil {
		return err
	}
	// BeginOperation above named this folder as a target, so a refused move never
	// leaves it behind empty.
	//bn:direct-write guarded by BeginOperation above
	if err := os.MkdirAll(newFolderAbs, 0o755); err != nil {
		return fmt.Errorf("move: mkdir %q: %w", newFolderAbs, err)
	}
	if err := noteops.CheckMoveDestination(plan.Target, plan.NewPath, opts.force); err != nil {
		return err
	}
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

	if err := noteops.ApplyMovePlan(v.Root, plan, &cfg.Frontmatter, out); err != nil {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "WARN: move interrupted; run 'git diff' to inspect partial state")
		return err
	}
	if err := noteops.RenameFile(v.Root, plan.Target, plan.NewPath); err != nil {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "WARN: move interrupted after content rewrites; run 'git diff' to inspect partial state")
		return err
	}
	if _, err := journaler.Finalize(); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "moved: %s -> %s\n", plan.Target, plan.NewPath)
	return err
}
