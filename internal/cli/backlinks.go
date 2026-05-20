package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
)

// maxVerboseSnippet is the rune-length cap for verbose-mode snippet text. The
// 120-rune budget is wide enough for typical prose but narrow enough to keep
// terminal output one line tall.
const maxVerboseSnippet = 120

// NewBacklinksCmd returns the `backlinks` subcommand. Exported for tests.
func NewBacklinksCmd() *cobra.Command {
	var nullSep bool
	var verbose bool

	cmd := &cobra.Command{
		Use:           "backlinks [note]",
		Short:         "List notes that link to <note>",
		Long:          "List the paths of notes that reference the target note. The target may be specified as a title (case-insensitive stem) or a path/glob. When called with no arguments, an fzf picker lets you select the target interactively.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("backlinks: no config in context")
			}
			v, err := vault.Open(cfg)
			if err != nil {
				return err
			}

			var target *vault.Note
			if len(args) == 0 {
				paths, walkErr := collectNotePaths(v)
				if walkErr != nil {
					return walkErr
				}
				if len(paths) == 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "backlinks: no notes in vault")
					return nil
				}
				extraArgs := append([]string(nil), cfg.Fzf.Args...)
				selected, pickErr := notePicker(paths, extraArgs)
				if pickErr != nil {
					return pickErr
				}
				if len(selected) == 0 {
					return nil
				}
				target = &vault.Note{Path: selected[0]}
			} else {
				var resolveErr error
				target, resolveErr = resolveBacklinkTarget(v, args[0])
				if resolveErr != nil {
					return resolveErr
				}
			}

			notes, err := v.Notes()
			if err != nil {
				return err
			}
			graph := query.BuildGraph(notes)

			referrers := backlinkReferrers(graph, target)

			out := cmd.OutOrStdout()
			if verbose {
				return writeVerboseBacklinks(out, notes, target, referrers)
			}

			sep := byte('\n')
			if nullSep {
				sep = 0
			}
			for _, p := range referrers {
				if _, err := fmt.Fprintf(out, "%s%c", p, sep); err != nil {
					return err
				}
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.BoolVarP(&nullSep, "null", "0", false, "separate paths with NUL instead of newline")
	// Long-only `--verbose`: a `-v` short would shadow the root `--verbose`.
	f.BoolVar(&verbose, "verbose", false, "show file:line and a snippet for each backlink (long-only; -v is reserved by the root command)")

	return cmd
}

// resolveBacklinkTarget turns a positional argument into a *vault.Note. It
// first attempts a case-insensitive title lookup; if no note is titled that
// way it falls back to treating the arg as a path/glob. An ambiguity error
// from the title lookup is propagated as-is.
func resolveBacklinkTarget(v *vault.Vault, arg string) (*vault.Note, error) {
	n, err := v.ResolveByTitle(arg, "")
	if err == nil {
		return n, nil
	}
	// Propagate ambiguity errors directly. "no note titled" is the cue to fall
	// back to path/glob resolution.
	if !strings.Contains(err.Error(), "no note titled") {
		return nil, err
	}

	paths, pathErr := resolveNoteInputs(v.Root, []string{arg}, nil, false)
	if pathErr != nil {
		return nil, err
	}
	if len(paths) != 1 {
		return nil, err
	}
	if _, statErr := os.Stat(paths[0]); statErr != nil {
		return nil, err
	}
	return &vault.Note{Path: paths[0]}, nil
}

// backlinkReferrers returns the sorted, deduped, self-filtered list of paths
// that reference target according to the graph's backward index.
func backlinkReferrers(g *query.Graph, target *vault.Note) []string {
	key := backlinkKey(target)
	seen := make(map[string]struct{})
	for _, p := range g.BackLinks(key) {
		if p == target.Path {
			continue
		}
		seen[p] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// backlinkKey returns the lowercased filename stem used as the backlink-index
// lookup key for a note.
func backlinkKey(n *vault.Note) string {
	stem := strings.TrimSuffix(filepath.Base(n.Path), filepath.Ext(n.Path))
	return strings.ToLower(stem)
}

// verboseRow is a single line of verbose-mode output. Frontmatter rows have
// isFront=true and no line number.
type verboseRow struct {
	path    string
	line    int
	isFront bool
	key     string
	text    string
}

func (r verboseRow) format() string {
	if r.isFront {
		return fmt.Sprintf("%s:fm:%s\t%s", r.path, r.key, r.text)
	}
	return fmt.Sprintf("%s:%d\t%s", r.path, r.line, r.text)
}

// writeVerboseBacklinks emits one row per occurrence of a link to target inside
// each referrer's body or frontmatter. Rows are sorted by path, frontmatter
// rows before body rows for the same path, then by line number.
func writeVerboseBacklinks(out io.Writer, notes []*vault.Note, target *vault.Note, referrers []string) error {
	wantKey := backlinkKey(target)
	referrerSet := make(map[string]struct{}, len(referrers))
	for _, p := range referrers {
		referrerSet[p] = struct{}{}
	}

	var rows []verboseRow
	for _, n := range notes {
		if n == nil {
			continue
		}
		if _, ok := referrerSet[n.Path]; !ok {
			continue
		}
		if body, err := n.Body(); err == nil {
			rows = append(rows, renderVerboseBody(n.Path, body, wantKey)...)
		}
		if fm, err := n.Front(); err == nil {
			rows = append(rows, renderVerboseFront(n.Path, fm, wantKey)...)
		}
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].path != rows[j].path {
			return rows[i].path < rows[j].path
		}
		if rows[i].isFront != rows[j].isFront {
			return rows[i].isFront
		}
		return rows[i].line < rows[j].line
	})

	for _, r := range rows {
		if _, err := fmt.Fprintln(out, r.format()); err != nil {
			return err
		}
	}
	return nil
}

func renderBodyLinks(path, body string, filter func(string) bool) []verboseRow {
	var rows []verboseRow
	for _, link := range wikilink.Parse(body) {
		if !filter(strings.ToLower(link.Title())) {
			continue
		}
		lineNum := strings.Count(body[:link.Start], "\n") + 1
		ls := strings.LastIndexByte(body[:link.Start], '\n') + 1
		le := strings.IndexByte(body[link.End:], '\n')
		if le < 0 {
			le = len(body)
		} else {
			le += link.End
		}
		snippet := truncateSnippet(strings.TrimSpace(body[ls:le]))
		rows = append(rows, verboseRow{path: path, line: lineNum, text: snippet})
	}
	return rows
}

func renderFrontLinks(path string, fm *frontmatter.Map, filter func(string) bool) []verboseRow {
	var rows []verboseRow
	for _, k := range fm.Keys() {
		v, _ := fm.Get(k)
		rows = append(rows, scanFrontLinks(path, k, v, filter)...)
	}
	return rows
}

func scanFrontLinks(path, key string, v any, filter func(string) bool) []verboseRow {
	switch vv := v.(type) {
	case string:
		for _, link := range wikilink.Parse(vv) {
			if filter(strings.ToLower(link.Title())) {
				return []verboseRow{{path: path, key: key, isFront: true, text: truncateSnippet(vv)}}
			}
		}
		return nil
	case []any:
		var rows []verboseRow
		for _, item := range vv {
			rows = append(rows, scanFrontLinks(path, key, item, filter)...)
		}
		return rows
	case map[string]any:
		var rows []verboseRow
		for _, item := range vv {
			rows = append(rows, scanFrontLinks(path, key, item, filter)...)
		}
		return rows
	}
	return nil
}

func renderVerboseBody(path, body, wantKey string) []verboseRow {
	return renderBodyLinks(path, body, func(t string) bool { return t == wantKey })
}

func renderVerboseFront(path string, fm *frontmatter.Map, wantKey string) []verboseRow {
	return renderFrontLinks(path, fm, func(t string) bool { return t == wantKey })
}

// truncateSnippet caps s at maxVerboseSnippet runes, replacing the tail with
// `…` when truncated. Rune-based so multibyte characters don't get split.
func truncateSnippet(s string) string {
	if utf8.RuneCountInString(s) <= maxVerboseSnippet {
		return s
	}
	count := 0
	for i := range s {
		if count == maxVerboseSnippet-1 {
			return s[:i] + "…"
		}
		count++
	}
	return s
}
