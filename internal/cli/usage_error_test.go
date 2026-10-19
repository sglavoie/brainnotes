package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

// Usage errors name the failing command and point at its help, the same
// `bn: <cmd>: ...` shape every other bn error has (docs/integrations.md).
func TestUsageErrorsNameTheCommand(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"arg count", []string{"new", "only-template"}, "bn: new: accepts 2 arg(s), received 1; see 'bn new --help'\n"},
		{"unknown flag", []string{"find", "--bogus"}, "bn: find: unknown flag: --bogus; see 'bn find --help'\n"},
		{"unknown shorthand", []string{"find", "-Z"}, "bn: find: unknown shorthand flag: 'Z' in -Z; see 'bn find --help'\n"},
		{"missing flag value", []string{"find", "--sort"}, "bn: find: flag needs an argument: --sort; see 'bn find --help'\n"},
		{"root flag", []string{"--bogus"}, "bn: unknown flag: --bogus; see 'bn --help'\n"},
		{"unknown command", []string{"bogus"}, "bn: unknown command \"bogus\" for \"bn\"; see 'bn --help'\n"},
		{"unknown subcommand", []string{"config", "bogus"}, "bn: config: unknown command \"bogus\" for \"bn config\"; see 'bn config --help'\n"},
		{"prefixed validator", []string{"config", "get"}, "bn: config get: expected 1 argument(s), got 0; usage: bn config get autosave.min_interval; see 'bn config get --help'\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BNOTES_VAULT", t.TempDir())
			root := NewRootCmd()
			root.SetArgs(tc.args)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			err := executeRoot(context.Background(), root)
			var usage *usageError
			if !errors.As(err, &usage) {
				t.Fatalf("bn %v: err = %T %v, want *usageError", tc.args, err, err)
			}
			if IsNegativeOutcome(err) {
				t.Fatalf("usage error must exit 2, not 1: %v", err)
			}
			var buf bytes.Buffer
			RenderError(&buf, err)
			if buf.String() != tc.want {
				t.Fatalf("rendered = %q, want %q", buf.String(), tc.want)
			}
		})
	}
}
