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
		Short:         "Export the vault link graph in dot, mermaid, or code format",
		Long:          "Export the vault's wikilink graph (or a filtered subgraph) as `dot` or `mermaid` for visualization, or as `code`, a list of edges with absolute file:line references that are cmd+clickable in editor terminals.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "dot" && format != "mermaid" && format != "code" {
				return fmt.Errorf("graph: unknown format %q (must be dot, mermaid, or code)", format)
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

			g := query.BuildGraphForRoot(v.Root, notes)

			sel, err := ff.selectNotes(cmd.Context(), cfg, v.Root, notes, g)
			if err != nil {
				return err
			}

			nodeSet := make(map[string]struct{})
			for _, n := range sel.notes {
				nodeSet[g.KeyForNote(n)] = struct{}{}
			}

			out := cmd.OutOrStdout()
			switch format {
			case "dot":
				return g.ExportDot(out, nodeSet)
			case "mermaid":
				return g.ExportMermaid(out, nodeSet)
			case "code":
				return g.ExportCode(out, notes, nodeSet)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&format, "format", "dot", "output format: dot, mermaid, or code")
	ff.register(cmd)

	return cmd
}
