package cli

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"
)

// usageError is a command line Cobra refused before the command ran: the wrong
// number of positional arguments, a flag that does not exist or did not parse,
// or a subcommand that does not exist. Cobra spells these without naming the
// command ("accepts 2 arg(s), received 1"), which is useless at the end of a
// pipeline, so every one is rewrapped to name the command and point at its
// help, the same `bn: <cmd>: ...` shape every other bn error has.
//
// It is an ordinary error, so cmd/bn exits 2 for it.
type usageError struct {
	// path is the command path without the leading "bn", e.g. "config get";
	// empty for the root command itself.
	path string
	err  error
}

func (e *usageError) Error() string {
	msg := e.err.Error()
	if e.path != "" && !strings.HasPrefix(msg, e.path+": ") {
		msg = e.path + ": " + msg
	}
	return msg + "; see '" + e.helpCommand() + "'"
}

func (e *usageError) Unwrap() error { return e.err }

func (e *usageError) helpCommand() string {
	if e.path == "" {
		return "bn --help"
	}
	return "bn " + e.path + " --help"
}

// newUsageError wraps err as a usage error of cmd, once.
func newUsageError(cmd *cobra.Command, err error) error {
	if err == nil {
		return nil
	}
	var already *usageError
	if errors.As(err, &already) {
		return err
	}
	path := cmd.CommandPath()
	if root := cmd.Root(); root != nil {
		path = strings.TrimPrefix(strings.TrimPrefix(path, root.Name()), " ")
	}
	return &usageError{path: path, err: err}
}

// installUsageErrors makes every argument-count and flag error in the tree
// rooted at root a usageError. The flag-error function is inherited by every
// subcommand from the root; positional validators are wrapped one command at
// a time, because Cobra calls each command's own.
func installUsageErrors(root *cobra.Command) {
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return newUsageError(cmd, err)
	})
	wrapArgsValidators(root)
}

func wrapArgsValidators(cmd *cobra.Command) {
	if validate := cmd.Args; validate != nil {
		cmd.Args = func(c *cobra.Command, args []string) error {
			return newUsageError(c, validate(c, args))
		}
	}
	for _, child := range cmd.Commands() {
		wrapArgsValidators(child)
	}
}

// asUsageError rewraps the one usage error Cobra raises outside a validator or
// flag parse: an unknown subcommand, which its command lookup reports against
// the parent it stopped at.
func asUsageError(cmd *cobra.Command, err error) error {
	if err == nil || cmd == nil {
		return err
	}
	if strings.HasPrefix(err.Error(), "unknown command ") {
		return newUsageError(cmd, err)
	}
	return err
}
