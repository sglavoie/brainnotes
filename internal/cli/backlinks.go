package cli

import (
	"fmt"
	"io"
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
	var opts linkListOptions

	cmd := &cobra.Command{
		Use:           "backlinks [note]",
		Short:         "List notes that link to <note>",
		Long:          "List the paths of notes that reference the target note. The target may be specified as a title (case-insensitive stem) or a path/glob. When called with no arguments, an fzf picker lets you select the target interactively.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.verbose = rootVerbose(cmd)
			return runBacklinksCommand(cmd, args, opts)
		},
	}

	f := cmd.Flags()
	f.BoolVarP(&opts.nullSep, "null", "0", false, "separate paths with NUL instead of newline")

	return cmd
}

// backlinkReferrers returns the sorted, deduped, self-filtered list of paths
// that reference target according to the graph's backward index.
func backlinkReferrers(g *query.Graph, target *vault.Note) []string {
	seen := make(map[string]struct{})
	for _, p := range g.BackLinksForNote(target) {
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
func writeVerboseBacklinks(out io.Writer, notes []*vault.Note, target *vault.Note, referrers []string, g *query.Graph) error {
	rows := collectVerboseRows(notes, target, referrers, g)
	sortVerboseRows(rows)
	for _, r := range rows {
		if _, err := fmt.Fprintln(out, r.format()); err != nil {
			return err
		}
	}
	return nil
}

// collectVerboseRows gathers one row per occurrence of a link to target, body
// rows before frontmatter rows for a given note. A note whose body or
// frontmatter will not parse contributes whichever half did parse rather than
// failing the command.
func collectVerboseRows(notes []*vault.Note, target *vault.Note, referrers []string, g *query.Graph) []verboseRow {
	wantKey := g.KeyForNote(target)
	referrerSet := make(map[string]struct{}, len(referrers))
	for _, p := range referrers {
		referrerSet[p] = struct{}{}
	}
	filter := func(link wikilink.Link) bool {
		return linkMatchesGraphKey(g, link, wantKey)
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
			rows = append(rows, renderVerboseBody(n.Path, body, filter)...)
		}
		if fm, err := n.Front(); err == nil {
			rows = append(rows, renderVerboseFront(n.Path, fm, filter)...)
		}
	}
	return rows
}

// sortVerboseRows orders rows by path, frontmatter rows before body rows for the
// same path, then by line number. Stable, so two frontmatter rows on the same
// note keep their key order.
func sortVerboseRows(rows []verboseRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].path != rows[j].path {
			return rows[i].path < rows[j].path
		}
		if rows[i].isFront != rows[j].isFront {
			return rows[i].isFront
		}
		return rows[i].line < rows[j].line
	})
}

func renderBodyLinks(path, body string, filter func(wikilink.Link) bool) []verboseRow {
	var rows []verboseRow
	for _, link := range wikilink.Parse(body) {
		if !filter(link) {
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

func renderFrontLinks(path string, fm *frontmatter.Map, filter func(wikilink.Link) bool) []verboseRow {
	var rows []verboseRow
	for _, k := range fm.Keys() {
		v, _ := fm.Get(k)
		rows = append(rows, scanFrontLinks(path, k, v, filter)...)
	}
	return rows
}

func scanFrontLinks(path, key string, v any, filter func(wikilink.Link) bool) []verboseRow {
	switch vv := v.(type) {
	case string:
		for _, link := range wikilink.Parse(vv) {
			if filter(link) {
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

func renderVerboseBody(path, body string, filter func(wikilink.Link) bool) []verboseRow {
	return renderBodyLinks(path, body, filter)
}

func renderVerboseFront(path string, fm *frontmatter.Map, filter func(wikilink.Link) bool) []verboseRow {
	return renderFrontLinks(path, fm, filter)
}

func linkMatchesGraphKey(g *query.Graph, link wikilink.Link, wantKey string) bool {
	if wantKey == "" {
		return false
	}
	for _, key := range graphLinkKeys(g, link) {
		if key == wantKey {
			return true
		}
	}
	return false
}

func graphLinkKeys(g *query.Graph, link wikilink.Link) []string {
	if g == nil {
		return nil
	}
	if link.Folder != "" {
		return graphFolderLinkKeys(g, link)
	}

	titleKey := relinkNormalizeTitle(link.Title())
	if titleKey == "" {
		return nil
	}
	return append([]string(nil), g.NoteKeysByTitle[titleKey]...)
}

// graphFolderLinkKeys resolves a folder-prefixed link to graph keys: the exact
// path key first, then the title, and only when the title names exactly one
// note. A bare [[Title]] is handled by graphLinkKeys itself, where an ambiguous
// title legitimately yields every candidate.
func graphFolderLinkKeys(g *query.Graph, link wikilink.Link) []string {
	targetKey := relinkNormalizeTarget(link.Target)
	if targetKey == "" {
		return nil
	}
	if _, ok := g.Titles[targetKey]; ok {
		return []string{targetKey}
	}
	titleKey := relinkNormalizeTitle(link.Title())
	if titleKey == "" {
		return nil
	}
	if keys := g.NoteKeysByTitle[titleKey]; len(keys) == 1 {
		return append([]string(nil), keys...)
	}
	return nil
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
