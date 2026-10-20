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

type unsetOptions struct {
	nullSep bool
	dryRun  bool
}

// validate holds unset's whole flag matrix: --null is only meaningful when the
// notes arrive on stdin, and --dry-run composes with everything. noteArgs is a
// parameter because it is a property of the positional arguments after the '--'
// split rather than of the flags.
func (o unsetOptions) validate(noteArgs []string) error {
	if o.nullSep && !usesStdinNoteInput(noteArgs) {
		return errors.New("unset: --null requires '-' stdin note input")
	}
	return nil
}

func runUnsetCommand(cmd *cobra.Command, args []string, opts unsetOptions) error {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("unset: no config in context")
	}
	v, err := vault.Open(cfg)
	if err != nil {
		return err
	}

	noteArgs, props := splitUnsetArgs(args, cmd.ArgsLenAtDash())
	if err := opts.validate(noteArgs); err != nil {
		return err
	}
	if err := validateUnsetProps(props); err != nil {
		return err
	}

	paths, ok, err := resolveUnsetPaths(cmd, v, cfg, noteArgs, opts.nullSep)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	sort.Strings(paths)

	return unsetAll(cmd, args, v, cfg, props, paths, opts)
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

// validateUnsetProps refuses the property list itself, which is a property of
// the positional arguments rather than of the flag matrix.
//
// The stdin sentinel '-' is rejected explicitly: with no '--' and a single
// positional arg, splitUnsetArgs routes that arg to props (so 'bn unset status'
// triggers an fzf picker). 'bn unset -' would route '-' to props and try to
// remove a property literally named '-', which is never what the user meant —
// error so they re-run with '-- propname'.
func validateUnsetProps(props []string) error {
	if len(props) == 0 {
		return errors.New("unset: at least one property name is required")
	}
	for _, p := range props {
		if p == "" {
			return errors.New("unset: empty property name")
		}
		if p == "-" {
			return errors.New("unset: '-' is the stdin sentinel, not a property name; use 'bn unset - -- <prop...>' to read paths from stdin")
		}
	}
	return nil
}

// resolveUnsetPaths returns the notes to remove properties from. The bool
// reports whether the run should continue: an fzf picker that selected nothing
// is a silent no-op and must not open a journal for zero paths.
func resolveUnsetPaths(cmd *cobra.Command, v *vault.Vault, cfg *config.Config, noteArgs []string, nullSep bool) ([]string, bool, error) {
	if len(noteArgs) == 0 {
		selected, err := pickNotes("unset", v, cfg, true)
		if err != nil {
			return nil, false, err
		}
		return selected, len(selected) > 0, nil
	}
	paths, err := resolveNoteInputs("unset", v.Root, noteArgs, cmd.InOrStdin(), nullSep)
	if err != nil {
		return nil, false, err
	}
	return paths, true, nil
}

// unsetAll opens the journal (apply mode) or the dry-run sink (preview mode)
// and walks the resolved paths. The journal is opened only once the paths are
// known, so a refusal above never leaves a journal behind.
func unsetAll(cmd *cobra.Command, args []string, v *vault.Vault, cfg *config.Config, props, paths []string, opts unsetOptions) error {
	out := cmd.OutOrStdout()
	var dry *DryRun
	if opts.dryRun {
		dry = &DryRun{Out: out}
	}
	var journaler *noteops.Journal
	if dry == nil {
		var err error
		journaler, err = noteops.BeginOperationContext(commandContext(cmd), v.Root, "unset", commandInvocation(cmd, args), paths)
		if err != nil {
			return err
		}
	}
	defer journaler.Release()

	for _, p := range paths {
		changed, err := applyUnset(p, props, cfg, v.Root, dry, journaler)
		if err != nil {
			return err
		}
		if changed && dry == nil {
			if _, err := fmt.Fprintf(out, "updated: %s\n", displayPath(p, v.Root)); err != nil {
				return err
			}
		}
	}
	_, err := journaler.Finalize()
	return err
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
