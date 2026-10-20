package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/queryexpr"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

type findFlags struct {
	tags         []string
	categories   []string
	status       string
	typeProp     string
	hasProps     []string
	missingProps []string
	age          string
	created      string
	folder       string
	orphan       bool
	pinned       bool
	brokenLinks  bool
	minBacklinks int
	maxBacklinks int
	minLinks     int
	maxLinks     int
	deadEnd      bool
	query        string
	view         string

	// command is the command name these flags are registered on, used only to
	// name the offender when a saved view stores an ordering this command
	// cannot run.
	command string
	// ordersResults records that this command produces an ordering and a cap of
	// its own, and can therefore execute the ones a saved view stores. It is
	// true for `bn find` alone; see rejectUnrunnableView.
	ordersResults bool
}

func (ff *findFlags) register(cmd *cobra.Command) {
	ff.command = cmd.Name()
	f := cmd.Flags()
	f.StringSliceVar(&ff.tags, "tag", nil, "filter by tag (repeatable; AND semantics)")
	f.StringSliceVar(&ff.categories, "category", nil, "filter by category target (repeatable; AND semantics)")
	// The single-value flags refuse a repeat rather than keeping the last
	// value, which would read as an AND that silently dropped a term.
	singleStringVarP(f, &ff.status, "status", "", "filter by status property")
	singleStringVarP(f, &ff.typeProp, "type", "", "filter by type/kind property")
	f.StringSliceVar(&ff.hasProps, "has", nil, "require property present (repeatable; AND semantics)")
	f.StringSliceVar(&ff.missingProps, "missing", nil, "require property absent (repeatable; AND semantics)")
	singleStringVarP(f, &ff.age, "age", "", "filter by 'last' property age, e.g. >30d, <7d, =0d")
	singleStringVarP(f, &ff.created, "created", "", "filter by 'created' property age, e.g. >30d, <7d, =0d")
	singleStringVarP(f, &ff.folder, "folder", "", "limit to a vault-relative folder (matches descendants)")
	f.BoolVar(&ff.orphan, "orphan", false, "only notes nothing in the vault links to")
	f.BoolVar(&ff.pinned, "pinned", false, "only notes pinned via the 'pinned' property")
	f.BoolVar(&ff.brokenLinks, "broken-links", false, "only notes with unresolved wikilinks")
	f.IntVar(&ff.minBacklinks, "min-backlinks", -1, "minimum inbound links from other notes")
	f.IntVar(&ff.maxBacklinks, "max-backlinks", -1, "maximum inbound links from other notes")
	f.IntVar(&ff.minLinks, "min-links", -1, "minimum outgoing links to other notes")
	f.IntVar(&ff.maxLinks, "max-links", -1, "maximum outgoing links to other notes")
	f.BoolVar(&ff.deadEnd, "dead-end", false, "notes with zero outgoing links (except dead_end: false)")
	singleStringVarP(f, &ff.query, "query", "q", "filter with a query expression, e.g. 'tag:foo and not folder:Archive'")
	singleStringVarP(f, &ff.view, "view", "", "filter with a saved view by name")
}

// needsGraph reports whether the link graph has to be built before the compiled
// plan can be evaluated.
//
// It consults both halves of the filter surface. plan.NeedsGraph covers every
// graph-reading term, including the ones the flags themselves lower to, and is
// the only thing that can see a graph field typed into --query or saved in a
// view. The flag struct is still read directly so that a flag whose lowering
// ever changes cannot silently stop building the graph it needs.
//
// A nil plan is legal: Plan.NeedsGraph reports false for one.
func (ff *findFlags) needsGraph(plan *queryexpr.Plan) bool {
	if plan.NeedsGraph() {
		return true
	}
	return ff.orphan || ff.brokenLinks || ff.deadEnd ||
		ff.minBacklinks >= 0 || ff.maxBacklinks >= 0 ||
		ff.minLinks >= 0 || ff.maxLinks >= 0
}

func (ff *findFlags) toQueryFlags(root string, g *query.Graph) query.Flags {
	return query.Flags{
		Tags:         ff.tags,
		Categories:   ff.categories,
		Status:       ff.status,
		Type:         ff.typeProp,
		HasAll:       ff.hasProps,
		MissingAll:   ff.missingProps,
		Age:          ff.age,
		Created:      ff.created,
		Folder:       ff.folder,
		Orphan:       ff.orphan,
		Pinned:       ff.pinned,
		BrokenLinks:  ff.brokenLinks,
		MinBacklinks: ff.minBacklinks,
		MaxBacklinks: ff.maxBacklinks,
		MinLinks:     ff.minLinks,
		MaxLinks:     ff.maxLinks,
		DeadEnd:      ff.deadEnd,
		Root:         root,
		Graph:        g,
	}
}

// compile folds the three filter sources this flag set exposes — the find
// flags, --query, and --view — into one plan whose Expr is the AND of all
// three. Every flag-scoped command goes through it, so there is exactly one
// place where "what did the user ask for?" is answered.
//
// The two text sources are compiled separately rather than concatenated: a
// *queryexpr.ParseError carries the source it was parsed from, so compiling
// each on its own is what keeps its carets under the text the user actually
// typed. Only the validated expressions are merged afterwards.
//
// The flag half is lowered by queryexpr.FromFlags and is not run through
// Compile, because it is a hand-built AST with no source text to point at; its
// terms are validated by Plan.Predicate along with everything else.
func (ff *findFlags) compile(cfg *config.Config, env queryexpr.Env) (*queryexpr.Plan, error) {
	flagsExpr, err := queryexpr.FromFlags(ff.toQueryFlags(env.Root, env.Graph))
	if err != nil {
		return nil, err
	}

	resolved, err := ff.resolveViewFlag(cfg, env)
	if err != nil {
		return nil, err
	}
	viewQuery := ""
	if resolved != nil {
		viewQuery = resolved.Plan.Expr.String()
	}

	operands, source, err := compileQuerySources([]string{ff.query, viewQuery}, env, flagsExpr)
	if err != nil {
		return nil, err
	}

	plan := &queryexpr.Plan{Expr: &queryexpr.And{Operands: operands}, Source: source}
	applyViewOrdering(plan, resolved)
	return plan, nil
}

// resolveViewFlag resolves --view, or returns a nil view when the flag was not
// set. A command that cannot execute a stored ordering refuses the view here,
// before any query text is compiled, so the refusal is what the user sees rather
// than a parse error from the view's own query.
func (ff *findFlags) resolveViewFlag(cfg *config.Config, env queryexpr.Env) (*queryexpr.View, error) {
	name := strings.TrimSpace(ff.view)
	if name == "" {
		return nil, nil
	}
	view, err := resolveView(cfg, name, env)
	if err != nil {
		return nil, err
	}
	if ff.ordersResults {
		return &view, nil
	}
	if err := rejectUnrunnableView(ff.command, view); err != nil {
		return nil, err
	}
	return &view, nil
}

// compileQuerySources compiles each non-empty query source on its own and
// returns the AND operands (flagsExpr first) plus the plan's Source.
//
// Source is what a later ParseError — one raised by Plan.Predicate rather than
// by Compile — renders against, so it holds the first source given, which is why
// the caller passes the user's --query ahead of a view's stored text.
func compileQuerySources(sources []string, env queryexpr.Env, flagsExpr queryexpr.Expr) ([]queryexpr.Expr, string, error) {
	operands := []queryexpr.Expr{flagsExpr}
	source := ""
	for _, src := range sources {
		if strings.TrimSpace(src) == "" {
			continue
		}
		part, err := queryexpr.Compile(src, env)
		if err != nil {
			return nil, "", err
		}
		operands = append(operands, part.Expr)
		if source == "" {
			source = src
		}
	}
	return operands, source, nil
}

// applyViewOrdering copies a view's stored ordering and cap onto the plan, so
// the command that produces the ordering can read them — and only that command
// applies them.
//
// Sort is copied only when the view actually stores one: ResolveView turns an
// empty sort into the default `path` spec, and copying that would make every
// view look like an explicit ordering request and suppress the implicit --pinned
// ordering in `bn find`.
func applyViewOrdering(plan *queryexpr.Plan, view *queryexpr.View) {
	if view == nil || view.Plan == nil {
		return
	}
	if strings.TrimSpace(view.Saved.Sort) != "" {
		plan.Sort = view.Plan.Sort
	}
	plan.Limit = view.Plan.Limit
}

// resolveView maps a --view name onto the fully resolved saved view: its
// expression, its ordering, and its cap.
//
// Resolution goes through queryexpr.ResolveView rather than reading
// SavedView.Query directly, so `--view` sees exactly what `bn views` sees. A
// view that `bn views` renders disabled — a query that no longer compiles, a
// sort spec that disagrees with relative_to — is refused here with the same
// detail rather than being silently run as a query-only filter.
//
// The lookup is case-insensitive, matching Config.View and the uniqueness rule
// the config layer enforces, and an unknown name lists what is available rather
// than only reporting the miss.
func resolveView(cfg *config.Config, name string, env queryexpr.Env) (queryexpr.View, error) {
	if cfg == nil {
		return queryexpr.View{}, fmt.Errorf("unknown view %q: no configuration is loaded", name)
	}
	saved, ok := cfg.View(name)
	if !ok {
		known := cfg.ViewNames()
		if len(known) == 0 {
			return queryexpr.View{}, fmt.Errorf("unknown view %q: no views are defined in %s", name, config.FileName)
		}
		return queryexpr.View{}, fmt.Errorf("unknown view %q: defined views are %s", name, strings.Join(known, ", "))
	}
	view := queryexpr.ResolveView(saved, env)
	if !view.Enabled() {
		return queryexpr.View{}, fmt.Errorf("view %q is disabled: %s", view.Name(), view.Detail())
	}
	return view, nil
}

// rejectUnrunnableView refuses a saved view whose stored ordering or cap the
// calling command cannot execute.
//
// This is the one documented policy for view ordering across the CLI: `bn find`
// is the only flag-scoped command that produces an ordering and a cap, so it is
// the only one that runs a view carrying them. Every other command — `search`,
// `graph`, `lint`, and the scoped mutators — refuses such a view instead of
// running a partial version of it. Silently dropping the fields is what made a
// view report as enabled while executing as something narrower than it was
// saved as, and dropping a cap on a *mutating* command is the worst version of
// that: `bn replace --view top-10` would have rewritten every match.
func rejectUnrunnableView(command string, view queryexpr.View) error {
	name := view.Name()
	if command == "" {
		command = "this command"
	}
	if sort := strings.TrimSpace(view.Saved.Sort); sort != "" {
		return fmt.Errorf(
			"view %q stores sort %q, which %s cannot apply; run it with 'bn find --view %s'",
			name, sort, command, shellQuote(name))
	}
	if view.Saved.Limit > 0 {
		return fmt.Errorf(
			"view %q stores limit %d, which %s cannot apply; run it with 'bn find --view %s'",
			name, view.Saved.Limit, command, shellQuote(name))
	}
	return nil
}

// findSelection is the result of one filter run: the matching notes in plan
// order, the plan they were selected by, and the link graph if one was needed.
// The graph is returned because a caller that asked for a graph-reading filter
// usually needs the graph itself afterwards (--show-broken, `bn graph`), and
// rebuilding it would double the cost of the query.
type findSelection struct {
	notes []*vault.Note
	plan  *queryexpr.Plan
	graph *query.Graph
}

// selectNotes is the one execution path every flag-scoped command routes
// through: compile the plan, build the graph only when the plan asks for one,
// then evaluate. Because selection is a single Evaluator.Evaluate call, `find`,
// `search`, `graph`, `lint`, and the four scoped mutators cannot drift on what
// a filter means.
//
// g is the caller's already-built graph, or nil to let this build one lazily.
// Notes come back ordered by path, which is what the plan's zero SortSpec asks
// for and what every caller previously sorted by itself.
func (ff *findFlags) selectNotes(ctx context.Context, cfg *config.Config, root string, notes []*vault.Note, g *query.Graph) (findSelection, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	env := queryexpr.Env{Root: root, Graph: g}
	if cfg != nil {
		env.Frontmatter = &cfg.Frontmatter
	}
	plan, err := ff.compile(cfg, env)
	if err != nil {
		return findSelection{}, ff.commandError(err)
	}
	if env.Graph == nil && ff.needsGraph(plan) {
		env.Graph = query.BuildGraphForRoot(root, notes)
	}
	// The evaluator honours the default path ordering and refuses every other
	// key, and it truncates to the plan's Limit. Both belong to the caller here:
	// the ordering a view can store needs state queryexpr deliberately does not
	// hold, and a cap applied before that ordering would keep the wrong N. So
	// selection runs against a filter-only copy, and the plan the caller gets
	// back still carries the ordering and cap for it to apply.
	filterPlan := *plan
	filterPlan.Sort = queryexpr.SortSpec{}
	filterPlan.Limit = 0
	matched, err := queryexpr.NewMemoryEvaluator(root, notes, env).Evaluate(ctx, &filterPlan)
	if err != nil {
		return findSelection{}, ff.commandError(err)
	}
	return findSelection{notes: matched, plan: plan, graph: env.Graph}, nil
}

// commandError names the command a filter failure belongs to. A query parse
// error keeps its type through the wrap, so RenderError still draws its carets,
// under the same `bn: <cmd>:` prefix as every other refusal.
func (ff *findFlags) commandError(err error) error {
	if ff.command == "" || errors.Is(err, context.Canceled) {
		return err
	}
	return fmt.Errorf("%s: %w", ff.command, err)
}

func noteKey(n *vault.Note) string {
	base := filepath.Base(n.Path)
	return strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base)))
}

// queryFlagNames lists the `find`-style flags that scope a mutating command
// (`replace`, `relink`, `repair`, `rename-key`) and `search`. It lives here
// beside the flag surface it names, together with its only reader, because it
// must list every registered scoping flag or the note-args / find-flags mutual
// exclusion silently stops firing.
var queryFlagNames = []string{
	"tag", "category", "status", "type", "has", "missing",
	"age", "created", "folder", "orphan", "broken-links",
	"min-backlinks", "max-backlinks", "min-links", "max-links",
	"dead-end", "query", "view",
}

// anyQueryFlagSet reports whether any find-style scoping flag was set on cmd.
func anyQueryFlagSet(cmd *cobra.Command) bool {
	for _, name := range queryFlagNames {
		if f := cmd.Flag(name); f != nil && f.Changed {
			return true
		}
	}
	return false
}
