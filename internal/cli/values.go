package cli

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// valuesOptions captures the flag inputs shared by `bn values` and `bn tags`.
type valuesOptions struct {
	sortMode  string
	showCount bool
}

// NewValuesCmd returns the `values` subcommand. Exported for tests.
func NewValuesCmd() *cobra.Command {
	opts := valuesOptions{sortMode: "count", showCount: true}

	cmd := &cobra.Command{
		Use:           "values <property>",
		Short:         "Count occurrences of each value for a frontmatter property",
		Long:          "Walk every note in the vault, parse frontmatter, and count occurrences of each value for the named property. Useful for hygiene: spotting typos and stray variants in the long tail.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValues(cmd, args[0], opts)
		},
	}
	addValuesFlags(cmd, &opts)
	return cmd
}

// NewTagsCmd returns the `tags` subcommand, an alias for `values tags`.
func NewTagsCmd() *cobra.Command {
	opts := valuesOptions{sortMode: "count", showCount: true}

	cmd := &cobra.Command{
		Use:           "tags",
		Short:         "Count tag occurrences across the vault (alias for `values tags`)",
		Long:          "Alias for `bn values tags`. Walks every note and counts how often each tag appears in the `tags` frontmatter property.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runValues(cmd, "tags", opts)
		},
	}
	addValuesFlags(cmd, &opts)
	return cmd
}

func addValuesFlags(cmd *cobra.Command, opts *valuesOptions) {
	f := cmd.Flags()
	f.StringVar(&opts.sortMode, "sort", "count", "sort by 'count' (desc, alpha tie-break) or 'name' (alpha)")
	f.BoolVar(&opts.showCount, "count", true, "prefix each line with its count (--count=false emits values only)")
}

func runValues(cmd *cobra.Command, property string, opts valuesOptions) error {
	if opts.sortMode != "count" && opts.sortMode != "name" {
		return fmt.Errorf("--sort: invalid value %q (must be 'count' or 'name')", opts.sortMode)
	}

	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("values: no config in context")
	}
	v, err := vault.Open(cfg)
	if err != nil {
		return err
	}
	notes, err := v.Notes()
	if err != nil {
		return err
	}

	verbose, _ := cmd.Root().PersistentFlags().GetBool("verbose")
	errOut := cmd.ErrOrStderr()

	counts := map[string]int{}
	warnedNotes := map[string]struct{}{}
	for _, n := range notes {
		if n == nil {
			continue
		}
		fm, err := n.Front()
		if err != nil {
			continue
		}
		raw, ok := fm.Get(property)
		if !ok {
			continue
		}
		countValue(raw, n.Path, counts, warnedNotes, verbose, errOut)
	}

	type entry struct {
		value string
		count int
	}
	entries := make([]entry, 0, len(counts))
	for k, c := range counts {
		entries = append(entries, entry{value: k, count: c})
	}
	sort.Slice(entries, func(i, j int) bool {
		if opts.sortMode == "name" {
			return entries[i].value < entries[j].value
		}
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].value < entries[j].value
	})

	out := cmd.OutOrStdout()
	if !opts.showCount {
		for _, e := range entries {
			if _, err := fmt.Fprintln(out, e.value); err != nil {
				return err
			}
		}
		return nil
	}

	width := 0
	for _, e := range entries {
		if w := len(strconv.Itoa(e.count)); w > width {
			width = w
		}
	}
	for _, e := range entries {
		if _, err := fmt.Fprintf(out, "%*d  %s\n", width, e.count, e.value); err != nil {
			return err
		}
	}
	return nil
}

// countValue increments counts for v's contributions, recursing into lists.
// Maps are skipped; with verbose=true, one warning is emitted per affected note.
func countValue(v any, notePath string, counts map[string]int, warnedNotes map[string]struct{}, verbose bool, errOut io.Writer) {
	switch vv := v.(type) {
	case nil:
		return
	case string:
		counts[vv]++
	case []any:
		for _, item := range vv {
			countValue(item, notePath, counts, warnedNotes, verbose, errOut)
		}
	case map[string]any:
		if !verbose {
			return
		}
		if _, seen := warnedNotes[notePath]; seen {
			return
		}
		warnedNotes[notePath] = struct{}{}
		fmt.Fprintf(errOut, "values: skipping map-typed value in %s\n", notePath)
	case time.Time:
		counts[formatTimeValue(vv)]++
	case bool:
		counts[strconv.FormatBool(vv)]++
	case int:
		counts[strconv.Itoa(vv)]++
	case float64:
		counts[strconv.FormatFloat(vv, 'g', -1, 64)]++
	default:
		counts[fmt.Sprintf("%v", vv)]++
	}
}

// formatTimeValue renders a time.Time as a date when the parsed value had no
// time component (frontmatter parser yields midnight UTC for `YYYY-MM-DD`);
// otherwise as RFC3339. The heuristic is the only way to distinguish the two
// since yaml.v3 surfaces both as time.Time without a marker.
func formatTimeValue(t time.Time) string {
	if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 && t.Nanosecond() == 0 && t.Location() == time.UTC {
		return t.Format("2006-01-02")
	}
	return t.Format(time.RFC3339)
}
