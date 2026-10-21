package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/sglavoie/brainnotes/internal/config"
)

func NewSchemaCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Show the configured frontmatter property schema",
		Long: `Schema prints the property definitions in [frontmatter.properties]. Each
property is shown with its type, its item kind for lists, whether it is
required, and its allowed values (if any). Use this as a reference when fixing
lint or repair violations.`,
		Args:          noArgs("schema"),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSchema(cmd, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false,
		`print a JSON array of {"name", "type", "items", "required", "allowed"} records`)
	return cmd
}

// runSchema prints the declared properties as a table, or as JSON records. No
// declarations prints the hint on stderr; as JSON it is `[]`.
func runSchema(cmd *cobra.Command, asJSON bool) error {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("schema: no config in context")
	}
	if asJSON {
		return writeSchemaJSON(cmd.OutOrStdout(), &cfg.Frontmatter)
	}
	if len(cfg.Frontmatter.Properties) == 0 {
		writeSchemaHint(cmd.ErrOrStderr(), "schema", cfg.SourcePath)
		return nil
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROPERTY\tTYPE\tITEMS\tREQUIRED\tALLOWED")
	for _, k := range cfg.Frontmatter.PropertyNames() {
		def := cfg.Frontmatter.Properties[k]
		req := "no"
		if def.Required {
			req = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", k, def.Type, def.Items, req, strings.Join(def.Allowed, ", "))
	}
	return w.Flush()
}

type schemaJSONProperty struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Items    string   `json:"items,omitempty"`
	Required bool     `json:"required"`
	Allowed  []string `json:"allowed"`
}

// writeSchemaJSON emits the declarations in the table's order. allowed is `[]`
// for a property with no allow-list.
func writeSchemaJSON(out io.Writer, fm *config.FrontmatterConfig) error {
	records := make([]schemaJSONProperty, 0, len(fm.Properties))
	for _, k := range fm.PropertyNames() {
		def := fm.Properties[k]
		allowed := append([]string{}, def.Allowed...)
		records = append(records, schemaJSONProperty{
			Name: k, Type: string(def.Type), Items: string(def.Items), Required: def.Required, Allowed: allowed,
		})
	}
	return writeJSON(out, records)
}
