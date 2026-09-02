package cli

import (
	"github.com/spf13/cobra"
)

// NewUndoCmd returns the `undo` subcommand. Exported for tests.
func NewUndoCmd() *cobra.Command {
	var opts undoOptions

	cmd := &cobra.Command{
		Use:   "undo [id]",
		Short: "Restore vault files from an undo journal entry",
		Long: `Undo restores files touched by one journal entry, using snapshots captured
before the source command ran. Use 'bn history' to choose an entry ID.

If [id] is omitted, undo targets the most recent journal entry.

'bn undo --dry-run' previews deterministic restore actions:
  undo: <entry-id> (<source-command>)
  --- <path>
  - state: <current-state>
  + state: <restored-state>

By default, undo is fail-fast: if any touched path no longer matches the
journaled post-command state, undo aborts with no writes. Use --force to
overwrite conflicts.

Atomicity: restore stages every snapshot and re-checks every precondition
before the first file moves, then applies them all. If a later file fails,
the already-applied files are rolled back to the state they had before the
restore started. Only a failed rollback can leave mixed state, and the error
names the affected paths; use 'git diff' to recover.

v1 limits: restore is whole-entry only (no per-file interactive restore). No
redo stack or conflict auto-merge; use git for long-term history.`,
		Example: `  bn history
  bn undo
  bn undo 20260102T030405.000000000Z-4f7aa81d --dry-run
  bn undo 20260102T030405.000000000Z-4f7aa81d --force`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUndoCommand(cmd, args, opts)
		},
	}

	f := cmd.Flags()
	f.BoolVar(&opts.dryRun, "dry-run", false, "preview restore actions without modifying files")
	f.BoolVar(&opts.force, "force", false, "override restore conflicts and overwrite current files")
	return cmd
}
