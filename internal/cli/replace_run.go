package cli

import (
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

type replaceOptions struct {
	// ff is held as the pointer the command registers, so replace keeps
	// routing through findflags.go's single execution path.
	ff     *findFlags
	dryRun bool
}

// validate holds replace's whole flag matrix: positional notes and the
// find-style scoping flags are two ways of naming the same thing, so combining
// them is refused. anyFlagSet comes from the *cobra.Command rather than from
// the options, so it is passed in.
func (o replaceOptions) validate(noteArgs []string, anyFlagSet bool) error {
	if len(noteArgs) > 0 && anyFlagSet {
		return errors.New("replace: cannot combine positional notes with find filter flags")
	}
	return nil
}

// replaceTarget is the coerced <key> <from> <to> triple the run applies.
type replaceTarget struct {
	key  string
	from any
	to   any
}

func runReplaceCommand(cmd *cobra.Command, args []string, opts replaceOptions) error {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("replace: no config in context")
	}
	v, err := vault.Open(cfg)
	if err != nil {
		return err
	}

	key, fromRaw, toRaw := args[0], args[1], args[2]
	noteArgs := args[3:]

	// Order is behaviour: an invalid key is reported before the flag matrix,
	// and both before <from>/<to> are coerced.
	if !pairKeyRe.MatchString(key) {
		return fmt.Errorf("replace: invalid key %q (allowed: letters, digits, '_', '-')", key)
	}
	if err := opts.validate(noteArgs, anyQueryFlagSet(cmd)); err != nil {
		return err
	}
	target, err := coerceReplaceTarget(key, fromRaw, toRaw, cfg)
	if err != nil {
		return err
	}

	candidates, err := collectReplaceCandidates(cmd, v, cfg, noteArgs, opts.ff)
	if err != nil {
		return err
	}

	r, err := newReplaceRun(cmd, args, v, cfg, target, opts)
	if err != nil {
		return err
	}
	return r.replaceAll(candidates)
}

// coerceReplaceTarget coerces <from> and <to> against the declared kind of
// <key>, which is what makes `bn replace pinned true false` compare bools and
// `bn replace parent Old New` compare `[[Old]]` to `[[New]]`.
func coerceReplaceTarget(key, fromRaw, toRaw string, cfg *config.Config) (replaceTarget, error) {
	isWikilink := cfg.Frontmatter.IsWikilink(key)
	fromVal, err := coerceReplaceValue(fromRaw, isWikilink)
	if err != nil {
		return replaceTarget{}, fmt.Errorf("replace: invalid <from>: %w", err)
	}
	toVal, err := coerceReplaceValue(toRaw, isWikilink)
	if err != nil {
		return replaceTarget{}, fmt.Errorf("replace: invalid <to>: %w", err)
	}
	return replaceTarget{key: key, from: fromVal, to: toVal}, nil
}

// coerceReplaceValue runs raw through coerceValue (auto-detect) and then wraps
// the result in [[...]] when the target key is wikilink-typed. Auto-coercion
// matches 'bn set' so user input like "true" or "2026-01-02" round-trips.
//
// For wikilink-typed properties, auto-coercion is skipped: stored values are
// always strings of the form `[[X]]`, so coercing user input like `true` to a
// bool would prevent any match. The raw string is wrapped in `[[ ]]` instead.
func coerceReplaceValue(raw string, isWikilink bool) (any, error) {
	if isWikilink {
		return wrapWikilinkValue(raw), nil
	}
	v, err := coerceValue(raw, "")
	if err != nil {
		return nil, err
	}
	return v, nil
}

// collectReplaceCandidates returns the notes the replace should iterate over.
// When noteArgs is non-empty the explicit paths are loaded individually;
// otherwise the full vault is walked and filtered through findFlags.selectNotes
// — the same compiled plan `bn find` runs, so a scoped mutation touches exactly
// the notes the equivalent `bn find` prints.
func collectReplaceCandidates(cmd *cobra.Command, v *vault.Vault, cfg *config.Config, noteArgs []string, ff *findFlags) ([]*vault.Note, error) {
	if len(noteArgs) > 0 {
		paths, err := resolveNoteInputs(v.Root, noteArgs, cmd.InOrStdin(), false)
		if err != nil {
			return nil, err
		}
		sort.Strings(paths)
		out := make([]*vault.Note, 0, len(paths))
		for _, p := range paths {
			n := vault.NewNote(v.Root, p)
			if err := n.Load(); err != nil {
				return nil, err
			}
			out = append(out, n)
		}
		return out, nil
	}

	notes, err := v.Notes()
	if err != nil {
		return nil, err
	}
	sel, err := ff.selectNotes(cmd.Context(), cfg, v.Root, notes, nil)
	if err != nil {
		return nil, err
	}
	return sel.notes, nil
}

// replaceRun carries the state the per-note apply and the summary share: the
// two output streams, the resolved target, and whichever of dry/journaler the
// mode selected.
type replaceRun struct {
	out       io.Writer
	errOut    io.Writer
	root      string
	fm        *config.FrontmatterConfig
	target    replaceTarget
	dry       *DryRun
	journaler *noteops.Journal
	count     int
}

func newReplaceRun(cmd *cobra.Command, args []string, v *vault.Vault, cfg *config.Config, target replaceTarget, opts replaceOptions) (*replaceRun, error) {
	out := cmd.OutOrStdout()
	r := &replaceRun{
		out:    out,
		errOut: cmd.ErrOrStderr(),
		root:   v.Root,
		fm:     &cfg.Frontmatter,
		target: target,
	}
	if opts.dryRun {
		r.dry = &DryRun{Out: out}
		return r, nil
	}
	journaler, err := noteops.BeginOperation(v.Root, "replace", commandInvocation(cmd, args), nil)
	if err != nil {
		return nil, err
	}
	r.journaler = journaler
	return r, nil
}

func (r *replaceRun) replaceAll(candidates []*vault.Note) error {
	for _, n := range candidates {
		if err := r.replaceNote(n); err != nil {
			return err
		}
	}
	if _, err := r.journaler.Finalize(); err != nil {
		return err
	}
	return r.finish()
}

func (r *replaceRun) replaceNote(n *vault.Note) error {
	changed, err := applyReplace(n, r.target.key, r.target.from, r.target.to, r.fm, r.root, r.dry, r.journaler)
	if err != nil {
		_, _ = fmt.Fprintln(r.errOut, "WARN: replace interrupted; run 'git diff' to inspect partial state")
		return err
	}
	if !changed {
		return nil
	}
	r.count++
	if r.dry != nil {
		return nil
	}
	_, err = fmt.Fprintf(r.out, "updated: %s\n", displayPath(n.Path, r.root))
	return err
}

// finish prints the trailing count, which a dry run deliberately omits.
func (r *replaceRun) finish() error {
	if r.dry != nil {
		return nil
	}
	_, err := fmt.Fprintf(r.out, "replaced %d file(s)\n", r.count)
	return err
}

// applyReplace performs the swap on a single note. On dry-run it emits a diff
// block and skips the write; otherwise the file is rewritten atomically.
func applyReplace(n *vault.Note, key string, fromVal, toVal any, fm *config.FrontmatterConfig, vaultRoot string, dry *DryRun, journaler *noteops.Journal) (bool, error) {
	front, err := n.Front()
	if err != nil {
		return false, err
	}
	beforeVal, beforePresent := front.Get(key)
	changed, err := front.ReplaceValue(key, fromVal, toVal)
	if err != nil {
		if errors.Is(err, frontmatter.ErrNotAList) {
			return false, fmt.Errorf("replace: %q: %w", key, err)
		}
		return false, err
	}
	if !changed {
		return false, nil
	}
	if dry != nil {
		afterVal, afterOK := front.Get(key)
		preview := noteops.NotePreview{RelPath: displayPath(n.Path, vaultRoot)}
		preview.Changes = append(preview.Changes,
			noteops.FrontmatterChange(key, beforePresent, beforeVal, afterOK, afterVal))
		if err := noteops.RenderNotePreview(dry.Out, preview); err != nil {
			return false, err
		}
		return preview.Changed(), nil
	}
	if err := journaler.Capture(n.Path); err != nil {
		return false, err
	}
	if err := n.Save(fm); err != nil {
		return false, err
	}
	return true, nil
}
