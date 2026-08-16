// Package cli wires the Cobra command tree for the bn binary.
package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

type ctxKey string

const configKey ctxKey = "bnotes.config"

// Execute runs the root command and returns any error from the chosen subcommand.
func Execute() error {
	return NewRootCmd().Execute()
}

// NewRootCmd builds the root cobra command with persistent flags wired up.
func NewRootCmd() *cobra.Command {
	var (
		vaultPath string
		verbose   bool
	)

	cmd := &cobra.Command{
		Use:           "bn",
		Short:         "Brain Notes: a CLI for plain-markdown vaults",
		Long:          "Brain Notes (bn) is a small, fast CLI for editing and querying a plain-Markdown notes vault. Use --dry-run previews where available, inspect with 'git diff', and use 'bn history' / 'bn undo' for short-term rollback.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Skip config loading for commands that don't need a vault
			// (completion scripts, help) and for `init`, which creates the
			// vault-root config file that loading would require.
			if cmd.Name() == "completion" || cmd.Name() == "help" || cmd.Name() == "init" ||
				cmd.Name() == cobra.ShellCompRequestCmd || cmd.Name() == cobra.ShellCompNoDescRequestCmd {
				return nil
			}
			for p := cmd.Parent(); p != nil; p = p.Parent() {
				if p.Name() == "completion" {
					return nil
				}
			}
			cfg, err := config.Load(config.LoadOptions{VaultFlag: vaultPath})
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			cmd.SetContext(context.WithValue(ctx, configKey, cfg))
			return nil
		},
	}

	cmd.PersistentFlags().StringVar(&vaultPath, "vault", "", "path to vault root (overrides BNOTES_VAULT and the upward search)")
	cmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")

	cmd.AddCommand(NewBackupCmd())
	cmd.AddCommand(NewBacklinksCmd())
	cmd.AddCommand(NewConfigCmd())
	cmd.AddCommand(NewContextCmd())
	cmd.AddCommand(NewDoctorCmd())
	cmd.AddCommand(NewFindCmd())
	cmd.AddCommand(NewGraphCmd())
	cmd.AddCommand(NewHistoryCmd())
	cmd.AddCommand(NewInitCmd())
	cmd.AddCommand(NewLintCmd())
	cmd.AddCommand(NewLinksCmd())
	cmd.AddCommand(NewMoveCmd())
	cmd.AddCommand(NewNewCmd())
	cmd.AddCommand(NewOpenCmd())
	cmd.AddCommand(NewRenameCmd())
	cmd.AddCommand(NewRenameKeyCmd())
	cmd.AddCommand(NewRepairCmd())
	cmd.AddCommand(NewRelinkCmd())
	cmd.AddCommand(NewRelatedCmd())
	cmd.AddCommand(NewReplaceCmd())
	cmd.AddCommand(NewRestoreCmd())
	cmd.AddCommand(NewSchemaCmd())
	cmd.AddCommand(NewSearchCmd())
	cmd.AddCommand(NewServeCmd())
	cmd.AddCommand(NewSetCmd())
	cmd.AddCommand(NewTagsCmd())
	cmd.AddCommand(NewUndoCmd())
	cmd.AddCommand(NewUnsetCmd())
	cmd.AddCommand(NewValuesCmd())
	cmd.AddCommand(NewVaultCmd())

	return cmd
}

// ConfigFromContext returns the *config.Config attached by PersistentPreRunE.
// Returns nil if no config has been attached (e.g. called outside of Cobra).
func ConfigFromContext(ctx context.Context) *config.Config {
	if ctx == nil {
		return nil
	}
	if v, ok := ctx.Value(configKey).(*config.Config); ok {
		return v
	}
	return nil
}
