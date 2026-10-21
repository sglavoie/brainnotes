package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/vault"
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

Atomicity: the move is one undo journal entry. Each affected file is
rewritten via a temp-file + rename, and the destination is re-checked just
before the first rewrite. If a rewrite or the final move fails, every backlink
already rewritten is put back, so the vault is left as it was found; only a
put-back that is itself refused (a note edited meanwhile) leaves rewritten
links behind, and the error names those notes. Reverse a completed move with
'bn undo' (entry IDs: 'bn history').`,
		Example: `  bn move References/Books/Dune.md References --dry-run
  bn move Dune References/Books        # by bare title
  bn move Old/Foo.md .                 # back to the vault root
  bn move Archive                      # pick the note with fzf`,
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

	noteArg, newFolderArg, ok, err := resolveMoveArgs(v, cfg, args)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	plan, newFolderAbs, err := planMove(v, cfg, noteArg, newFolderArg, opts.force)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if opts.dryRun {
		return noteops.PrintMovePlan(out, v.Root, plan)
	}
	return applyMove(cmd, args, v, cfg, plan, newFolderAbs, opts.force, out)
}

// resolveMoveArgs maps the one- and two-argument forms onto (note, new folder).
// The one-argument form opens the picker; a dismissed picker reports ok=false so
// the caller returns without moving anything.
func resolveMoveArgs(v *vault.Vault, cfg *config.Config, args []string) (noteArg, newFolderArg string, ok bool, err error) {
	if len(args) == 2 {
		return args[0], args[1], true, nil
	}
	selected, err := pickNotes("move", v, cfg, false)
	if err != nil {
		return "", "", false, err
	}
	if len(selected) == 0 {
		return "", "", false, nil
	}
	return selected[0], args[0], true, nil
}

// planMove runs every refusal and builds the plan. Nothing on disk changes here,
// which is what makes --dry-run one `if` away from the apply path. The absolute
// destination folder is returned alongside the plan because the apply path has
// to name it to BeginOperation before creating it.
func planMove(v *vault.Vault, cfg *config.Config, noteArg, newFolderArg string, force bool) (*noteops.MovePlan, string, error) {
	target, err := resolveMoveTarget(v, noteArg)
	if err != nil {
		return nil, "", err
	}

	title := target.Title()
	oldFolder, err := target.Folder(v.Root)
	if err != nil {
		return nil, "", fmt.Errorf("move: %w", err)
	}

	newFolderRel, newFolderAbs, err := noteops.ResolveNewFolder(v.Root, newFolderArg)
	if err != nil {
		return nil, "", err
	}

	newPath := filepath.Join(newFolderAbs, title+filepath.Ext(target.Path))
	if err := noteops.CheckMoveDestination(target.Path, newPath, force); err != nil {
		return nil, "", err
	}

	oldRel := noteops.JoinFolderTitle(oldFolder, title)
	newRel := noteops.JoinFolderTitle(newFolderRel, title)
	if err := noteops.ValidateMoveTargetPath(newFolderArg, newFolderRel); err != nil {
		return nil, "", err
	}
	if oldRel == newRel {
		return nil, "", fmt.Errorf("move: destination is the same as source: %s", oldRel)
	}

	plan, err := noteops.BuildMovePlan(v, target.Path, newPath, oldRel, newRel, cfg)
	if err != nil {
		return nil, "", err
	}
	return plan, newFolderAbs, nil
}

// applyMove is the only writing path. The destination check runs a second time
// here, after the journal exists, because the plan was built before it, and
// CommitMovePlan runs it a last time right before the first rewrite.
func applyMove(cmd *cobra.Command, args []string, v *vault.Vault, cfg *config.Config, plan *noteops.MovePlan, newFolderAbs string, force bool, out io.Writer) error {
	// Ahead of the destination folder, which a refused move would otherwise
	// leave behind empty.
	journaler, err := noteops.BeginOperationContext(commandContext(cmd), v.Root, "move", commandInvocation(cmd, args),
		[]string{plan.Target, plan.NewPath, newFolderAbs})
	if err != nil {
		return err
	}
	defer journaler.Release()
	// BeginOperation above named this folder as a target, so a refused move never
	// leaves it behind empty.
	//bn:direct-write guarded by BeginOperation above
	if err := os.MkdirAll(newFolderAbs, 0o755); err != nil {
		return fmt.Errorf("move: mkdir %q: %w", newFolderAbs, err)
	}
	if err := noteops.CheckMoveDestination(plan.Target, plan.NewPath, force); err != nil {
		return err
	}
	if err := captureMovePaths(journaler, plan); err != nil {
		return err
	}

	relocate := noteops.RelocationOptions{Force: force, Out: out}
	if err := noteops.CommitMovePlan(journaler, v.Root, plan, &cfg.Frontmatter, relocate); err != nil {
		warnIncompleteRelocation(cmd, "move", err)
		return err
	}
	if _, err := journaler.Finalize(); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "moved: %s -> %s\n", plan.Target, plan.NewPath)
	return err
}

// captureMovePaths records the pre-image of the source, the destination, and
// every note whose links the move rewrites, in that order.
func captureMovePaths(journaler *noteops.Journal, plan *noteops.MovePlan) error {
	paths := append([]string{plan.Target, plan.NewPath}, plan.FileOrder...)
	for _, p := range paths {
		if err := journaler.Capture(p); err != nil {
			return err
		}
	}
	return nil
}
