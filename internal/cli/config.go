package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	editutil "github.com/sglavoie/sglavoie_brainnotes/internal/editor"
)

// NewConfigCmd builds the `config` command tree.
func NewConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "config",
		Short:         "Show the active vault root and configuration file",
		Long:          "Print the resolved vault root, the portable and optional local configuration files loaded from it, and which mechanism resolved the vault: the --vault flag, $BNOTES_VAULT, or an upward search from the current directory.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(cmd *cobra.Command, args []string) error { return runConfigSummary(cmd) },
	}

	cmd.AddCommand(newConfigEditCmd())
	cmd.AddCommand(newConfigExplainCmd())
	cmd.AddCommand(newConfigGetCmd())
	cmd.AddCommand(newConfigMigrateCmd())
	cmd.AddCommand(newConfigSetCmd())
	cmd.AddCommand(newConfigUnsetCmd())
	return cmd
}

func newConfigEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Open the vault's brainnotes.toml in your editor",
		Long: "Open the brainnotes.toml at the resolved vault root in [open].editor, falling back to $EDITOR. " +
			"Run `bn init` first if the vault has no configuration file yet.\n\n" +
			"A configuration that no longer parses still opens: fixing it is what this command is for. The " +
			"editor setting is then read from $EDITOR alone, since the file that names it cannot be read.",
		Args: cobra.NoArgs,
		// Requiring a successful load would put the editor behind the very
		// syntax error the user is trying to fix.
		Annotations:   map[string]string{skipConfigLoad: "opens a configuration that may not parse"},
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          runConfigEdit,
	}
}

func runConfigEdit(cmd *cobra.Command, _ []string) error {
	const label = "config edit"
	vaultFlag, err := cmd.Flags().GetString("vault")
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	path, configured, err := configEditTarget(cmd, vaultFlag)
	if err != nil {
		return err
	}
	spec, err := editutil.Spec(configured, os.Getenv("EDITOR"))
	if errors.Is(err, editutil.ErrNoEditorConfigured) {
		return fmt.Errorf("%s: set [open].editor or $EDITOR", label)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	bin, argv, err := editutil.Resolve(spec, path, 0)
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	if err := execEditor(bin, argv); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}

// configEditTarget returns the portable configuration file and the configured
// editor. A configuration that does not load still yields its path — with a
// warning naming why, and no configured editor — as long as the file exists.
func configEditTarget(cmd *cobra.Command, vaultFlag string) (path, editor string, err error) {
	opts := config.LoadOptions{VaultFlag: vaultFlag}
	cfg, loadErr := config.Load(opts)
	if loadErr == nil {
		return cfg.SourcePath, cfg.Open.Editor, nil
	}
	root, _, err := config.ResolveVault(opts)
	if err != nil {
		return "", "", err
	}
	path = filepath.Join(root, config.FileName)
	if _, statErr := os.Stat(path); statErr != nil {
		return "", "", loadErr
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "config edit: warning: %v\n", loadErr)
	return path, "", nil
}
