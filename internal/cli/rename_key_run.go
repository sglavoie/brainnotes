package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/frontmatter"
	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// renameKeyOptions is the flag state of one `bn rename-key` invocation. `ff` is
// the shared filter surface rather than a copy of it, so `rename-key` keeps
// routing through findflags.go's single execution path.
type renameKeyOptions struct {
	dryRun bool
	ff     *findFlags
}

// validate holds the mutual-exclusion rules `rename-key` enforces. Whether a
// find flag was set is a property of the *cobra.Command rather than of the
// options, so it arrives as a parameter.
func (o renameKeyOptions) validate(noteArgs []string, anyFlagSet bool) error {
	if len(noteArgs) > 0 && anyFlagSet {
		return errors.New("rename-key: cannot combine positional notes with find filter flags")
	}
	return nil
}

// renameKeyRun is the resolved state one rename-key pass shares across its
// notes. Exactly one of `dry`/`journaler` is ever non-nil.
type renameKeyRun struct {
	cfg       *config.Config
	root      string
	oldKey    string
	newKey    string
	out       io.Writer
	stderr    io.Writer
	dry       *DryRun
	journaler *noteops.Journal

	renamed int
	skipped int
}

func runRenameKeyCommand(cmd *cobra.Command, args []string, opts renameKeyOptions) error {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("rename-key: no config in context")
	}
	v, err := vault.Open(cfg)
	if err != nil {
		return err
	}

	// Order is load-bearing: both keys are validated before the flag matrix, so
	// a malformed key is a key error rather than a scoping error.
	oldKey, newKey, noteArgs := args[0], args[1], args[2:]
	if err := validateRenameKeys(oldKey, newKey); err != nil {
		return err
	}
	if err := opts.validate(noteArgs, anyQueryFlagSet(cmd)); err != nil {
		return err
	}

	candidates, err := collectReplaceCandidates(cmd, v, cfg, noteArgs, opts.ff)
	if err != nil {
		return err
	}

	r := &renameKeyRun{
		cfg:    cfg,
		root:   v.Root,
		oldKey: oldKey,
		newKey: newKey,
		out:    cmd.OutOrStdout(),
		stderr: cmd.ErrOrStderr(),
	}
	if opts.dryRun {
		r.dry = &DryRun{Out: r.out}
	} else {
		r.journaler, err = noteops.BeginOperationContext(commandContext(cmd), v.Root, "rename-key", commandInvocation(cmd, args), nil)
		if err != nil {
			return err
		}
	}
	defer r.journaler.Release()

	for _, n := range candidates {
		if err := r.renameNote(n); err != nil {
			return err
		}
	}
	return r.finish()
}

// validateRenameKeys rejects the two malformed-key cases and the no-op rename.
func validateRenameKeys(oldKey, newKey string) error {
	if !pairKeyRe.MatchString(oldKey) {
		return fmt.Errorf("rename-key: invalid key %q (allowed: letters, digits, '_', '-')", oldKey)
	}
	if !pairKeyRe.MatchString(newKey) {
		return fmt.Errorf("rename-key: invalid key %q (allowed: letters, digits, '_', '-')", newKey)
	}
	if oldKey == newKey {
		return errors.New("rename-key: old and new key are the same")
	}
	return nil
}

// renameNote renames the key in one note. A note without the old key is skipped
// silently; a note that already carries the new key is a counted warning rather
// than an error, so one conflict does not abort the run.
func (r *renameKeyRun) renameNote(n *vault.Note) error {
	front, err := n.Front()
	if err != nil {
		return err
	}
	if !front.Has(r.oldKey) {
		return nil
	}

	oldVal, _ := front.Get(r.oldKey)
	changed, err := front.RenameKey(r.oldKey, r.newKey)
	if err != nil {
		if errors.Is(err, frontmatter.ErrKeyConflict) {
			fmt.Fprintf(r.stderr, "rename-key: warning: %s: skipped, key %q already exists\n",
				displayPath(n.Path, r.root), r.newKey)
			r.skipped++
			return nil
		}
		return err
	}
	if !changed {
		return nil
	}

	if err := r.commitNote(n, oldVal); err != nil {
		return err
	}
	r.renamed++
	return nil
}

// commitNote renders the preview or journals and writes the note, whichever the
// mode selected.
func (r *renameKeyRun) commitNote(n *vault.Note, oldVal any) error {
	if r.dry != nil {
		return r.previewNote(n, oldVal)
	}
	if err := r.journaler.Capture(n.Path); err != nil {
		return err
	}
	return n.Save(&r.cfg.Frontmatter)
}

// previewNote renders the rename as the canonical drop-then-add frontmatter
// pair through noteops.RenderNotePreview, which is what keeps this --dry-run
// byte-identical in form to every other one and to the web preview APIs.
func (r *renameKeyRun) previewNote(n *vault.Note, oldVal any) error {
	preview := noteops.NotePreview{RelPath: displayPath(n.Path, r.root)}
	preview.Changes = append(preview.Changes,
		noteops.FrontmatterChange(r.oldKey, true, oldVal, false, nil),
		noteops.FrontmatterChange(r.newKey, false, nil, true, oldVal))
	return noteops.RenderNotePreview(r.dry.Out, preview)
}

// finish finalizes the journal and writes the summary. A dry run has no
// journal and writes its would-be summary to stderr, keeping stdout the preview.
func (r *renameKeyRun) finish() error {
	if r.dry != nil {
		fmt.Fprintln(r.stderr, "would "+r.summary("rename"))
		return nil
	}
	if _, err := r.journaler.Finalize(); err != nil {
		return err
	}

	fmt.Fprintln(r.out, r.summary("renamed"))

	// Config-update warnings are only useful when files were actually
	// rewritten; suppress during --dry-run and no-op runs to avoid confusing
	// users.
	if r.renamed > 0 {
		if _, defined := r.cfg.Frontmatter.Definition(r.oldKey); defined {
			fmt.Fprintf(r.stderr, "rename-key: warning: key %q is defined in [frontmatter.properties]; update your brainnotes.toml to use %q\n", r.oldKey, r.newKey)
		}
	}
	return nil
}

// summary is the trailing count line, after verb ("renamed", or "rename" for a
// dry run's "would rename ...").
func (r *renameKeyRun) summary(verb string) string {
	line := fmt.Sprintf("%s %s -> %s in %d file(s)", verb, r.oldKey, r.newKey, r.renamed)
	if r.skipped > 0 {
		line += fmt.Sprintf(", %d skipped (conflict)", r.skipped)
	}
	return line
}
