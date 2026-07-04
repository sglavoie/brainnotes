package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

// commandInvocation renders the command path plus its positional args as the
// human-readable command string stored in an undo-journal entry.
func commandInvocation(cmd *cobra.Command, args []string) string {
	parts := make([]string, 0, 1+len(args))
	if cmd != nil {
		if path := strings.TrimSpace(cmd.CommandPath()); path != "" {
			parts = append(parts, path)
		}
	}
	parts = append(parts, args...)
	if len(parts) == 0 {
		return "bn"
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}
