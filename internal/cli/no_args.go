package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// noArgs is the positional validator for a leaf command that takes no
// arguments. cobra.NoArgs spells a stray argument as `unknown command "x" for
// "bn find"`, which reads as a typo in a subcommand name when the command has
// none; on a leaf the honest sentence is that it takes no arguments:
//
//	find: takes no arguments (got "tag:foo"); did you mean -q "tag:foo"?
//
// The -q hint is offered only by a command that registers -q, because a stray
// query expression is the commonest reason for the argument there.
//
// A parent command with subcommands keeps cobra.NoArgs: for it an unknown
// word really is an unknown subcommand.
func noArgs(name string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}
		quoted := make([]string, len(args))
		for i, arg := range args {
			quoted[i] = fmt.Sprintf("%q", arg)
		}
		msg := fmt.Sprintf("%s: takes no arguments (got %s)", name, strings.Join(quoted, " "))
		if q := cmd.Flags().ShorthandLookup("q"); q != nil && q.Name == "query" {
			msg += fmt.Sprintf("; did you mean -q %s?", shellQuote(strings.Join(args, " ")))
		}
		return errors.New(msg)
	}
}
