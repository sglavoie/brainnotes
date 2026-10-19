package cli

import (
	"github.com/spf13/cobra"
)

// openPicker is the picker used by `open`. Tests swap it to avoid spawning fzf.
var openPicker = RunFZF

// openExec is the editor invocation used by `open`. Tests swap it to avoid
// exec-replacing the test process.
var openExec = func(editor, file string) error { return runEditor(editor, file) }

// NewOpenCmd returns the `open` subcommand. Exported for tests.
func NewOpenCmd() *cobra.Command {
	var opts openOptions

	cmd := &cobra.Command{
		Use:           "open [query]",
		Short:         "Pick a note with fzf and open it in [open].editor or $EDITOR",
		Long:          "Pipe every vault note path into fzf for selection, then exec the configured editor ([open].editor in config, or $EDITOR as fallback) on the chosen path. With a positional query, fzf opens pre-filtered (the picker is not auto-confirmed). Use --print to emit the selected path instead of invoking the editor.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOpenCommand(cmd, args, opts)
		},
	}

	f := cmd.Flags()
	f.BoolVar(&opts.printOnly, "print", false, "print the selected path instead of invoking the editor")
	f.BoolVarP(&opts.nullSep, "null", "0", false, "with --print, terminate the path with NUL instead of newline")

	return cmd
}
