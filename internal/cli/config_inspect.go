package cli

import (
	"fmt"
	"io"
	"reflect"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

func newConfigExplainCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "explain KEY",
		Short:         "Show an effective configuration value and where it came from",
		Long:          "Show a loaded value after defaults and normalization, the file that assigned it, and the file that owns the key. For example: bn config explain open.editor. This command writes nothing.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(cmd *cobra.Command, args []string) error { return runConfigExplain(cmd, args[0]) },
	}
}

func runConfigExplain(cmd *cobra.Command, key string) error {
	cfg := ConfigFromContext(cmd.Context())
	value, err := cfg.EffectiveValue(key)
	if err != nil {
		return err
	}
	source := "built-in default"
	if layer, assigned := cfg.SourceOf(key); assigned {
		source = config.LayerPath(cfg.Vault, layer)
	}
	owner, _ := cfg.OwnerPath(key)
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Key:         %s\nSource:      %s\nOwned by:    %s\nEffective value (after defaults and normalization):\n", key, source, owner); err != nil {
		return err
	}
	return writeEffectiveValue(cmd.OutOrStdout(), value)
}

// TOML omits nil collections; an explanation must still show their value.
func writeEffectiveValue(out io.Writer, value any) error {
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Slice && v.Len() == 0 {
		_, err := fmt.Fprintln(out, "value = []")
		return err
	}
	if v.Kind() == reflect.Map && v.Len() == 0 {
		_, err := fmt.Fprintln(out, "value = {}")
		return err
	}
	return toml.NewEncoder(out).Encode(map[string]any{"value": value})
}

func runConfigSummary(cmd *cobra.Command) error {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return fmt.Errorf("config: no configuration loaded")
	}
	local := cfg.LocalSourcePath
	if local == "" {
		local = "none"
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Vault:       %s\nConfig:      %s\nOverlay:     %s\nResolved by: %s\n", cfg.Vault, cfg.SourcePath, local, cfg.VaultOrigin)
	return err
}
