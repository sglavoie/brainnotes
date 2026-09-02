package cli

import (
	"github.com/spf13/cobra"
)

// NewUnsetCmd returns the `unset` subcommand. Exported for tests.
func NewUnsetCmd() *cobra.Command {
	var opts unsetOptions

	cmd := &cobra.Command{
		Use:   "unset [note...] [--] <property...>",
		Short: "Remove frontmatter properties from one or more notes",
		Long: `Unset removes frontmatter properties from one or more notes.

Without '--', the first positional arg is the note path (or '-' for stdin) and
all subsequent args are property names. To pass multiple notes use '--' to
separate them from the property list: 'bn unset a.md b.md -- prop1 prop2'.
Use 'bn unset -- prop1 prop2' to pick notes via an fzf multi-select picker.

Removing a missing property is a silent no-op.

Use --dry-run to preview which properties would be removed without writing.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUnsetCommand(cmd, args, opts)
		},
	}

	f := cmd.Flags()
	f.BoolVarP(&opts.nullSep, "null", "0", false, "read NUL-separated paths from stdin")
	f.BoolVar(&opts.dryRun, "dry-run", false, "preview removals without writing")
	return cmd
}
