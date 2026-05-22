package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// openPicker is the picker used by `open`. Tests swap it to avoid spawning fzf.
var openPicker = RunFZF

// openExec is the editor invocation used by `open`. Tests swap it to avoid
// exec-replacing the test process.
var openExec = func(editor, file string) error { return runEditor(editor, file) }

// NewOpenCmd returns the `open` subcommand. Exported for tests.
func NewOpenCmd() *cobra.Command {
	var (
		printOnly bool
		nullSep   bool
	)

	cmd := &cobra.Command{
		Use:           "open [query]",
		Short:         "Pick a note with fzf and open it in $EDITOR",
		Long:          "Pipe every vault note path into fzf for selection, then exec $EDITOR on the chosen path. With a positional query, fzf opens pre-filtered (the picker is not auto-confirmed). Use --print to emit the selected path instead of invoking the editor.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("open: no config in context")
			}
			v, err := vault.Open(cfg)
			if err != nil {
				return err
			}

			var paths []string
			if err := v.Walk(vault.WalkFilter{}, func(n *vault.Note) error {
				paths = append(paths, n.Path)
				return nil
			}); err != nil {
				return err
			}
			sort.Strings(paths)

			if len(paths) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "open: no notes in vault")
				return nil
			}

			extraArgs := append([]string(nil), cfg.Fzf.Args...)
			if len(args) == 1 && args[0] != "" {
				extraArgs = append(extraArgs, "--query", args[0])
			}

			selected, err := openPicker(paths, extraArgs)
			if err != nil {
				return err
			}
			if len(selected) == 0 {
				return nil
			}
			target := selected[0]

			if printOnly {
				sep := byte('\n')
				if nullSep {
					sep = 0
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s%c", target, sep); err != nil {
					return err
				}
				return nil
			}

			editor := cfg.Open.Editor
			if editor == "" {
				editor = os.Getenv("EDITOR")
			}
			if editor == "" {
				return errors.New("open: no editor configured (set [open].editor in config or $EDITOR)")
			}
			if err := openExec(editor, target); err != nil {
				return fmt.Errorf("open: %w", err)
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.BoolVar(&printOnly, "print", false, "print the selected path instead of invoking $EDITOR")
	f.BoolVarP(&nullSep, "null", "0", false, "with --print, terminate the path with NUL instead of newline")

	return cmd
}
