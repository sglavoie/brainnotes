package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Every --help Examples block is a list of runnable invocations of the command
// it documents, indented two spaces as Cobra prints them; an example that names
// another command, or a flag the command does not register, would mislead.
func TestHelpExamplesInvokeTheirCommand(t *testing.T) {
	root := NewRootCmd()
	for _, name := range []string{"set", "move", "serve", "related", "rename-key"} {
		t.Run(name, func(t *testing.T) {
			cmd, _, err := root.Find([]string{name})
			if err != nil || cmd.Name() != name {
				t.Fatalf("find %s: %v", name, err)
			}
			if cmd.Example == "" {
				t.Fatalf("bn %s --help has no Examples", name)
			}
			for _, line := range strings.Split(cmd.Example, "\n") {
				if !strings.HasPrefix(line, "  bn ") {
					t.Fatalf("example %q is not indented like Cobra's other examples", line)
				}
				_, invocation, ok := strings.Cut(line+" ", "bn "+name+" ")
				if !ok {
					t.Fatalf("example %q does not run bn %s", line, name)
				}
				assertExampleFlagsExist(t, cmd, invocation)
			}
		})
	}
}

// assertExampleFlagsExist checks the long flags of one invocation (the text
// after "bn <name> "), ignoring a trailing comment and anything after a pipe.
func assertExampleFlagsExist(t *testing.T, cmd *cobra.Command, invocation string) {
	t.Helper()
	invocation, _, _ = strings.Cut(invocation, "#")
	invocation, _, _ = strings.Cut(invocation, "|")
	for _, field := range strings.Fields(invocation) {
		if !strings.HasPrefix(field, "--") || field == "--" {
			continue
		}
		name, _, _ := strings.Cut(strings.TrimPrefix(field, "--"), "=")
		if cmd.Flag(name) == nil {
			t.Fatalf("example %q uses --%s, which the command does not register", invocation, name)
		}
	}
}
