package cli

import (
	"errors"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// NewFindCmd returns the `find` subcommand. Exported for tests.
func NewFindCmd() *cobra.Command {
	var (
		tags        []string
		categories  []string
		status      string
		typeProp    string
		hasProp     string
		missingProp string
		age         string
		created     string
		orphan      bool
		folder      string
		interactive bool
		nullSep     bool
	)

	cmd := &cobra.Command{
		Use:           "find",
		Short:         "Find notes by frontmatter criteria",
		Long:          "Find notes whose frontmatter matches every supplied filter. Prints matching absolute paths, one per line (or NUL-separated with -0). Pipe into fzf with -i for an interactive picker.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("find: no config in context")
			}
			v, err := vault.Open(cfg)
			if err != nil {
				return err
			}
			notes, err := v.Notes()
			if err != nil {
				return err
			}

			flags := query.Flags{
				Tags:       tags,
				Categories: categories,
				Status:     status,
				Type:       typeProp,
				Has:        hasProp,
				Missing:    missingProp,
				Age:        age,
				Created:    created,
				Folder:     folder,
				Orphan:     orphan,
				Root:       v.Root,
			}
			if orphan {
				flags.BacklinkIndex = query.BuildBacklinkIndex(notes)
			}
			pred, err := query.FromFlags(flags)
			if err != nil {
				return err
			}

			var paths []string
			for _, n := range notes {
				if pred(n) {
					paths = append(paths, n.Path)
				}
			}
			sort.Strings(paths)

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
	f.StringSliceVar(&tags, "tag", nil, "filter by tag (repeatable; AND semantics)")
	f.StringSliceVar(&categories, "category", nil, "filter by category target (repeatable; AND semantics)")
	f.StringVar(&status, "status", "", "filter by status property")
	f.StringVar(&typeProp, "type", "", "filter by type/kind property")
	f.StringVar(&hasProp, "has", "", "require property present")
	f.StringVar(&missingProp, "missing", "", "require property absent")
	f.StringVar(&age, "age", "", "filter by 'last' property age, e.g. >30d, <7d, =0d")
	f.StringVar(&created, "created", "", "filter by 'created' property age, e.g. >30d, <7d, =0d")
	f.StringVar(&folder, "folder", "", "limit to a vault-relative folder (matches descendants)")
	f.BoolVar(&orphan, "orphan", false, "only notes nothing in the vault links to")
	f.BoolVarP(&interactive, "interactive", "i", false, "pipe matches into fzf for selection")
	f.BoolVarP(&nullSep, "null", "0", false, "separate paths with NUL instead of newline")

	return cmd
}
