package cli

import (
	"errors"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// NewUnsetCmd returns the `unset` subcommand. Exported for tests.
func NewUnsetCmd() *cobra.Command {
	var (
		nullSep bool
		dryRun  bool
	)

	cmd := &cobra.Command{
		Use:   "unset [note...] [--] <property...>",
		Short: "Remove frontmatter properties from one or more notes",
		Long: `Unset removes frontmatter properties from one or more notes.

Without '--', the first positional arg is the note path (or '-' for stdin) and
all subsequent args are property names. To pass multiple notes use '--' to
separate them from the property list: 'bn unset a.md b.md -- prop1 prop2'.
Use 'bn unset -- prop1 prop2' to pick notes via an fzf multi-select picker.

Removing a missing property is a silent no-op.

Use --dry-run to preview which properties would be removed without writing.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.MinimumNArgs(1),
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
			if nullSep && !usesStdinNoteInput(noteArgs) {
				return errors.New("unset: --null requires '-' stdin note input")
			}
			if len(props) == 0 {
				return errors.New("unset: at least one property name is required")
			}
			// Reject the stdin sentinel '-' when it appears as a property name.
			// With no '--' and a single positional arg, splitUnsetArgs routes
			// that arg to props (so 'bn unset status' triggers an fzf picker).
			// 'bn unset -' would route '-' to props and try to remove a
			// property literally named '-', which is never what the user
			// meant — error explicitly so they re-run with '-- propname'.
			for _, p := range props {
				if p == "" {
					return errors.New("unset: empty property name")
				}
				if p == "-" {
					return errors.New("unset: '-' is the stdin sentinel, not a property name; use 'bn unset - -- <prop...>' to read paths from stdin")
				}
			}

			var paths []string
			if len(noteArgs) == 0 {
				selected, err := pickNotes(v, cfg, true)
				if err != nil {
					return err
				}
				if len(selected) == 0 {
					return nil
				}
				paths = selected
			} else {
				paths, err = resolveNoteInputs(v.Root, noteArgs, cmd.InOrStdin(), nullSep)
				if err != nil {
					return err
				}
			}
			sort.Strings(paths)

			out := cmd.OutOrStdout()
			var dry *DryRun
			if dryRun {
				dry = &DryRun{Out: out}
			}
			var journaler *noteops.Journal
			if dry == nil {
				journaler = noteops.NewJournal(v.Root, commandInvocation(cmd, args))
			}
			for _, p := range paths {
				changed, err := applyUnset(p, props, cfg, v.Root, dry, journaler)
				if err != nil {
					return err
				}
				if changed && dry == nil {
					if _, err := fmt.Fprintf(out, "%s: updated\n", p); err != nil {
						return err
					}
				}
			}
			if _, err := journaler.Finalize(); err != nil {
				return err
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
// '--' was supplied, the first arg is always treated as a note path (or '-')
// and the remaining args are property names. Property-only mode must use '--':
// 'bn unset -- status'.
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
func applyUnset(path string, props []string, cfg *config.Config, vaultRoot string, dry *DryRun, journaler *noteops.Journal) (bool, error) {
	if dry == nil {
		return noteops.ApplyFrontmatterUnset(vaultRoot, path, props, &cfg.Frontmatter, journaler)
	}

	n := &vault.Note{Path: path}
	if err := n.Load(); err != nil {
		return false, err
	}
	front, err := n.Front()
	if err != nil {
		return false, err
	}
	if dry != nil {
		preview := noteops.NotePreview{RelPath: displayPath(path, vaultRoot)}
		// Deduplicate property names so callers passing the same key twice
		// (e.g. via shell expansion) do not see the same change line repeated.
		// The non-dry path is naturally idempotent because the second Unset
		// finds Has=false; the dry preview must match.
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
			preview.Changes = append(preview.Changes,
				noteops.FrontmatterChange(p, true, v, false, nil))
		}
		if err := noteops.RenderNotePreview(dry.Out, preview); err != nil {
			return false, err
		}
		return preview.Changed(), nil
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
	if err := journaler.Capture(path); err != nil {
		return false, err
	}
	if err := n.Save(&cfg.Frontmatter); err != nil {
		return false, err
	}
	return true, nil
}
