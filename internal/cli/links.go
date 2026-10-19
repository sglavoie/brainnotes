package cli

import (
	"github.com/spf13/cobra"
)

// NewLinksCmd returns the `links` subcommand. Exported for tests.
func NewLinksCmd() *cobra.Command {
	var opts linkListOptions

	cmd := &cobra.Command{
		Use:           "links [note]",
		Short:         "List notes that <note> links to",
		Long:          "List the paths of notes that the target note references via wikilinks. The target may be specified as a title (case-insensitive stem) or a path/glob. When called with no arguments, an fzf picker lets you select the target interactively.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLinksCommand(cmd, args, opts)
		},
	}

	f := cmd.Flags()
	f.BoolVarP(&opts.nullSep, "null", "0", false, "separate paths with NUL instead of newline")
	f.BoolVarP(&opts.verbose, "verbose", "v", false, "print each link with its file:line (or frontmatter key) and a snippet")
	f.BoolVar(&opts.asJSON, "json", false, `print a JSON array of {"path", "abs_path", "title"} records (path is vault-relative)`)

	return cmd
}
