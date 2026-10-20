package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// vaultExec is the editor invocation used by `vault`. Tests swap it to avoid
// exec-replacing the test process.
var vaultExec = func(editor, path string) error { return runEditor(editor, path) }

// NewVaultCmd returns the `vault` subcommand. Exported for tests.
func NewVaultCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "vault",
		Short:         "Open the vault root directory in [open].editor or $EDITOR",
		Long:          "Exec-replace with the configured editor opened on the vault root directory ([open].editor in config, or $EDITOR as fallback).",
		Args:          noArgs("vault"),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("vault: no config in context")
			}
			editorSpec, err := resolveEditorSpec("vault", cfg.Open.Editor)
			if err != nil {
				return err
			}
			if err := vaultExec(editorSpec, cfg.Vault); err != nil {
				return fmt.Errorf("vault: %w", err)
			}
			return nil
		},
	}
}
