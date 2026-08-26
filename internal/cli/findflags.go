package cli

import (
	"context"
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
	hasProp      string
	missingProp  string
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
}

func (ff *findFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringSliceVar(&ff.tags, "tag", nil, "filter by tag (repeatable; AND semantics)")
	f.StringSliceVar(&ff.categories, "category", nil, "filter by category target (repeatable; AND semantics)")
	f.StringVar(&ff.status, "status", "", "filter by status property")
	f.StringVar(&ff.typeProp, "type", "", "filter by type/kind property")
	f.StringVar(&ff.hasProp, "has", "", "require property present")
	f.StringVar(&ff.missingProp, "missing", "", "require property absent")
	f.StringVar(&ff.age, "age", "", "filter by 'last' property age, e.g. >30d, <7d, =0d")
	f.StringVar(&ff.created, "created", "", "filter by 'created' property age, e.g. >30d, <7d, =0d")
	f.StringVar(&ff.folder, "folder", "", "limit to a vault-relative folder (matches descendants)")
	f.BoolVar(&ff.orphan, "orphan", false, "only notes nothing in the vault links to")
	f.BoolVar(&ff.pinned, "pinned", false, "only notes pinned via the 'pinned' property")
	f.BoolVar(&ff.brokenLinks, "broken-links", false, "only notes with unresolved wikilinks")
	f.IntVar(&ff.minBacklinks, "min-backlinks", -1, "minimum inbound links from other notes")
	f.IntVar(&ff.maxBacklinks, "max-backlinks", -1, "maximum inbound links from other notes")
	f.IntVar(&ff.minLinks, "min-links", -1, "minimum outgoing links to other notes")
	f.IntVar(&ff.maxLinks, "max-links", -1, "maximum outgoing links to other notes")
	f.BoolVar(&ff.deadEnd, "dead-end", false, "notes with zero outgoing links (except dead_end: false)")
	f.StringVarP(&ff.query, "query", "q", "", "filter with a query expression, e.g. 'tag:foo and not folder:Archive'")
	f.StringVar(&ff.view, "view", "", "filter with a saved view by name")
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
		Has:          ff.hasProp,
		Missing:      ff.missingProp,
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

	viewQuery := ""
	if name := strings.TrimSpace(ff.view); name != "" {
		viewQuery, err = resolveViewQuery(cfg, name)
		if err != nil {
			return nil, err
		}
	}

	operands := []queryexpr.Expr{flagsExpr}
	// Source is what a later ParseError — one raised by Plan.Predicate rather
	// than by Compile — renders against, so it holds the text the user typed
	// in preference to a view's stored text.
	source := ""
	for _, src := range []string{ff.query, viewQuery} {
		if strings.TrimSpace(src) == "" {
			continue
		}
		part, err := queryexpr.Compile(src, env)
		if err != nil {
			return nil, err
		}
		operands = append(operands, part.Expr)
		if source == "" {
			source = src
		}
	}

	return &queryexpr.Plan{Expr: &queryexpr.And{Operands: operands}, Source: source}, nil
}

// resolveViewQuery maps a --view name onto the expression that view saves.
//
// Only the view's query text is returned. A view also persists an ordering and
// a cap, and those are not applied here: `--sort`/`--relative-to` are `bn find`
// flags while --view is on every flag-scoped command, so honouring a stored
// sort has to be a decision made where the ordering is produced, not where the
// filter is compiled. What --view means today is therefore exactly "AND in this
// saved expression", which composes with every other filter source without
// surprising a command that has no ordering to apply.
//
// The lookup is case-insensitive, matching Config.View and the uniqueness rule
// the config layer enforces, and an unknown name lists what is available rather
// than only reporting the miss.
func resolveViewQuery(cfg *config.Config, name string) (string, error) {
	view, ok := cfg.View(name)
	if !ok {
		known := cfg.ViewNames()
		if len(known) == 0 {
			return "", fmt.Errorf("unknown view %q: no views are defined in %s", name, config.FileName)
		}
		return "", fmt.Errorf("unknown view %q: defined views are %s", name, strings.Join(known, ", "))
	}
	return view.Query, nil
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
		return findSelection{}, err
	}
	if env.Graph == nil && ff.needsGraph(plan) {
		env.Graph = query.BuildGraphForRoot(root, notes)
	}
	matched, err := queryexpr.NewMemoryEvaluator(root, notes, env).Evaluate(ctx, plan)
	if err != nil {
		return findSelection{}, err
	}
	return findSelection{notes: matched, plan: plan, graph: env.Graph}, nil
}

func noteKey(n *vault.Note) string {
	base := filepath.Base(n.Path)
	return strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base)))
}
