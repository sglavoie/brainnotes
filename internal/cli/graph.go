package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func NewGraphCmd() *cobra.Command {
	var format string

	ff := new(findFlags)

	cmd := &cobra.Command{
		Use:           "graph",
		Short:         "Export the vault link graph in dot or mermaid format",
		Long:          "Export the vault's wikilink graph (or a filtered subgraph) in dot or mermaid format for visualization. Pipe the output to a file and open it in a Graphviz viewer or Mermaid renderer.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "dot" && format != "mermaid" {
				return fmt.Errorf("graph: unknown format %q (must be dot or mermaid)", format)
			}

			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("graph: no config in context")
			}
			v, err := vault.Open(cfg)
			if err != nil {
				return err
			}
			notes, err := v.Notes()
			if err != nil {
				return err
			}

			g := query.BuildGraph(notes)

			flags := ff.toQueryFlags(v.Root, g)
			pred, err := query.FromFlags(flags)
			if err != nil {
				return err
			}

			nodeSet := make(map[string]struct{})
			for _, n := range notes {
				if pred(n) {
					nodeSet[noteKey(n)] = struct{}{}
				}
			}

			out := cmd.OutOrStdout()
			switch format {
			case "dot":
				return g.ExportDot(out, nodeSet)
			case "mermaid":
				return g.ExportMermaid(out, nodeSet)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&format, "format", "dot", "output format: dot or mermaid")
	ff.register(cmd)

	return cmd
}
