package cli

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/queryexpr"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// findOptions is the flag state of one `bn find` invocation. `ff` is the shared
// filter surface rather than a copy of it, so `find` keeps routing through
// findflags.go's single execution path.
type findOptions struct {
	interactive       bool
	nullSep           bool
	asJSON            bool
	showBroken        bool
	listBrokenTargets bool
	sortText          string
	relativeTo        string
	// limit caps the output; 0 is no cap and defers to a view's stored limit.
	limit int
	ff    *findFlags
}

// validate holds every mutual-exclusion rule `find` enforces. Unlike search's,
// every rule is a property of the option fields alone — `--broken-links` is read
// off the shared findFlags rather than off the *cobra.Command — so it takes no
// parameters.
func (o findOptions) validate() error {
	if err := o.validateBrokenLinkRequirements(); err != nil {
		return err
	}
	if err := o.validateJSONOutput(); err != nil {
		return err
	}
	return o.validateBrokenOutputCombos()
}

// validateBrokenLinkRequirements rejects the two broken-link reports asked for
// without the filter that produces them, and the two of them asked for at once.
func (o findOptions) validateBrokenLinkRequirements() error {
	if o.showBroken && !o.ff.brokenLinks {
		return errors.New("find: --show-broken requires --broken-links")
	}
	if o.listBrokenTargets && !o.ff.brokenLinks {
		return errors.New("find: --list-broken-targets requires --broken-links")
	}
	if o.showBroken && o.listBrokenTargets {
		return errors.New("find: --show-broken cannot be combined with --list-broken-targets")
	}
	return nil
}

// validateBrokenOutputCombos rejects the two path-shaped modes a broken-link
// report cannot share output with: neither report emits paths, so neither can be
// piped into fzf or NUL-separated.
func (o findOptions) validateBrokenOutputCombos() error {
	if !o.showBroken && !o.listBrokenTargets {
		return nil
	}
	switch {
	case o.interactive:
		return errors.New("find: --interactive cannot be combined with --show-broken or --list-broken-targets")
	case o.nullSep:
		return errors.New("find: --null cannot be combined with --show-broken or --list-broken-targets")
	case o.asJSON:
		return errors.New("find: --json cannot be combined with --show-broken or --list-broken-targets")
	}
	return nil
}

// validateJSONOutput rejects the one path-list option JSON replaces.
func (o findOptions) validateJSONOutput() error {
	if o.asJSON && o.nullSep {
		return errors.New("find: --json cannot be combined with --null")
	}
	return nil
}

// findRun is the resolved state the three output modes share: the ordered and
// capped matches plus everything needed to render them.
type findRun struct {
	cmd     *cobra.Command
	cfg     *config.Config
	opts    findOptions
	root    string
	matched []*vault.Note
	graph   *query.Graph
}

func runFindCommand(cmd *cobra.Command, opts findOptions) error {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("find: no config in context")
	}
	// Order is load-bearing: the flag matrix is refused first, then a --sort that
	// disagrees with --relative-to, and both before the vault is opened.
	if err := validateLimitFlag(cmd, "find", opts.limit); err != nil {
		return err
	}
	if err := opts.validate(); err != nil {
		return err
	}
	sortSpec, err := validateFindSort(opts.sortText, opts.relativeTo)
	if err != nil {
		return err
	}

	v, err := vault.Open(cfg)
	if err != nil {
		return err
	}
	notes, err := loadReadableNotes(cmd, v)
	if err != nil {
		return err
	}

	sel, err := opts.ff.selectNotes(cmd.Context(), cfg, v.Root, notes, nil)
	if err != nil {
		return err
	}
	matched, err := orderFindResults(findSortInput{
		vault:   v,
		matched: sel.notes,
		notes:   notes,
		graph:   sel.graph,
	}, opts, sel.plan, sortSpec)
	if err != nil {
		return err
	}

	return runFindMode(&findRun{
		cmd:     cmd,
		cfg:     cfg,
		opts:    opts,
		root:    v.Root,
		matched: matched,
		graph:   sel.graph,
	})
}

// orderFindResults applies the ordering and then the cap.
//
// `bn find` is the only flag-scoped command that produces an ordering, so it is
// the only one that executes the ordering and cap a saved view stores; every
// other command refuses such a view outright. Explicit flags win: --sort or
// --relative-to on the command line replaces the view's ordering wholesale
// rather than merging with it, so the two halves cannot disagree.
//
// The cap is applied last, over the ordered result, so a view that saves "the 10
// most recently modified" returns those ten rather than ten arbitrary matches
// re-sorted. An explicit --limit replaces the view's cap, the way --sort
// replaces its ordering.
func orderFindResults(in findSortInput, opts findOptions, plan *queryexpr.Plan, spec queryexpr.SortSpec) ([]*vault.Note, error) {
	matched := in.matched
	ordered := strings.TrimSpace(opts.sortText) != "" || strings.TrimSpace(opts.relativeTo) != ""
	if !ordered && plan.Sort.Key != "" {
		spec, ordered = plan.Sort, true
	}
	switch {
	case ordered:
		// An explicit ordering wins over the implicit pinned ordering below,
		// including when it asks for plain path order.
		sorted, err := sortFindResults(in, spec)
		if err != nil {
			return nil, err
		}
		matched = sorted
	case opts.ff.pinned:
		matched = pinnedOrder(in.vault.Root, matched)
	}
	return capResults(matched, findLimit(opts.limit, plan.Limit)), nil
}

// runFindMode dispatches to the one output mode the flags select. validate() has
// already refused every combination that is not exactly one of these three, and
// the two broken-link reports are mutually exclusive, so the order of the first
// two cases is not load-bearing.
func runFindMode(r *findRun) error {
	switch {
	case r.opts.showBroken:
		return printShowBroken(r.cmd.OutOrStdout(), r.matched, r.graph, r.root)
	case r.opts.listBrokenTargets:
		return printListBrokenTargets(r.cmd.OutOrStdout(), r.matched, r.graph)
	default:
		return runFindPaths(r)
	}
}

// runFindPaths is the default mode: one absolute path per matching note, in the
// order orderFindResults produced, optionally narrowed by fzf first.
func runFindPaths(r *findRun) error {
	paths := make([]string, len(r.matched))
	for i, n := range r.matched {
		paths[i] = n.Path
	}

	if r.opts.interactive {
		picked, err := RunFZF(paths, r.cfg.Fzf.Args)
		selected, err := requireSelection("find", picked, err)
		if err != nil {
			return err
		}
		paths = selected
	}
	if r.opts.asJSON {
		// The shared {path, abs_path, title} record: `path` is vault-relative,
		// as in `related --json` and `lint --json`, and `abs_path` is the path
		// the plain listing prints.
		return writeNotesJSON(r.cmd.OutOrStdout(), r.root, paths)
	}
	return writePathList(r.cmd.OutOrStdout(), paths, r.opts.nullSep)
}

// findLimit is the cap find applies: --limit when given, else the view's.
func findLimit(flag, view int) int {
	if flag > 0 {
		return flag
	}
	return view
}

// capResults keeps the first limit items; a limit of 0 keeps them all.
func capResults[T any](items []T, limit int) []T {
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}
