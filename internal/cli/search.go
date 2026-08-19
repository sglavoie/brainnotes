package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	editutil "github.com/sglavoie/sglavoie_brainnotes/internal/editor"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// searchOpenExec is the editor invocation used by `search --open`.
// Tests swap it to avoid exec-replacing the test process.
var searchOpenExec = func(editor, file string, line int) error {
	return runEditorAtLine(editor, file, line)
}

// NewSearchCmd returns the `search` subcommand. Exported for tests.
func NewSearchCmd() *cobra.Command {
	var (
		regex         bool
		caseSensitive bool
		nullSep       bool
		interactive   bool
		contextN      int
		count         bool
		openResult    bool
	)

	ff := new(findFlags)

	cmd := &cobra.Command{
		Use:   "search [pattern] [note...]",
		Short: "Search note bodies for a pattern",
		Long: `Search note bodies for a text pattern and print matching note paths (one per line).
By default the search is case-insensitive and treats the pattern as a literal string.

Output modes:
  paths (default)  One absolute path per matching note, sorted.
  --context N      Each match with N lines of surrounding body context (grep-style).
  --count          Match count per note, sorted by count descending then path.
  --open           Open the first matching result in the configured editor at the matching line.

Find flags (--tag, --category, --status, --type, --folder, etc.) scope the search to
matching notes. Positional note arguments and find flags are mutually exclusive.

Examples:
  bn search "activation energy" --tag physics
  bn search "TODO" --context 2
  bn search "meeting" --count
  bn search "activation energy" --open
  bn find --tag drafts | bn search "review" -`,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("search: no config in context")
			}
			openMatch := func(path string, line int) error {
				editorSpec, err := editutil.Spec(cfg.Open.Editor, os.Getenv("EDITOR"))
				if errors.Is(err, editutil.ErrNoEditorConfigured) {
					return errors.New("search: --open requires [open].editor or $EDITOR")
				}
				if err != nil {
					return fmt.Errorf("search: %w", err)
				}
				if err := searchOpenExec(editorSpec, path, line); err != nil {
					return fmt.Errorf("search: %w", err)
				}
				return nil
			}

			var pattern string
			var noteArgs []string
			if len(args) == 0 {
				if !interactive {
					return errors.New("search: pattern required; use -i for interactive mode without a pattern")
				}
			} else {
				pattern = args[0]
				noteArgs = args[1:]
			}

			var matcher func(string) bool
			if pattern != "" {
				var err error
				matcher, err = buildMatcher(pattern, regex, caseSensitive)
				if err != nil {
					return err
				}
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
				if openResult {
					return errors.New("search: --context cannot be combined with --open")
				}
			}
			if count {
				if interactive {
					return errors.New("search: --count cannot be combined with --interactive")
				}
				if nullSep {
					return errors.New("search: --count cannot be combined with --null")
				}
				if openResult {
					return errors.New("search: --count cannot be combined with --open")
				}
			}
			if openResult && nullSep {
				return errors.New("search: --open cannot be combined with --null")
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

			if interactive {
				var m func(string) bool
				if pattern != "" {
					m = matcher
				}
				corpusLines, err := buildInteractiveLines(notes, v.Root, m)
				if err != nil {
					return err
				}
				fzfArgs := cfg.Fzf.Args
				hasUserPreview := false
				for _, a := range fzfArgs {
					if a == "--preview" || strings.HasPrefix(a, "--preview=") {
						hasUserPreview = true
						break
					}
				}
				if !hasUserPreview {
					var preview string
					if pattern != "" {
						preview = buildHighlightPreview(pattern, regex, caseSensitive)
					} else {
						preview = buildLiveQueryHighlightPreview()
					}
					inject := []string{"--preview", preview}
					fzfArgs = append(inject, fzfArgs...)
				}
				if openResult {
					selected, err := runFZFLinesSelections(corpusLines, fzfArgs)
					if err != nil {
						return err
					}
					if len(selected) == 0 {
						return nil
					}
					return openMatch(selected[0].path, selected[0].line)
				}
				selected, err := RunFZFLines(corpusLines, fzfArgs)
				if err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				sep := byte('\n')
				if nullSep {
					sep = 0
				}
				for _, p := range selected {
					fmt.Fprintf(out, "%s%c", p, sep)
				}
				return nil
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

			if openResult {
				var matched []searchMatch
				for _, n := range notes {
					line, ok, err := firstMatchingLine(n.Path, matcher)
					if err != nil {
						return err
					}
					if ok {
						matched = append(matched, searchMatch{path: n.Path, line: line})
					}
				}
				sort.Slice(matched, func(i, j int) bool {
					return matched[i].path < matched[j].path
				})
				if len(matched) == 0 {
					return nil
				}
				return openMatch(matched[0].path, matched[0].line)
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
	f.BoolVar(&openResult, "open", false, "open the first matching result in the configured editor at the matching line")

	return cmd
}
