package cli

import (
	"github.com/spf13/cobra"
)

// NewRelinkCmd returns the `relink` subcommand. Exported for tests.
func NewRelinkCmd() *cobra.Command {
	opts := relinkOptions{ff: new(findFlags)}

	cmd := &cobra.Command{
		Use:   "relink [note...]",
		Short: "Repair unresolved wikilinks with safe deterministic rewrites",
		Long: `Relink scans unresolved wikilinks and applies only deterministic rewrites.

Safe rewrites in v1:
  - exact path/key match after path normalization (e.g. backslashes -> '/')
  - exact title match (case-insensitive, optional .md) when unique

Ambiguous targets are skipped and reported for manual follow-up.

Scope:
  - No notes and no find flags: every note in the vault.
  - One or more find flags (--tag, --folder, ...): matching notes only.
  - Positional notes: only those notes (mutually exclusive with find flags).

Atomicity: each file is rewritten via a temp-file + rename, and the run is one
undo journal entry: reverse a completed run with 'bn undo' (entry IDs:
'bn history'). There is no transaction across files: if a mid-apply error
occurs, prior writes stand and the unfinished entry cannot be undone. Use
'git diff' to inspect or recover.

Use --dry-run to preview changes without writing.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRelinkCommand(cmd, args, opts)
		},
	}

	opts.ff.register(cmd)
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "preview changes without writing")
	return cmd
}
