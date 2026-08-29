package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	editutil "github.com/sglavoie/sglavoie_brainnotes/internal/editor"
)

// NewConfigCmd builds the `config` command tree.
func NewConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "config",
		Short:         "Show the active vault root and configuration file",
		Long:          "Print the resolved vault root, the brainnotes.toml that was loaded from it, and which mechanism resolved the vault: the --vault flag, $BNOTES_VAULT, or an upward search from the current directory.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("config: no configuration loaded")
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(),
				"Vault:       %s\nConfig:      %s\nResolved by: %s\n",
				cfg.Vault, cfg.SourcePath, cfg.VaultOrigin)
			return err
		},
	}

	cmd.AddCommand(newConfigEditCmd())
	cmd.AddCommand(newConfigGetCmd())
	cmd.AddCommand(newConfigSetCmd())
	cmd.AddCommand(newConfigUnsetCmd())
	return cmd
}

func newConfigEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "edit",
		Short:         "Open the vault's brainnotes.toml in $EDITOR",
		Long:          "Open the brainnotes.toml at the resolved vault root in $EDITOR. Run `bn init` first if the vault has no configuration file yet.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("config edit: no configuration loaded")
			}

			spec, err := editutil.Spec("", os.Getenv("EDITOR"))
			if errors.Is(err, editutil.ErrNoEditorConfigured) {
				return errors.New("config edit: $EDITOR is not set")
			}
			if err != nil {
				return fmt.Errorf("config edit: %w", err)
			}
			bin, argv, err := editutil.Resolve(spec, cfg.SourcePath, 0)
			if err != nil {
				return fmt.Errorf("config edit: %w", err)
			}
			if err := execEditor(bin, argv); err != nil {
				return fmt.Errorf("config edit: %w", err)
			}
			return nil
		},
	}
}
