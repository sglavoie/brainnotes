package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

type valEntry struct {
	value string
	count int
}

type valuesOptions struct {
	sortMode  string
	showCount bool
	showFiles bool
}

// NewValuesCmd returns the `values` subcommand. Exported for tests.
func NewValuesCmd() *cobra.Command {
	opts := valuesOptions{sortMode: "count", showCount: true}

	cmd := &cobra.Command{
		Use:           "values [property]",
		Short:         "Count occurrences of each value for a frontmatter property",
		Long:          "Walk every note in the vault, parse frontmatter, and count occurrences of each value for the named property. When called with no arguments, an fzf picker lets you select the property interactively. Useful for hygiene: spotting typos and stray variants in the long tail.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValuesCommand(cmd, args, opts)
		},
	}
	addValuesFlags(cmd, &opts)
	return cmd
}

// runValuesCommand tallies the named property, or the one the picker selects
// when no property is named.
func runValuesCommand(cmd *cobra.Command, args []string, opts valuesOptions) error {
	if len(args) == 1 {
		return runValues(cmd, args[0], opts)
	}
	property, err := pickValuesProperty(cmd)
	if err != nil {
		return err
	}
	return runValues(cmd, property, opts)
}

// pickValuesProperty offers every frontmatter key in the vault. A vault with no
// properties, and a picker that selected nothing, are ErrNothingSelected
// (exit 1), as every other empty picker is.
func pickValuesProperty(cmd *cobra.Command) (string, error) {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return "", errors.New("values: no config in context")
	}
	v, err := vault.Open(cfg)
	if err != nil {
		return "", err
	}
	keys, err := collectPropertyKeys(v)
	if err != nil {
		return "", err
	}
	if len(keys) == 0 {
		return "", nothingSelected("values", "no properties in vault")
	}
	picked, err := propertyPicker(keys, cfg.Fzf.Args)
	selected, err := requireSelection("values", picked, err)
	if err != nil {
		return "", err
	}
	return selected[0], nil
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
	f.BoolVar(&opts.showFiles, "files", false, "list the files containing each value")
}

func runValues(cmd *cobra.Command, property string, opts valuesOptions) error {
	if opts.sortMode != "count" && opts.sortMode != "name" {
		return enumValueError(cmd.Name(), "sort", opts.sortMode, "count", "name")
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

	counts, files := collectValueCounts(v, notes, property, opts.showFiles, rootVerbose(cmd), cmd.ErrOrStderr())
	entries := sortValueEntries(counts, opts.sortMode)
	return writeValuesOutput(cmd.OutOrStdout(), entries, files, opts)
}

// collectValueCounts walks the notes and tallies every occurrence of property.
// A note whose frontmatter will not parse, or which does not carry the property,
// is skipped silently — `values` reports on what it can read rather than
// refusing the whole vault for one bad note. files is nil unless wantFiles.
func collectValueCounts(v *vault.Vault, notes []*vault.Note, property string, wantFiles, verbose bool, errOut io.Writer) (map[string]int, map[string]map[string]struct{}) {
	counts := map[string]int{}
	var files map[string]map[string]struct{}
	if wantFiles {
		files = make(map[string]map[string]struct{})
	}
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
		var relPath string
		if wantFiles {
			relPath, _ = filepath.Rel(v.Root, n.Path)
		}
		countValue(raw, n.Path, relPath, counts, files, warnedNotes, verbose, errOut)
	}
	return counts, files
}

// sortValueEntries orders the tally: by count descending with an alphabetical
// tie-break, or alphabetically when sortMode is "name".
func sortValueEntries(counts map[string]int, sortMode string) []valEntry {
	entries := make([]valEntry, 0, len(counts))
	for k, c := range counts {
		entries = append(entries, valEntry{value: k, count: c})
	}
	sort.Slice(entries, func(i, j int) bool {
		if sortMode == "name" {
			return entries[i].value < entries[j].value
		}
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].value < entries[j].value
	})
	return entries
}

// writeValuesOutput renders the three output forms: the per-value file listing,
// bare values, and the count-prefixed default.
func writeValuesOutput(out io.Writer, entries []valEntry, files map[string]map[string]struct{}, opts valuesOptions) error {
	width := 0
	if opts.showCount {
		width = valueCountWidth(entries)
	}
	if opts.showFiles {
		return writeFilesOutput(out, entries, files, opts.showCount, width)
	}
	for _, e := range entries {
		if !opts.showCount {
			if _, err := fmt.Fprintln(out, e.value); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(out, "%*d  %s\n", width, e.count, e.value); err != nil {
			return err
		}
	}
	return nil
}

// valueCountWidth is the column width that right-aligns every count.
func valueCountWidth(entries []valEntry) int {
	width := 0
	for _, e := range entries {
		if w := len(strconv.Itoa(e.count)); w > width {
			width = w
		}
	}
	return width
}

// countValue increments counts for v's contributions, recursing into lists.
// Maps are skipped; with verbose=true, one warning is emitted per affected note.
func countValue(v any, notePath string, relPath string, counts map[string]int, files map[string]map[string]struct{}, warnedNotes map[string]struct{}, verbose bool, errOut io.Writer) {
	switch vv := v.(type) {
	case nil:
		return
	case []any:
		for _, item := range vv {
			countValue(item, notePath, relPath, counts, files, warnedNotes, verbose, errOut)
		}
	case map[string]any:
		warnSkippedMapValue(notePath, warnedNotes, verbose, errOut)
	default:
		record(formatValueScalar(vv), counts, files, relPath)
	}
}

// formatValueScalar renders one scalar frontmatter value as the string the tally
// is keyed by. The default arm is what keeps an unmodelled YAML scalar counted
// rather than dropped.
func formatValueScalar(v any) string {
	switch vv := v.(type) {
	case string:
		return vv
	case time.Time:
		return frontmatter.FormatTimestamp(vv)
	case bool:
		return strconv.FormatBool(vv)
	case int:
		return strconv.Itoa(vv)
	case float64:
		return strconv.FormatFloat(vv, 'g', -1, 64)
	default:
		return fmt.Sprintf("%v", vv)
	}
}

// warnSkippedMapValue emits at most one map-skipped warning per note, and only
// under --verbose.
func warnSkippedMapValue(notePath string, warnedNotes map[string]struct{}, verbose bool, errOut io.Writer) {
	if !verbose {
		return
	}
	if _, seen := warnedNotes[notePath]; seen {
		return
	}
	warnedNotes[notePath] = struct{}{}
	fmt.Fprintf(errOut, "values: skipping map-typed value in %s\n", notePath)
}

func record(key string, counts map[string]int, files map[string]map[string]struct{}, relPath string) {
	counts[key]++
	if files != nil {
		if files[key] == nil {
			files[key] = map[string]struct{}{}
		}
		files[key][relPath] = struct{}{}
	}
}

func writeFilesOutput(out io.Writer, entries []valEntry, files map[string]map[string]struct{}, showCount bool, countWidth int) error {
	for i, e := range entries {
		if i > 0 {
			if _, err := fmt.Fprintln(out); err != nil {
				return err
			}
		}
		if showCount {
			if _, err := fmt.Fprintf(out, "%*d  %s:\n", countWidth, e.count, e.value); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintf(out, "%s:\n", e.value); err != nil {
				return err
			}
		}
		for _, f := range sortedKeys(files[e.value]) {
			if _, err := fmt.Fprintf(out, "  %s\n", f); err != nil {
				return err
			}
		}
	}
	return nil
}

func sortedKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
