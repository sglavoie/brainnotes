package cli

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/sglavoie/brainnotes/internal/journal"
)

// NewHistoryCmd returns the `history` subcommand. Exported for tests.
func NewHistoryCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "history",
		Short: "List recent undo journal entries",
		Long: `History lists journal entries newest-first from the vault-local undo
journal (<vault>/.bnotes/journal). Output is tab-separated:
TIMESTAMP<TAB>ID<TAB>COMMAND, one entry per line. The header row naming those
columns is printed only when stdout is a terminal, so piped output is entries
alone.

Use this with the safety workflow: preview with --dry-run when available,
apply, inspect with 'git diff', then run 'bn undo <id>' to roll back if
needed.

With --json, the output is one object: {"entries": [...]}, newest first, each
entry carrying its id, timestamp, command and the vault-relative paths it
changed. An empty journal prints no rows (or an empty "entries" array) and says
so on stderr.`,
		Example: `  bn history
  bn history | head -n 10
  bn history --json | jq -r '.entries[0].id'`,
		Args:          noArgs("history"),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHistory(cmd, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the entries as a JSON object instead of tab-separated text")
	return cmd
}

// writeHistoryWarnings reports the entries that could not be read. Each one
// names the directory to inspect, because the remedy is to look at (or delete)
// exactly that entry — the rest of the history is unaffected and still undoable.
func writeHistoryWarnings(w io.Writer, warnings []journal.Warning) error {
	for _, warning := range warnings {
		if _, err := fmt.Fprintf(w, "history: warning: %v\n", warning); err != nil {
			return err
		}
	}
	return nil
}

func runHistory(cmd *cobra.Command, asJSON bool) error {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("history: no config in context")
	}

	entries, warnings, err := journal.ListEntries(journalRootForVault(cfg.Vault))
	if err != nil {
		return fmt.Errorf("history: %w", err)
	}
	if len(entries) == 0 {
		// stderr, not stdout: on stdout this sentence would be read as a
		// record by `bn history | cut -f2`.
		if _, err := fmt.Fprintln(cmd.ErrOrStderr(), "history: no journal entries"); err != nil {
			return err
		}
	}
	if asJSON {
		err = writeHistoryJSON(cmd.OutOrStdout(), entries)
	} else {
		err = writeHistoryText(cmd.OutOrStdout(), entries)
	}
	if err != nil {
		return err
	}
	// Warnings go to stderr, after the entries, so `bn history | head` keeps
	// producing the records it promises even when part of the journal is
	// damaged. A corrupt entry is worth reporting; it is not worth
	// contaminating a stream other tools parse.
	return writeHistoryWarnings(cmd.ErrOrStderr(), warnings)
}

func writeHistoryText(out io.Writer, entries []journal.Entry) error {
	if len(entries) == 0 {
		return nil
	}
	// The header is for a person reading a terminal. In a pipeline it would be
	// the first "record", so `bn history | head -n 10` would show nine entries
	// and `bn history | cut -f2` would start with the word ID.
	if stdoutIsTerminal(out) {
		if _, err := fmt.Fprintln(out, "TIMESTAMP\tID\tCOMMAND"); err != nil {
			return err
		}
	}
	for _, e := range entries {
		if _, err := fmt.Fprintf(
			out,
			"%s\t%s\t%s\n",
			e.Timestamp.UTC().Format(time.RFC3339Nano),
			e.ID,
			e.Command,
		); err != nil {
			return err
		}
	}
	return nil
}

type historyJSON struct {
	Entries []historyJSONEntry `json:"entries"`
}

type historyJSONEntry struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Command   string    `json:"command"`
	Files     []string  `json:"files"`
}

func writeHistoryJSON(out io.Writer, entries []journal.Entry) error {
	payload := historyJSON{Entries: make([]historyJSONEntry, 0, len(entries))}
	for _, e := range entries {
		files := make([]string, 0, len(e.Files))
		for _, f := range e.Files {
			files = append(files, f.Path)
		}
		payload.Entries = append(payload.Entries, historyJSONEntry{
			ID:        e.ID,
			Timestamp: e.Timestamp.UTC(),
			Command:   e.Command,
			Files:     files,
		})
	}
	return writeJSON(out, payload)
}
