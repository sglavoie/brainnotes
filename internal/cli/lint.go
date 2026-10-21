package cli

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/sglavoie/brainnotes/internal/schema"
)

// LintError signals that lint found violations. main.go prints it and exits 1.
type LintError struct {
	Count int
}

func (e *LintError) Error() string {
	return fmt.Sprintf("lint: %d violation(s) found", e.Count)
}

func NewLintCmd() *cobra.Command {
	opts := lintOptions{ff: new(findFlags)}

	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Report schema violations in frontmatter across the vault",
		Long: `Lint checks every note (or a scoped subset) against the property definitions
in [frontmatter.properties] and reports violations without modifying files.

Exit code 0 means clean; exit code 1 means violations were found; exit code 2
means lint could not run (for example, an unknown --property).`,
		Args:          noArgs("lint"),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLintCommand(cmd, opts)
		},
	}

	opts.ff.register(cmd)
	cmd.Flags().StringVar(&opts.property, "property", "", "lint only this schema property")
	cmd.Flags().BoolVar(&opts.asJSON, "json", false, `print {"count", "violations": [{"path", "property", "kind", "detail"}]} instead of the grouped report`)

	return cmd
}

type groupKey struct {
	Property string
	Kind     string
	Detail   string
}

func formatViolations(cmd *cobra.Command, violations []schema.Violation) {
	groups := groupViolations(violations)
	out := cmd.OutOrStdout()
	for _, key := range sortedViolationKeys(groups) {
		paths := groups[key]
		sort.Strings(paths)
		fmt.Fprint(out, violationHeadline(key, len(paths)))
		for _, p := range paths {
			fmt.Fprintf(out, "  %s\n", p)
		}
	}
}

// groupViolations collapses the per-note violations into one group per
// (property, kind, detail), which is what makes the report one headline per
// distinct problem rather than one line per note.
func groupViolations(violations []schema.Violation) map[groupKey][]string {
	groups := make(map[groupKey][]string)
	for _, v := range violations {
		key := groupKey{Property: v.Property, Kind: v.Kind, Detail: v.Detail}
		groups[key] = append(groups[key], v.NotePath)
	}
	return groups
}

// sortedViolationKeys orders the groups by property, then kind, then detail, so
// the report is stable across runs.
func sortedViolationKeys(groups map[groupKey][]string) []groupKey {
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
	return keys
}

// violationHeadline renders the group's summary line, including its trailing
// newline. An unrecognized kind renders nothing — the group's note paths are
// still listed — which is what the switch here has always done rather than
// inventing a headline for a kind the schema layer did not define.
func violationHeadline(key groupKey, n int) string {
	noun := "notes"
	if n == 1 {
		noun = "note"
	}
	switch key.Kind {
	case "missing":
		return fmt.Sprintf("%s: missing in %d %s (%s)\n", key.Property, n, noun, key.Detail)
	case "type":
		return fmt.Sprintf("%s: wrong type in %d %s (%s)\n", key.Property, n, noun, key.Detail)
	case "value":
		return fmt.Sprintf("%s: %s in %d %s\n", key.Property, key.Detail, n, noun)
	case schema.KindParse:
		return fmt.Sprintf("%s: unparsable in %d %s (%s)\n", key.Property, n, noun, key.Detail)
	}
	return ""
}
