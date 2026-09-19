package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/schema"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// repairOptions is the flag state of one `bn repair` invocation. `ff` is the
// shared filter surface rather than a copy of it, so `repair` keeps routing
// through findflags.go's single execution path.
type repairOptions struct {
	dryRun   bool
	property string
	ff       *findFlags
}

// validate holds the mutual-exclusion rules `repair` enforces. Whether a find
// flag was set is a property of the *cobra.Command rather than of the options,
// so it arrives as a parameter and validate stays a pure function of its inputs.
func (o repairOptions) validate(noteArgs []string, anyFlagSet bool) error {
	if len(noteArgs) > 0 && anyFlagSet {
		return errors.New("repair: cannot combine positional notes with find filter flags")
	}
	return nil
}

// repairRun is the resolved state one repair pass shares across its notes: the
// property set to plan against, the two output streams, and whichever of
// `dry`/`journaler` the mode selected — exactly one of them is ever non-nil.
type repairRun struct {
	cfg       *config.Config
	opts      repairOptions
	root      string
	props     map[string]config.PropertyDefinition
	out       io.Writer
	stderr    io.Writer
	dry       *DryRun
	journaler *noteops.Journal

	repaired           int
	skipped            int
	hasMissingRequired bool
}

func runRepairCommand(cmd *cobra.Command, args []string, opts repairOptions) error {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("repair: no config in context")
	}
	v, err := vault.Open(cfg)
	if err != nil {
		return err
	}

	// Order is load-bearing: an unconfigured schema is a hint rather than an
	// error, and an undefined --property is reported before the flag matrix.
	if len(cfg.Frontmatter.Properties) == 0 {
		writeSchemaHint(cmd.ErrOrStderr(), "repair", cfg.SourcePath)
		return nil
	}
	props, err := repairProperties(cfg, opts.property)
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

	r, err := newRepairRun(cmd, args, opts, cfg, v.Root, props)
	if err != nil {
		return err
	}
	defer r.journaler.Release()
	return r.repairAll(candidates)
}

// newRepairRun resolves the output streams and opens the journal, except on a
// dry-run, which writes a preview instead of taking one.
//
// The journal it opens is joined to the checkpoint barrier and stays joined
// until somebody releases it, so this returns an obligation along with the run.
// Nothing here can fail after BeginOperation succeeds, which is what makes
// handing it back safe rather than merely convenient.
//
//bn:journal-handoff runRepairCommand defers r.journaler.Release() on the returned run
func newRepairRun(
	cmd *cobra.Command,
	args []string,
	opts repairOptions,
	cfg *config.Config,
	root string,
	props map[string]config.PropertyDefinition,
) (*repairRun, error) {
	r := &repairRun{
		cfg:    cfg,
		opts:   opts,
		root:   root,
		props:  props,
		out:    cmd.OutOrStdout(),
		stderr: cmd.ErrOrStderr(),
	}
	if opts.dryRun {
		r.dry = &DryRun{Out: r.out}
		return r, nil
	}
	journaler, err := noteops.BeginOperation(root, "repair", commandInvocation(cmd, args), nil)
	if err != nil {
		return nil, err
	}
	r.journaler = journaler
	return r, nil
}

// repairAll walks the candidates and closes the run. Finalize is nil-safe, so
// the dry-run path reaches it too and commits nothing.
func (r *repairRun) repairAll(candidates []*vault.Note) error {
	for _, n := range candidates {
		if err := r.repairNote(n); err != nil {
			return err
		}
	}
	if _, err := r.journaler.Finalize(); err != nil {
		return err
	}
	r.writeSummary()
	return nil
}

// repairProperties narrows the configured property set to --property, or
// returns all of them when the flag is unset. An undefined property is an error
// rather than an empty plan.
func repairProperties(cfg *config.Config, property string) (map[string]config.PropertyDefinition, error) {
	if property == "" {
		return cfg.Frontmatter.Properties, nil
	}
	def, ok := cfg.Frontmatter.Definition(property)
	if !ok {
		return nil, fmt.Errorf("repair: property %q not defined in [frontmatter.properties]", property)
	}
	return map[string]config.PropertyDefinition{property: def}, nil
}

// repairNote plans and applies the repairs for one note. Skips are reported
// before the action count is re-checked, so a note with nothing fixable still
// explains why.
func (r *repairRun) repairNote(n *vault.Note) error {
	front, err := n.Front()
	if err != nil {
		return err
	}

	result := schema.PlanRepairs(front, r.props)
	if r.opts.property != "" {
		result = filterRepairResult(result, r.opts.property)
	}
	if len(result.Actions) == 0 && len(result.Skips) == 0 {
		return nil
	}

	r.reportSkips(n, result.Skips)

	if len(result.Actions) == 0 {
		return nil
	}
	return r.applyNote(n, result)
}

// applyNote captures the pre-mutation state, applies the planned actions, and
// reports the rewritten file. On a dry-run applyRepairs renders the preview and
// writes nothing, so the capture and the report are both skipped.
func (r *repairRun) applyNote(n *vault.Note, result schema.RepairResult) error {
	if r.dry == nil {
		if err := r.journaler.Capture(n.Path); err != nil {
			return err
		}
	}

	changed, err := applyRepairs(n, result, &r.cfg.Frontmatter, r.root, r.dry)
	if err != nil {
		_, _ = fmt.Fprintln(r.stderr, "WARN: repair interrupted; run 'git diff' to inspect partial state")
		return err
	}
	if !changed {
		return nil
	}
	r.repaired++
	if r.dry == nil {
		fmt.Fprintf(r.out, "repaired: %s\n", displayPath(n.Path, r.root))
	}
	return nil
}

// reportSkips prints the unfixable issues for one note and records whether any
// of them was a missing required property, which decides the closing hint.
func (r *repairRun) reportSkips(n *vault.Note, skips []schema.RepairSkip) {
	for _, s := range skips {
		relPath := displayPath(n.Path, r.root)
		fmt.Fprintf(r.stderr, "SKIP: %s: %s: %s (%s)\n", relPath, s.Property, s.Detail, s.Kind)
		r.skipped++
		if s.Kind == "missing_required" {
			r.hasMissingRequired = true
		}
	}
}

// writeSummary emits the trailing counts. The dry-run has no summary line but
// still emits the missing-required hint.
func (r *repairRun) writeSummary() {
	if r.dry == nil {
		summary := fmt.Sprintf("repaired %d file(s)", r.repaired)
		if r.skipped > 0 {
			summary += fmt.Sprintf(", %d skipped", r.skipped)
		}
		fmt.Fprintln(r.out, summary)
	}
	if r.hasMissingRequired {
		fmt.Fprintln(r.stderr, "\nhint: set missing properties with: bn set <note> <property>=<value>")
	}
}

func filterRepairResult(r schema.RepairResult, prop string) schema.RepairResult {
	var out schema.RepairResult
	for _, a := range r.Actions {
		if a.Property == prop {
			out.Actions = append(out.Actions, a)
		}
	}
	for _, s := range r.Skips {
		if s.Property == prop {
			out.Skips = append(out.Skips, s)
		}
	}
	return out
}
