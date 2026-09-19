package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

type relinkOptions struct {
	// ff is held as the pointer the command registers, so relink scopes
	// through findflags.go's single execution path.
	ff     *findFlags
	dryRun bool
}

// validate holds relink's whole flag matrix: positional notes and the
// find-style scoping flags are two ways of naming the same scope, so combining
// them is refused. anyFlagSet comes from the *cobra.Command rather than from
// the options, so it is passed in.
func (o relinkOptions) validate(noteArgs []string, anyFlagSet bool) error {
	if len(noteArgs) > 0 && anyFlagSet {
		return errors.New("relink: cannot combine positional notes with find filter flags")
	}
	return nil
}

func runRelinkCommand(cmd *cobra.Command, args []string, opts relinkOptions) error {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("relink: no config in context")
	}
	v, err := vault.Open(cfg)
	if err != nil {
		return err
	}

	if err := opts.validate(args, anyQueryFlagSet(cmd)); err != nil {
		return err
	}

	candidates, err := collectReplaceCandidates(cmd, v, cfg, args, opts.ff)
	if err != nil {
		return err
	}
	// The link graph is built over the whole vault even when the scope is
	// narrower: a candidate's link is only unresolved relative to every note
	// that exists, not to the selected subset.
	allNotes, err := v.Notes()
	if err != nil {
		return err
	}

	r, err := newRelinkRun(cmd, args, v, cfg, allNotes, opts)
	if err != nil {
		return err
	}
	defer r.journaler.Release()
	return r.relinkAll(candidates)
}

// relinkRun carries the state the per-note rewrite, the residue report, and the
// summary share: the two output streams, the resolved graph and index, and
// whichever of dry/journaler the mode selected.
type relinkRun struct {
	out       io.Writer
	errOut    io.Writer
	root      string
	fm        *config.FrontmatterConfig
	graph     *query.Graph
	idx       *relinkIndex
	dry       *DryRun
	journaler *noteops.Journal
	relinked  int
	residues  []relinkResidue
}

// newRelinkRun resolves the output streams and opens the journal, except on a
// dry-run, which writes a preview instead of taking one.
//
// The journal it opens is joined to the checkpoint barrier and stays joined
// until somebody releases it, so this returns an obligation along with the run.
// Nothing here can fail after BeginOperation succeeds, which is what makes
// handing it back safe rather than merely convenient.
//
//bn:journal-handoff runRelinkCommand defers r.journaler.Release() on the returned run
func newRelinkRun(cmd *cobra.Command, args []string, v *vault.Vault, cfg *config.Config, allNotes []*vault.Note, opts relinkOptions) (*relinkRun, error) {
	g := query.BuildGraphForRoot(v.Root, allNotes)
	out := cmd.OutOrStdout()
	r := &relinkRun{
		out:    out,
		errOut: cmd.ErrOrStderr(),
		root:   v.Root,
		fm:     &cfg.Frontmatter,
		graph:  g,
		idx:    newRelinkIndex(v.Root, g),
	}
	if opts.dryRun {
		r.dry = &DryRun{Out: out}
		return r, nil
	}
	journaler, err := noteops.BeginOperation(v.Root, "relink", commandInvocation(cmd, args), nil)
	if err != nil {
		return nil, err
	}
	r.journaler = journaler
	return r, nil
}

func (r *relinkRun) relinkAll(candidates []*vault.Note) error {
	for _, n := range candidates {
		if err := r.relinkNote(n); err != nil {
			return err
		}
	}
	if _, err := r.journaler.Finalize(); err != nil {
		return err
	}
	return r.finish()
}

func (r *relinkRun) relinkNote(n *vault.Note) error {
	changed, noteResidues, err := applyRelink(n, r.idx, r.graph, r.fm, r.root, r.dry, r.journaler)
	if err != nil {
		_, _ = fmt.Fprintln(r.errOut, "WARN: relink interrupted; run 'git diff' to inspect partial state")
		return err
	}
	// Residues accumulate even for an unchanged note: a link with no
	// deterministic target is exactly the case that reports and does not
	// rewrite.
	r.residues = append(r.residues, noteResidues...)
	if !changed {
		return nil
	}
	r.relinked++
	if r.dry == nil {
		fmt.Fprintf(r.out, "relinked: %s\n", displayPath(n.Path, r.root))
	}
	return nil
}

// finish reports the skipped links on stderr — in both modes, since a dry run
// still names what it could not resolve — and the applied count on stdout,
// which a dry run omits.
func (r *relinkRun) finish() error {
	if err := printRelinkResidues(r.errOut, r.residues); err != nil {
		return err
	}
	if len(r.residues) > 0 {
		fmt.Fprintln(r.errOut, "\nhint: run 'bn find --broken-links --show-broken' for manual follow-up")
	}
	if r.dry != nil {
		return nil
	}
	summary := fmt.Sprintf("relinked %d file(s)", r.relinked)
	if len(r.residues) > 0 {
		summary += fmt.Sprintf(", %d unresolved skipped", len(r.residues))
	}
	fmt.Fprintln(r.out, summary)
	return nil
}
