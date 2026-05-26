package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
)

const undoPartialFailureGuidance = "undo: restore can leave partial state after apply errors; inspect 'git diff' to recover"

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

Atomicity: each file rewrite is temp-file + rename. There is no cross-file
transaction; if an apply error interrupts restore, prior writes stand. Use
'git diff' to inspect and recover partial state.

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
				id, err := latestJournalEntryID(journalRootForVault(cfg.Vault))
				if err != nil {
					return err
				}
				entryID = id
			}

			plan, err := journal.Restore(cfg.Vault, journalRootForVault(cfg.Vault), entryID, journal.RestoreOptions{
				Force:  force,
				DryRun: dryRun,
			})
			if err != nil {
				var conflictErr *journal.RestoreConflictError
				if errors.As(err, &conflictErr) {
					return fmt.Errorf("undo: %s", renderUndoConflict(conflictErr))
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

func latestJournalEntryID(journalRoot string) (string, error) {
	entries, err := journal.ListEntries(journalRoot)
	if err != nil {
		return "", fmt.Errorf("undo: %w", err)
	}
	if len(entries) == 0 {
		return "", errors.New("undo: no journal entries found")
	}
	return entries[0].ID, nil
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
