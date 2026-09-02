package cli

import (
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
)

// runLinksCommand lists the notes the target links out to. Both of its output
// forms render from the same resolved state, so the mode selection is a single
// branch rather than a dispatcher.
func runLinksCommand(cmd *cobra.Command, args []string, opts linkListOptions) error {
	r, ok, err := newLinkListRun(cmd, "links", args, opts)
	if err != nil || !ok {
		return err
	}

	out := cmd.OutOrStdout()
	if opts.verbose {
		return writeVerboseLinks(out, r.target, r.graph)
	}
	return writePathList(out, forwardLinkPaths(r.graph, r.target), opts.nullSep)
}

// forwardLinkPaths returns the sorted paths of the notes the target resolves a
// forward link to. A forward link whose title names no note in the vault has no
// path and is therefore absent from this list; `bn find --broken-links` is the
// command that reports those.
func forwardLinkPaths(g *query.Graph, target *vault.Note) []string {
	titles := g.ForwardLinksForNote(target)
	paths := make([]string, 0, len(titles))
	for _, title := range titles {
		if p, ok := g.PathsByTitle[title]; ok {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	return paths
}

// writeVerboseLinks emits one row per occurrence of a resolved forward link
// inside the target's own body or frontmatter. Frontmatter rows sort ahead of
// body rows; there is only ever one path, so path is not part of the ordering
// as it is in writeVerboseBacklinks.
func writeVerboseLinks(out io.Writer, target *vault.Note, g *query.Graph) error {
	fwdKeys := g.ForwardLinksForNote(target)
	fwdSet := make(map[string]struct{}, len(fwdKeys))
	for _, key := range fwdKeys {
		fwdSet[key] = struct{}{}
	}

	filter := func(link wikilink.Link) bool {
		for _, key := range graphLinkKeys(g, link) {
			if _, ok := fwdSet[key]; ok {
				return true
			}
		}
		return false
	}

	var rows []verboseRow
	if body, err := target.Body(); err == nil {
		rows = append(rows, renderBodyLinks(target.Path, body, filter)...)
	}
	if fm, err := target.Front(); err == nil {
		rows = append(rows, renderFrontLinks(target.Path, fm, filter)...)
	}

	sort.SliceStable(rows, func(i, j int) bool {
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
