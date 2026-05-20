package cli

import (
	"errors"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// NewUnsetCmd returns the `unset` subcommand. Exported for tests.
func NewUnsetCmd() *cobra.Command {
	var (
		nullSep bool
		dryRun  bool
	)

	cmd := &cobra.Command{
		Use:   "unset <note...> [--] <property...>",
		Short: "Remove frontmatter properties from one or more notes",
		Long: `Unset removes frontmatter properties from one or more notes.

Without '--', the first positional arg is the note path (or '-' for stdin) and
all subsequent args are property names. To pass multiple notes use '--' to
separate them from the property list: 'bn unset a.md b.md -- prop1 prop2'.

Removing a missing property is a silent no-op.

Use --dry-run to preview which properties would be removed without writing.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("unset: no config in context")
			}
			v, err := vault.Open(cfg)
			if err != nil {
				return err
			}

			noteArgs, props := splitUnsetArgs(args, cmd.ArgsLenAtDash())
			if len(noteArgs) == 0 {
				return errors.New("unset: at least one note path is required")
			}
			if len(props) == 0 {
				return errors.New("unset: at least one property name is required")
			}
			for _, p := range props {
				if p == "" {
					return errors.New("unset: empty property name")
				}
			}

			paths, err := resolveNoteInputs(v.Root, noteArgs, cmd.InOrStdin(), nullSep)
			if err != nil {
				return err
			}
			sort.Strings(paths)

			out := cmd.OutOrStdout()
			var dry *DryRun
			if dryRun {
				dry = &DryRun{Out: out}
			}
			for _, p := range paths {
				changed, err := applyUnset(p, props, cfg, v.Root, dry)
				if err != nil {
					return err
				}
				if changed && dry == nil {
					if _, err := fmt.Fprintf(out, "%s: updated\n", p); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&nullSep, "null", "0", false, "read NUL-separated paths from stdin")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview removals without writing")
	return cmd
}

// splitUnsetArgs partitions args into note paths and property names. Cobra
// strips '--' from args but exposes its position via ArgsLenAtDash. When no
// '--' was supplied, the first arg is the note (or '-') and the rest are
// property names.
func splitUnsetArgs(args []string, dashAt int) (notes, props []string) {
	if dashAt >= 0 {
		return args[:dashAt], args[dashAt:]
	}
	if len(args) < 2 {
		return args, nil
	}
	return args[:1], args[1:]
}

// applyUnset loads the note, removes every named property that exists, and
// saves atomically. Returns (changed, err) so callers can keep silent on
// no-op invocations as the spec requires.
//
// When dry is non-nil, the file is left untouched and a per-file `- key: value`
// block is emitted for every present property in props.
func applyUnset(path string, props []string, cfg *config.Config, vaultRoot string, dry *DryRun) (bool, error) {
	n := &vault.Note{Path: path}
	if err := n.Load(); err != nil {
		return false, err
	}
	front, err := n.Front()
	if err != nil {
		return false, err
	}
	if dry != nil {
		block := newFileBlock(displayPath(path, vaultRoot))
		// Deduplicate property names so callers passing the same key twice
		// (e.g. via shell expansion) do not see the same `- key: value` line
		// repeated. The non-dry path is naturally idempotent because the
		// second Unset finds Has=false; the dry preview must match.
		seen := make(map[string]struct{}, len(props))
		for _, p := range props {
			if _, dup := seen[p]; dup {
				continue
			}
			seen[p] = struct{}{}
			v, ok := front.Get(p)
			if !ok {
				continue
			}
			block.removed(p, v)
		}
		if err := block.flush(dry.Out); err != nil {
			return false, err
		}
		return !block.empty(), nil
	}
	changed := false
	for _, p := range props {
		if front.Has(p) {
			front.Unset(p)
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	if err := n.Save(&cfg.Frontmatter); err != nil {
		return false, err
	}
	return true, nil
}
