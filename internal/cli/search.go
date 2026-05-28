package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// NewSearchCmd returns the `search` subcommand. Exported for tests.
func NewSearchCmd() *cobra.Command {
	var (
		regex         bool
		caseSensitive bool
		nullSep       bool
		interactive   bool
		contextN      int
		count         bool
	)

	ff := new(findFlags)

	cmd := &cobra.Command{
		Use:   "search <pattern> [note...]",
		Short: "Search note bodies for a pattern",
		Long: `Search note bodies for a text pattern and print matching note paths (one per line).
By default the search is case-insensitive and treats the pattern as a literal string.

Output modes:
  paths (default)  One absolute path per matching note, sorted.
  --context N      Each match with N lines of surrounding body context (grep-style).
  --count          Match count per note, sorted by count descending then path.

Find flags (--tag, --category, --status, --type, --folder, etc.) scope the search to
matching notes. Positional note arguments and find flags are mutually exclusive.

Examples:
  bn search "activation energy" --tag physics
  bn search "TODO" --context 2
  bn search "meeting" --count
  bn find --tag drafts | bn search "review" -`,
		Args:          cobra.MinimumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("search: no config in context")
			}

			pattern := args[0]
			noteArgs := args[1:]

			matcher, err := buildMatcher(pattern, regex, caseSensitive)
			if err != nil {
				return err
			}

			anyFlagSet := false
			for _, name := range queryFlagNames {
				if f := cmd.Flag(name); f != nil && f.Changed {
					anyFlagSet = true
					break
				}
			}
			if len(noteArgs) > 0 && anyFlagSet {
				return errors.New("search: note arguments and find flags are mutually exclusive")
			}
			if contextN > 0 {
				if count {
					return errors.New("search: --context cannot be combined with --count")
				}
				if interactive {
					return errors.New("search: --context cannot be combined with --interactive")
				}
				if nullSep {
					return errors.New("search: --context cannot be combined with --null")
				}
			}
			if count {
				if interactive {
					return errors.New("search: --count cannot be combined with --interactive")
				}
				if nullSep {
					return errors.New("search: --count cannot be combined with --null")
				}
			}

			v, err := vault.Open(cfg)
			if err != nil {
				return err
			}

			var notes []*vault.Note
			switch {
			case len(noteArgs) > 0:
				paths, err := resolveNoteInputs(v.Root, noteArgs, cmd.InOrStdin(), nullSep)
				if err != nil {
					return err
				}
				allNotes, err := v.Notes()
				if err != nil {
					return err
				}
				pathSet := make(map[string]bool, len(paths))
				for _, p := range paths {
					pathSet[p] = true
				}
				for _, n := range allNotes {
					if pathSet[n.Path] {
						notes = append(notes, n)
					}
				}
			case anyFlagSet:
				allNotes, err := v.Notes()
				if err != nil {
					return err
				}
				var g *query.Graph
				if ff.needsGraph() {
					g = query.BuildGraphForRoot(v.Root, allNotes)
				}
				flags := ff.toQueryFlags(v.Root, g)
				pred, err := query.FromFlags(flags)
				if err != nil {
					return err
				}
				for _, n := range allNotes {
					if pred(n) {
						notes = append(notes, n)
					}
				}
			default:
				notes, err = v.Notes()
				if err != nil {
					return err
				}
			}

			if contextN > 0 {
				sort.Slice(notes, func(i, j int) bool { return notes[i].Path < notes[j].Path })
				out := cmd.OutOrStdout()
				first := true
				for _, n := range notes {
					var buf bytes.Buffer
					if err := contextOutput(&buf, n, v.Root, matcher, contextN); err != nil {
						return err
					}
					if buf.Len() == 0 {
						continue
					}
					if !first {
						fmt.Fprintln(out, "--")
					}
					first = false
					if _, err := out.Write(buf.Bytes()); err != nil {
						return err
					}
				}
				return nil
			}

			if count {
				type countEntry struct {
					count   int
					relpath string
				}
				var results []countEntry
				for _, n := range notes {
					body, err := n.Body()
					if err != nil {
						return err
					}
					if body == "" {
						continue
					}
					c := 0
					for _, line := range strings.Split(body, "\n") {
						if matcher(line) {
							c++
						}
					}
					if c == 0 {
						continue
					}
					rel := n.Path
					if r, err2 := filepath.Rel(v.Root, n.Path); err2 == nil {
						rel = filepath.ToSlash(r)
					}
					results = append(results, countEntry{c, rel})
				}
				sort.Slice(results, func(i, j int) bool {
					if results[i].count != results[j].count {
						return results[i].count > results[j].count
					}
					return results[i].relpath < results[j].relpath
				})
				out := cmd.OutOrStdout()
				for _, r := range results {
					if _, err := fmt.Fprintf(out, "%d\t%s\n", r.count, r.relpath); err != nil {
						return err
					}
				}
				return nil
			}

			var matched []*vault.Note
			for _, n := range notes {
				body, err := n.Body()
				if err != nil {
					return err
				}
				for _, line := range strings.Split(body, "\n") {
					if matcher(line) {
						matched = append(matched, n)
						break
					}
				}
			}
			sort.Slice(matched, func(i, j int) bool {
				return matched[i].Path < matched[j].Path
			})

			paths := make([]string, len(matched))
			for i, n := range matched {
				paths[i] = n.Path
			}

			if interactive {
				selected, err := RunFZF(paths, cfg.Fzf.Args)
				if err != nil {
					return err
				}
				paths = selected
			}

			out := cmd.OutOrStdout()
			sep := byte('\n')
			if nullSep {
				sep = 0
			}
			for _, p := range paths {
				if _, err := fmt.Fprintf(out, "%s%c", p, sep); err != nil {
					return err
				}
			}
			return nil
		},
	}

	f := cmd.Flags()
	ff.register(cmd)
	f.BoolVarP(&regex, "regex", "e", false, "treat pattern as a regular expression")
	f.BoolVar(&caseSensitive, "case-sensitive", false, "case-sensitive match (default: case-insensitive)")
	f.BoolVarP(&nullSep, "null", "0", false, "NUL-separate output paths")
	f.BoolVarP(&interactive, "interactive", "i", false, "pipe matching note paths into fzf")
	f.IntVarP(&contextN, "context", "C", 0, "show N lines of context around each match")
	f.BoolVar(&count, "count", false, "print match count per note, sorted by count desc")

	return cmd
}

func buildMatcher(pattern string, isRegex, caseSensitive bool) (func(string) bool, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, errors.New("search: pattern must not be empty")
	}
	if isRegex {
		rePattern := pattern
		if !caseSensitive {
			rePattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(rePattern)
		if err != nil {
			return nil, fmt.Errorf("search: invalid regex %q: %w", pattern, err)
		}
		return re.MatchString, nil
	}
	if caseSensitive {
		return func(s string) bool {
			return strings.Contains(s, pattern)
		}, nil
	}
	lower := strings.ToLower(pattern)
	return func(s string) bool {
		return strings.Contains(strings.ToLower(s), lower)
	}, nil
}

// window is an inclusive 0-based file-line index range.
type window struct{ lo, hi int }

// mergeWindows sorts windows by lo and merges overlapping or adjacent ones.
func mergeWindows(wins []window) []window {
	if len(wins) == 0 {
		return nil
	}
	sort.Slice(wins, func(i, j int) bool { return wins[i].lo < wins[j].lo })
	merged := []window{wins[0]}
	for _, w := range wins[1:] {
		last := &merged[len(merged)-1]
		if w.lo <= last.hi+1 {
			if w.hi > last.hi {
				last.hi = w.hi
			}
		} else {
			merged = append(merged, w)
		}
	}
	return merged
}

// contextOutput writes grep-style context output for a single note to out.
// It reads the raw file, skips frontmatter, finds matches, builds context
// windows (clamped to body), merges overlapping windows, and emits them
// with "--" separators between non-adjacent blocks.
func contextOutput(out io.Writer, n *vault.Note, vaultRoot string, match func(string) bool, contextN int) error {
	raw, err := os.ReadFile(n.Path)
	if err != nil {
		return err
	}
	fileLines := strings.Split(string(raw), "\n")
	// Drop the trailing empty string left by a file ending with '\n'.
	for len(fileLines) > 0 && fileLines[len(fileLines)-1] == "" {
		fileLines = fileLines[:len(fileLines)-1]
	}
	if len(fileLines) == 0 {
		return nil
	}

	// Locate first body line (after closing "---" of frontmatter).
	bodyStartIdx := 0
	if strings.TrimSpace(fileLines[0]) == "---" {
		for i := 1; i < len(fileLines); i++ {
			if strings.TrimSpace(fileLines[i]) == "---" {
				bodyStartIdx = i + 1
				break
			}
		}
	}
	if bodyStartIdx >= len(fileLines) {
		return nil
	}

	// Collect indices of matching lines.
	var matchIdxs []int
	for i := bodyStartIdx; i < len(fileLines); i++ {
		if match(fileLines[i]) {
			matchIdxs = append(matchIdxs, i)
		}
	}
	if len(matchIdxs) == 0 {
		return nil
	}

	// Build context windows clamped to body range.
	last := len(fileLines) - 1
	wins := make([]window, len(matchIdxs))
	for k, idx := range matchIdxs {
		lo := idx - contextN
		if lo < bodyStartIdx {
			lo = bodyStartIdx
		}
		hi := idx + contextN
		if hi > last {
			hi = last
		}
		wins[k] = window{lo, hi}
	}
	wins = mergeWindows(wins)

	relPath := n.Path
	if rel, err2 := filepath.Rel(vaultRoot, n.Path); err2 == nil {
		relPath = filepath.ToSlash(rel)
	}

	for i, w := range wins {
		if i > 0 {
			fmt.Fprintln(out, "--")
		}
		for lineIdx := w.lo; lineIdx <= w.hi; lineIdx++ {
			lineNum := lineIdx + 1
			text := truncateSnippet(fileLines[lineIdx])
			if match(fileLines[lineIdx]) {
				fmt.Fprintf(out, "%s:%d:%s\n", relPath, lineNum, text)
			} else {
				fmt.Fprintf(out, "%s-%d-%s\n", relPath, lineNum, text)
			}
		}
	}
	return nil
}
