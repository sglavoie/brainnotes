package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/pins"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// NewFindCmd returns the `find` subcommand. Exported for tests.
func NewFindCmd() *cobra.Command {
	var (
		interactive       bool
		nullSep           bool
		showBroken        bool
		listBrokenTargets bool
	)

	ff := new(findFlags)

	cmd := &cobra.Command{
		Use:           "find",
		Short:         "Find notes by frontmatter criteria",
		Long:          "Find notes whose frontmatter matches every supplied filter. Prints matching absolute paths, one per line (or NUL-separated with -0). Pipe into fzf with -i for an interactive picker.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("find: no config in context")
			}
			if showBroken && !ff.brokenLinks {
				return errors.New("--show-broken requires --broken-links")
			}
			if listBrokenTargets && !ff.brokenLinks {
				return errors.New("--list-broken-targets requires --broken-links")
			}
			if showBroken && listBrokenTargets {
				return errors.New("--show-broken and --list-broken-targets are mutually exclusive")
			}
			if (showBroken || listBrokenTargets) && interactive {
				return errors.New("--interactive cannot be used with --show-broken or --list-broken-targets")
			}
			if (showBroken || listBrokenTargets) && nullSep {
				return errors.New("--null cannot be used with --show-broken or --list-broken-targets")
			}
			v, err := vault.Open(cfg)
			if err != nil {
				return err
			}
			notes, err := v.Notes()
			if err != nil {
				return err
			}

			var g *query.Graph
			if ff.needsGraph() {
				g = query.BuildGraphForRoot(v.Root, notes)
			}
			flags := ff.toQueryFlags(v.Root, g)
			pred, err := query.FromFlags(flags)
			if err != nil {
				return err
			}

			var matched []*vault.Note
			for _, n := range notes {
				if pred(n) {
					matched = append(matched, n)
				}
			}
			sort.Slice(matched, func(i, j int) bool {
				return matched[i].Path < matched[j].Path
			})
			if ff.pinned {
				matched = pinnedOrder(v.Root, matched)
			}

			out := cmd.OutOrStdout()

			if showBroken {
				return printShowBroken(out, matched, flags.Graph, v.Root)
			}
			if listBrokenTargets {
				return printListBrokenTargets(out, matched, flags.Graph)
			}

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

	ff.register(cmd)
	f := cmd.Flags()
	f.BoolVarP(&interactive, "interactive", "i", false, "pipe matches into fzf for selection")
	f.BoolVarP(&nullSep, "null", "0", false, "separate paths with NUL instead of newline")
	f.BoolVar(&showBroken, "show-broken", false, "show each broken link (requires --broken-links)")
	f.BoolVar(&listBrokenTargets, "list-broken-targets", false, "list unique broken targets (requires --broken-links)")

	return cmd
}

// pinnedOrder re-sorts matched notes into the canonical pinned order (ascending
// rank, then unranked, path-tiebroken) instead of the default path sort. Any
// note pins.List does not place keeps its path-sorted position at the end,
// which cannot happen while --pinned is the only caller.
func pinnedOrder(root string, matched []*vault.Note) []*vault.Note {
	byRel := make(map[string]*vault.Note, len(matched))
	for _, n := range matched {
		rel, err := filepath.Rel(root, n.Path)
		if err != nil {
			continue
		}
		byRel[filepath.ToSlash(rel)] = n
	}
	ordered := make([]*vault.Note, 0, len(matched))
	placed := make(map[*vault.Note]bool, len(matched))
	for _, e := range pins.List(root, matched) {
		n, ok := byRel[e.RelPath]
		if !ok || placed[n] {
			continue
		}
		placed[n] = true
		ordered = append(ordered, n)
	}
	for _, n := range matched {
		if !placed[n] {
			ordered = append(ordered, n)
		}
	}
	return ordered
}

type brokenRecord struct {
	relpath        string
	line           int
	frontmatterKey string
	raw            string
}

func printShowBroken(out io.Writer, matched []*vault.Note, g *query.Graph, root string) error {
	var records []brokenRecord
	for _, n := range matched {
		rel, err := filepath.Rel(root, n.Path)
		if err != nil {
			rel = n.Path
		}
		for _, bl := range query.UnresolvedLinks(n, g) {
			records = append(records, brokenRecord{
				relpath:        rel,
				line:           bl.Line,
				frontmatterKey: bl.FrontmatterKey,
				raw:            bl.Link.Raw,
			})
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].relpath != records[j].relpath {
			return records[i].relpath < records[j].relpath
		}
		if records[i].line != records[j].line {
			return records[i].line < records[j].line
		}
		if records[i].frontmatterKey != records[j].frontmatterKey {
			return records[i].frontmatterKey < records[j].frontmatterKey
		}
		return records[i].raw < records[j].raw
	})
	for _, r := range records {
		var err error
		if r.frontmatterKey != "" {
			_, err = fmt.Fprintf(out, "%s:frontmatter[%s]: %s\n", r.relpath, r.frontmatterKey, r.raw)
		} else {
			_, err = fmt.Fprintf(out, "%s:%d: %s\n", r.relpath, r.line, r.raw)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func printListBrokenTargets(out io.Writer, matched []*vault.Note, g *query.Graph) error {
	seen := make(map[string]string)
	for _, n := range matched {
		for _, bl := range query.UnresolvedLinks(n, g) {
			title := bl.Link.Title()
			key := strings.ToLower(title)
			if _, ok := seen[key]; !ok {
				seen[key] = title
			}
		}
	}
	targets := make([]string, 0, len(seen))
	for _, title := range seen {
		targets = append(targets, title)
	}
	sort.Slice(targets, func(i, j int) bool {
		return strings.ToLower(targets[i]) < strings.ToLower(targets[j])
	})
	for _, t := range targets {
		if _, err := fmt.Fprintf(out, "%s\n", t); err != nil {
			return err
		}
	}
	return nil
}
