package cli

import (
	"github.com/spf13/cobra"
)

// NewRenameCmd returns the `rename` subcommand. Exported for tests.
func NewRenameCmd() *cobra.Command {
	var opts renameOptions

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
			return runRenameCommand(cmd, args, opts)
		},
	}

	f := cmd.Flags()
	f.BoolVar(&opts.dryRun, "dry-run", false, "print the plan without modifying any files")
	f.BoolVar(&opts.force, "force", false, "overwrite the destination file if it already exists")
	return cmd
}
