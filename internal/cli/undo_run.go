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

// undoOptions is the flag state of one `bn undo` invocation.
//
// There is no validate(): --dry-run and --force compose. Forcing a preview is a
// legitimate way to see what an overriding restore would do, so an empty method
// would be ceremony rather than the place a rule goes.
type undoOptions struct {
	dryRun bool
	force  bool
}

// runUndoCommand resolves the entry to restore and hands it to noteops.Undo.
//
// The restore itself is entirely `internal/noteops` and `internal/journal`
// work: this function chooses an entry ID, passes the two flags through
// unchanged, translates the three failure shapes into user-facing text, and
// prints the plan. Nothing here stages, applies, or rolls back a file.
func runUndoCommand(cmd *cobra.Command, args []string, opts undoOptions) error {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("undo: no config in context")
	}

	entryID, err := resolveUndoEntryID(cmd, cfg.Vault, args)
	if err != nil {
		return err
	}

	plan, err := noteops.Undo(cfg.Vault, entryID, noteops.UndoOptions{
		Force:   opts.force,
		DryRun:  opts.dryRun,
		Command: commandInvocation(cmd, args),
	})
	if err != nil {
		return describeUndoError(err)
	}

	if err := printUndoPlan(cmd.OutOrStdout(), plan); err != nil {
		return err
	}
	if opts.dryRun {
		return nil
	}
	// Stderr, and not an error: the restore landed, was journaled and was
	// snapshotted, and only a transaction temp file outlived it. Failing over it
	// would say the content did not come back; staying silent would leave the one
	// file nothing else will ever mention again.
	if plan.Cleanup != nil {
		if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", plan.Cleanup); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "restored %d path(s)\n", len(plan.Changes))
	return err
}

// resolveUndoEntryID returns the journal entry to restore: the trimmed
// positional argument, or the most recent readable entry when there is none.
func resolveUndoEntryID(cmd *cobra.Command, vaultRoot string, args []string) (string, error) {
	if len(args) == 1 {
		entryID := strings.TrimSpace(args[0])
		if entryID == "" {
			return "", errors.New("undo: empty entry id")
		}
		return entryID, nil
	}

	id, warnings, err := latestJournalEntryID(journalRootForVault(vaultRoot))
	if err != nil {
		return "", err
	}
	// "Most recent entry" now means "most recent *readable* entry", so say which
	// entries were passed over. Silently undoing an older command than the one
	// the user just ran is the failure mode worth spending two lines of stderr
	// on. A named entry ID never reaches here: that request stays a direct
	// error.
	if err := writeHistoryWarnings(cmd.ErrOrStderr(), warnings); err != nil {
		return "", err
	}
	return id, nil
}

// describeUndoError turns a restore failure into the message the user sees.
// The three shapes are distinct on purpose:
//
// A conflict is rendered as a list of the paths that moved on, with the
// --force hint, because nothing was written.
//
// A snapshot failure is passed through bare. The partial-failure guidance is
// about a restore that stopped partway, and it is the wrong thing to say about
// a restore that finished: a snapshot failure means the files are already back
// to their earlier state, so telling the user to inspect a rollback would send
// them looking for damage that is not there.
//
// Anything else carries the partial-failure guidance.
func describeUndoError(err error) error {
	var conflictErr *journal.RestoreConflictError
	if errors.As(err, &conflictErr) {
		return fmt.Errorf("undo: %s", renderUndoConflict(conflictErr))
	}
	if noteops.IsSnapshotFailure(err) {
		return fmt.Errorf("undo: %w", err)
	}
	return fmt.Errorf("undo: %w\n%s", err, undoPartialFailureGuidance)
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
