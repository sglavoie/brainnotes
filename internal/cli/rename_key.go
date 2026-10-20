package cli

import (
	"github.com/spf13/cobra"
)

// NewRenameKeyCmd returns the `rename-key` subcommand. Exported for tests.
func NewRenameKeyCmd() *cobra.Command {
	opts := renameKeyOptions{ff: new(findFlags)}

	cmd := &cobra.Command{
		Use:   "rename-key <old-key> <new-key> [note...]",
		Short: "Rename a frontmatter key across the vault or a scoped subset",
		Long: `Rename-key changes a frontmatter key from <old-key> to <new-key> in every
matching note. The value is preserved exactly (no type coercion, no wikilink
wrapping). Notes where <old-key> is absent are skipped silently. Notes where
<new-key> already exists are skipped with a warning.

The scope is selected as follows:

  - No notes and no find flags: every note in the vault.
  - One or more find flags (--tag, --folder, ...): notes matching the filter.
  - Positional notes: only those notes (mutually exclusive with find flags).

Atomicity: each file is rewritten in place, never vacating its own name. There
is no transaction across files: if a mid-apply error occurs, prior writes stand
and the unfinished entry cannot be undone; use 'git diff' to recover. A completed
run is one journal entry, so 'bn undo' reverts the whole run.

Use --dry-run to preview changes without writing.`,
		Args:          cobra.MinimumNArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRenameKeyCommand(cmd, args, opts)
		},
	}

	f := cmd.Flags()
	opts.ff.register(cmd)
	f.BoolVar(&opts.dryRun, "dry-run", false, "preview changes without writing")

	return cmd
}
