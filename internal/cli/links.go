package cli

import (
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func NewLinksCmd() *cobra.Command {
	var nullSep bool
	var verbose bool

	cmd := &cobra.Command{
		Use:           "links [note]",
		Short:         "List notes that <note> links to",
		Long:          "List the paths of notes that the target note references via wikilinks. The target may be specified as a title (case-insensitive stem) or a path/glob. When called with no arguments, an fzf picker lets you select the target interactively.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("links: no config in context")
			}
			if nullSep && verbose {
				return errors.New("links: --null cannot be used with --verbose")
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
					fmt.Fprintln(cmd.ErrOrStderr(), "links: no notes in vault")
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
			g := query.BuildGraph(notes)

			targetKey := backlinkKey(target)
			fwdTitles := g.ForwardLinks(targetKey)

			if verbose {
				return writeVerboseLinks(cmd.OutOrStdout(), target, g, fwdTitles)
			}

			paths := make([]string, 0, len(fwdTitles))
			for _, title := range fwdTitles {
				if p, ok := g.PathsByTitle[title]; ok {
					paths = append(paths, p)
				}
			}
			sort.Strings(paths)

			sep := byte('\n')
			if nullSep {
				sep = 0
			}
			out := cmd.OutOrStdout()
			for _, p := range paths {
				if _, err := fmt.Fprintf(out, "%s%c", p, sep); err != nil {
					return err
				}
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.BoolVarP(&nullSep, "null", "0", false, "separate paths with NUL instead of newline")
	f.BoolVar(&verbose, "verbose", false, "show file:line and a snippet for each forward link (long-only; -v is reserved by the root command)")

	return cmd
}

func writeVerboseLinks(out io.Writer, target *vault.Note, g *query.Graph, fwdTitles []string) error {
	targetKey := backlinkKey(target)
	fwdSet := make(map[string]struct{}, len(fwdTitles))
	for _, t := range fwdTitles {
		fwdSet[t] = struct{}{}
	}

	filter := func(t string) bool {
		if t == targetKey {
			return false
		}
		_, ok := fwdSet[t]
		return ok
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
