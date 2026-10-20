package cli

import (
	"bytes"
	"fmt"
	"io"
	"reflect"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

func newConfigExplainCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:           "explain <key>",
		Short:         "Show an effective configuration value and where it came from",
		Long:          "Show a loaded value after defaults and normalization, the file that assigned it, and the file that owns the key. For example: bn config explain open.editor. This command writes nothing.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(cmd *cobra.Command, args []string) error { return runConfigExplain(cmd, args[0], asJSON) },
	}
	cmd.Flags().BoolVar(&asJSON, "json", false,
		`print a JSON object {"key", "source", "owned_by", "value"} (source is null for a built-in default)`)
	return cmd
}

// configExplainJSON is `config explain --json`. Source is null rather than the
// text form's "built-in default", so a script tests for a file without
// matching prose.
type configExplainJSON struct {
	Key     string  `json:"key"`
	Source  *string `json:"source"`
	OwnedBy string  `json:"owned_by"`
	Value   any     `json:"value"`
}

func runConfigExplain(cmd *cobra.Command, key string, asJSON bool) error {
	cfg := ConfigFromContext(cmd.Context())
	value, err := cfg.EffectiveValue(key)
	if err != nil {
		return fmt.Errorf("config explain: %w", err)
	}
	var sourcePath *string
	if layer, assigned := cfg.SourceOf(key); assigned {
		p := config.LayerPath(cfg.Vault, layer)
		sourcePath = &p
	}
	owner, _ := cfg.OwnerPath(key)
	if asJSON {
		jsonValue, err := effectiveValueJSON(value)
		if err != nil {
			return err
		}
		return writeJSON(cmd.OutOrStdout(), configExplainJSON{Key: key, Source: sourcePath, OwnedBy: owner, Value: jsonValue})
	}
	source := "built-in default"
	if sourcePath != nil {
		source = *sourcePath
	}
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

// effectiveValueJSON is the value the text form prints, spelled for JSON. It
// goes through the same TOML encoding, so a section reads with its config key
// names (not Go field names) and a duration with its config spelling; an empty
// collection is [] or {}, as in the text form, rather than null.
func effectiveValueJSON(value any) (any, error) {
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Slice && v.Len() == 0 {
		return []any{}, nil
	}
	if v.Kind() == reflect.Map && v.Len() == 0 {
		return map[string]any{}, nil
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(map[string]any{"value": value}); err != nil {
		return nil, err
	}
	var decoded map[string]any
	if _, err := toml.Decode(buf.String(), &decoded); err != nil {
		return nil, err
	}
	return decoded["value"], nil
}

// configSummaryJSON is `config --json`. Overlay is null when no local file is
// loaded, where the text form prints "none".
type configSummaryJSON struct {
	Vault      string  `json:"vault"`
	Config     string  `json:"config"`
	Overlay    *string `json:"overlay"`
	ResolvedBy string  `json:"resolved_by"`
}

func runConfigSummary(cmd *cobra.Command, asJSON bool) error {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return fmt.Errorf("config: no configuration loaded")
	}
	if asJSON {
		record := configSummaryJSON{Vault: cfg.Vault, Config: cfg.SourcePath, ResolvedBy: string(cfg.VaultOrigin)}
		if cfg.LocalSourcePath != "" {
			record.Overlay = &cfg.LocalSourcePath
		}
		return writeJSON(cmd.OutOrStdout(), record)
	}
	local := cfg.LocalSourcePath
	if local == "" {
		local = "none"
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Vault:       %s\nConfig:      %s\nOverlay:     %s\nResolved by: %s\n", cfg.Vault, cfg.SourcePath, local, cfg.VaultOrigin)
	return err
}
