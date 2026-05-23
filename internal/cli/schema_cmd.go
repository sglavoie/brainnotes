package cli

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func NewSchemaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Show the configured frontmatter property schema",
		Long: `Schema prints the property schema defined in config.toml. Each property
is shown with its type, whether it is required, and its allowed values
(if any). Use this as a reference when fixing lint or repair violations.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("schema: no config in context")
			}

			if len(cfg.Frontmatter.Schema) == 0 {
				writeSchemaHint(cmd.ErrOrStderr(), "schema", cfg.SourcePath)
				return nil
			}

			keys := make([]string, 0, len(cfg.Frontmatter.Schema))
			for k := range cfg.Frontmatter.Schema {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			out := cmd.OutOrStdout()
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "PROPERTY\tTYPE\tREQUIRED\tALLOWED")
			for _, k := range keys {
				rule := cfg.Frontmatter.Schema[k]
				req := "no"
				if rule.Required {
					req = "yes"
				}
				allowed := ""
				if len(rule.Allowed) > 0 {
					allowed = strings.Join(rule.Allowed, ", ")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", k, rule.Type, req, allowed)
			}
			w.Flush()
			return nil
		},
	}
	return cmd
}
