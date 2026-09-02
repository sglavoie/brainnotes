package cli

import (
	"github.com/spf13/cobra"
)

// NewFindCmd returns the `find` subcommand. Exported for tests.
func NewFindCmd() *cobra.Command {
	// find is the one flag-scoped command that orders and caps its output, so
	// it is the one command allowed to run a saved view's stored sort and limit.
	opts := findOptions{ff: &findFlags{ordersResults: true}}

	cmd := &cobra.Command{
		Use:   "find",
		Short: "Find notes by frontmatter criteria",
		Long: `Find notes whose frontmatter matches every supplied filter. Prints matching
absolute paths, one per line (or NUL-separated with -0). Pipe into fzf with -i
for an interactive picker.

-q takes a query expression, which reaches filters the flags cannot spell:
boolean composition (and / or / not, grouped with parentheses), comparison
against a declared property (rating>=4, due<today), and the link graph
(near:[[Deep Work]]<=2, links-to:[[Index]], degree>=5, cluster-size:1). An
undeclared property is reached through prop:, as in prop:mykey=value.

--view names a saved view. Flags, -q, and --view all AND together, so each one
narrows the result further. A view that no longer compiles is refused rather
than run as a partial filter, and a view that stores a sort or a limit runs with
them: find is the only command that orders and caps results, so every other
command refuses such a view instead of dropping the fields. An explicit --sort
or --relative-to replaces the view's ordering.

--sort orders the matches. Results are path-ordered by default; --sort relevance
ranks them against the note named by --relative-to, most related first, using the
same signals bn related does. Sorting never changes which notes match.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		// Args is NoArgs, so there are never positional arguments to hand on.
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runFindCommand(cmd, opts)
		},
	}

	opts.ff.register(cmd)
	f := cmd.Flags()
	f.BoolVarP(&opts.interactive, "interactive", "i", false, "pipe matches into fzf for selection")
	f.BoolVarP(&opts.nullSep, "null", "0", false, "separate paths with NUL instead of newline")
	f.BoolVar(&opts.showBroken, "show-broken", false, "show each broken link (requires --broken-links)")
	f.BoolVar(&opts.listBrokenTargets, "list-broken-targets", false, "list unique broken targets (requires --broken-links)")
	f.StringVar(&opts.sortText, "sort", "", findSortHelp())
	f.StringVar(&opts.relativeTo, "relative-to", "", "anchor note for --sort relevance (title or path)")

	return cmd
}
