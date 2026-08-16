package cli

import (
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func NewSchemaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Show the configured frontmatter property schema",
		Long: `Schema prints the property definitions in [frontmatter.properties]. Each
property is shown with its type, its item kind for lists, whether it is
required, and its allowed values (if any). Use this as a reference when fixing
lint or repair violations.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("schema: no config in context")
			}

			if len(cfg.Frontmatter.Properties) == 0 {
				writeSchemaHint(cmd.ErrOrStderr(), "schema", cfg.SourcePath)
				return nil
			}

			out := cmd.OutOrStdout()
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "PROPERTY\tTYPE\tITEMS\tREQUIRED\tALLOWED")
			for _, k := range cfg.Frontmatter.PropertyNames() {
				def := cfg.Frontmatter.Properties[k]
				req := "no"
				if def.Required {
					req = "yes"
				}
				allowed := ""
				if len(def.Allowed) > 0 {
					allowed = strings.Join(def.Allowed, ", ")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", k, def.Type, def.Items, req, allowed)
			}
			w.Flush()
			return nil
		},
	}
	return cmd
}
