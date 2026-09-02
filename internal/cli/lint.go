package cli

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/schema"
)

// LintError signals that lint found violations. main.go prints it and exits 1.
type LintError struct {
	Count int
}

func (e *LintError) Error() string {
	return fmt.Sprintf("%d violation(s) found", e.Count)
}

func NewLintCmd() *cobra.Command {
	opts := lintOptions{ff: new(findFlags)}

	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Report schema violations in frontmatter across the vault",
		Long: `Lint checks every note (or a scoped subset) against the property definitions
in [frontmatter.properties] and reports violations without modifying files.

Exit code 0 means clean; exit code 1 means violations were found.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLintCommand(cmd, opts)
		},
	}

	opts.ff.register(cmd)
	cmd.Flags().StringVar(&opts.property, "property", "", "lint only this schema property")

	return cmd
}

type groupKey struct {
	Property string
	Kind     string
	Detail   string
}

func formatViolations(cmd *cobra.Command, violations []schema.Violation) {
	groups := make(map[groupKey][]string)
	for _, v := range violations {
		key := groupKey{Property: v.Property, Kind: v.Kind, Detail: v.Detail}
		groups[key] = append(groups[key], v.NotePath)
	}

	keys := make([]groupKey, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Property != keys[j].Property {
			return keys[i].Property < keys[j].Property
		}
		if keys[i].Kind != keys[j].Kind {
			return keys[i].Kind < keys[j].Kind
		}
		return keys[i].Detail < keys[j].Detail
	})

	out := cmd.OutOrStdout()
	for _, key := range keys {
		paths := groups[key]
		sort.Strings(paths)

		n := len(paths)
		noun := "notes"
		if n == 1 {
			noun = "note"
		}

		switch key.Kind {
		case "missing":
			fmt.Fprintf(out, "%s: missing in %d %s (%s)\n", key.Property, n, noun, key.Detail)
		case "type":
			fmt.Fprintf(out, "%s: wrong type in %d %s (%s)\n", key.Property, n, noun, key.Detail)
		case "value":
			fmt.Fprintf(out, "%s: %s in %d %s\n", key.Property, key.Detail, n, noun)
		}

		for _, p := range paths {
			fmt.Fprintf(out, "  %s\n", p)
		}
	}
}
