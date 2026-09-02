package cli

import (
	"github.com/spf13/cobra"
)

func NewGraphCmd() *cobra.Command {
	opts := graphOptions{format: "dot", ff: new(findFlags)}

	cmd := &cobra.Command{
		Use:           "graph",
		Short:         "Export the vault link graph in dot, mermaid, or code format",
		Long:          "Export the vault's wikilink graph (or a filtered subgraph) as `dot` or `mermaid` for visualization, or as `code`, a list of edges with absolute file:line references that are cmd+clickable in editor terminals.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGraphCommand(cmd, opts)
		},
	}

	cmd.Flags().StringVar(&opts.format, "format", opts.format, "output format: dot, mermaid, or code")
	opts.ff.register(cmd)

	return cmd
}
