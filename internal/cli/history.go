package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
)

// NewHistoryCmd returns the `history` subcommand. Exported for tests.
func NewHistoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "history",
		Short: "List recent undo journal entries",
		Long: `History lists journal entries newest-first from the vault-local undo
journal (<vault>/.bnotes/journal). Output is tab-separated:
TIMESTAMP<TAB>ID<TAB>COMMAND.

Use this with the safety workflow: preview with --dry-run when available,
apply, inspect with 'git diff', then run 'bn undo <id>' to roll back if
needed.`,
		Example: `  bn history
  bn history | head -n 10`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("history: no config in context")
			}

			entries, err := journal.ListEntries(journalRootForVault(cfg.Vault))
			if err != nil {
				return fmt.Errorf("history: %w", err)
			}

			out := cmd.OutOrStdout()
			if len(entries) == 0 {
				_, err := fmt.Fprintln(out, "history: no journal entries")
				return err
			}

			if _, err := fmt.Fprintln(out, "TIMESTAMP\tID\tCOMMAND"); err != nil {
				return err
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
		},
	}
	return cmd
}
