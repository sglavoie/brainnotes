package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

const undoPartialFailureGuidance = "undo: restore stages every file before applying any and rolls applied files back on failure; if the error reports a failed rollback, inspect 'git diff' to recover"

// NewUndoCmd returns the `undo` subcommand. Exported for tests.
func NewUndoCmd() *cobra.Command {
	var (
		dryRun bool
		force  bool
	)

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
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("undo: no config in context")
			}

			entryID := ""
			if len(args) == 1 {
				entryID = strings.TrimSpace(args[0])
				if entryID == "" {
					return errors.New("undo: empty entry id")
				}
			} else {
				id, warnings, err := latestJournalEntryID(journalRootForVault(cfg.Vault))
				if err != nil {
					return err
				}
				// "Most recent entry" now means "most recent *readable* entry", so
				// say which entries were passed over. Silently undoing an older
				// command than the one the user just ran is the failure mode worth
				// spending two lines of stderr on. A named entry ID never reaches
				// here: that request stays a direct error.
				if err := writeHistoryWarnings(cmd.ErrOrStderr(), warnings); err != nil {
					return err
				}
				entryID = id
			}

			plan, err := noteops.Undo(cfg.Vault, entryID, noteops.UndoOptions{
				Force:   force,
				DryRun:  dryRun,
				Command: commandInvocation(cmd, args),
			})
			if err != nil {
				var conflictErr *journal.RestoreConflictError
				if errors.As(err, &conflictErr) {
					return fmt.Errorf("undo: %s", renderUndoConflict(conflictErr))
				}
				// The partial-failure guidance is about a restore that stopped
				// partway, and it is the wrong thing to say about a restore that
				// finished: a snapshot failure means the files are already back to
				// their earlier state, so telling the user to inspect a rollback
				// would send them looking for damage that is not there.
				if noteops.IsSnapshotFailure(err) {
					return fmt.Errorf("undo: %w", err)
				}
				return fmt.Errorf("undo: %w\n%s", err, undoPartialFailureGuidance)
			}

			if err := printUndoPlan(cmd.OutOrStdout(), plan); err != nil {
				return err
			}
			if !dryRun {
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "restored %d path(s)\n", len(plan.Changes))
				return err
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.BoolVar(&dryRun, "dry-run", false, "preview restore actions without modifying files")
	f.BoolVar(&force, "force", false, "override restore conflicts and overwrite current files")
	return cmd
}

func latestJournalEntryID(journalRoot string) (string, []journal.Warning, error) {
	entries, warnings, err := journal.ListEntries(journalRoot)
	if err != nil {
		return "", nil, fmt.Errorf("undo: %w", err)
	}
	if len(entries) == 0 {
		return "", warnings, errors.New("undo: no journal entries found")
	}
	return entries[0].ID, warnings, nil
}

func printUndoPlan(w io.Writer, plan *journal.RestorePlan) error {
	if plan == nil {
		return errors.New("undo: empty restore plan")
	}
	if _, err := fmt.Fprintf(w, "undo: %s (%s)\n", plan.EntryID, plan.Command); err != nil {
		return err
	}
	for _, change := range plan.Changes {
		if _, err := fmt.Fprintf(w, "--- %s\n", change.Path); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "- state: %s\n", change.CurrentState); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "+ state: %s\n", change.RestoredState); err != nil {
			return err
		}
	}
	return nil
}

func renderUndoConflict(err *journal.RestoreConflictError) string {
	if err == nil {
		return "restore aborted due to conflicts"
	}
	lines := make([]string, 0, len(err.Conflicts)+2)
	lines = append(lines, fmt.Sprintf("restore %s aborted due to conflicts:", err.EntryID))
	for _, c := range err.Conflicts {
		lines = append(lines, fmt.Sprintf("  - %s expected=%s actual=%s", c.Path, c.Expected, c.Actual))
	}
	lines = append(lines, "re-run with --force to overwrite current files")
	return strings.Join(lines, "\n")
}
