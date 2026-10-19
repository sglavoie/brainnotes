package cli

import (
	"github.com/spf13/cobra"
)

// runBacklinksCommand lists the notes that reference the target. It is the
// mirror of runLinksCommand: same resolved state, same two output forms, read
// from the other side of the graph.
func runBacklinksCommand(cmd *cobra.Command, args []string, opts linkListOptions) error {
	r, ok, err := newLinkListRun(cmd, "backlinks", args, opts)
	if err != nil || !ok {
		return err
	}

	referrers := backlinkReferrers(r.graph, r.target)
	out := cmd.OutOrStdout()
	if opts.verbose {
		return writeVerboseBacklinks(out, r.notes, r.target, referrers, r.graph)
	}
	return opts.writePaths(out, r.root, referrers)
}
