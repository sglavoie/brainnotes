package queryexpr

import (
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/query"
)

// Env is the vault state a compiled query needs to become a predicate. It is
// passed twice on purpose: Compile takes it to resolve field names against the
// vault's declared properties, and Plan.Predicate takes it again to build the
// closures. That split is what makes Plan.NeedsGraph useful — a caller can
// compile (and reject) a query with Graph nil, and only pay for
// query.BuildGraphForRoot when the plan says the graph is needed.
type Env struct {
	// Root is the absolute vault root, required by the folder field.
	Root string
	// Frontmatter supplies the declared property names used for field
	// resolution and did-you-mean candidates. Nil means "no vault config":
	// reserved fields still compile, every other name is unknown.
	Frontmatter *config.FrontmatterConfig
	// Graph is required whenever Plan.NeedsGraph reports true.
	Graph *query.Graph
	// Metrics is required by the traversal fields (near, path-to, degree,
	// cluster-size). Plan.Predicate derives it from Graph when a caller leaves
	// it nil, so supplying it is an optimization for callers that already have
	// one, not an obligation.
	Metrics *query.GraphMetrics
	// Now pins the clock. It is unused by the fields this package lowers today
	// — query.Age reads time.Now itself — and exists for the relative date
	// literals typed frontmatter comparison introduces.
	Now func() time.Time

	// memo caches BFS results across the graph terms of one compiled
	// predicate. It is unexported because it is per-call state rather than
	// vault state: Plan.Predicate installs a fresh one, and it is discarded
	// once the predicate closures are built. A caller cannot supply a stale
	// one, and nothing survives to be read during the scan — which is what
	// keeps GraphMetrics immutable and shareable across concurrent requests.
	memo *graphMemo
}

// Plan is a compiled query: the validated expression, the ordering and cap a
// caller asked for, and the source text, kept so a later error can render
// carets against what the user typed.
type Plan struct {
	Expr   Expr
	Sort   SortSpec
	Limit  int
	Source string
}

// MissingStateError reports that a compiled plan needs vault state its Env did
// not carry. It is a caller error rather than a query error — the query was
// already accepted at compile time — so it is deliberately not a *ParseError
// and has no column to point at.
type MissingStateError struct {
	Field string
	Need  string
}

func (e *MissingStateError) Error() string {
	return "field \"" + e.Field + "\" requires a " + e.Need
}

// Compile parses src and validates every term in it, returning a plan whose
// Expr is known to lower cleanly. Errors are *ParseError, so a CLI can call
// Render for a caret-annotated block.
//
// Compilation needs no graph: a query naming graph fields compiles against an
// Env with Graph nil and reports that fact through Plan.NeedsGraph.
func Compile(src string, env Env) (*Plan, error) {
	expr, err := Parse(src)
	if err != nil {
		return nil, err
	}
	if _, err := exprBuilder(expr, src, env); err != nil {
		return nil, err
	}
	return &Plan{Expr: expr, Source: src}, nil
}

// Predicate lowers the plan onto a single query.Predicate.
//
// It re-walks the expression rather than caching builders from Compile, which
// is what lets the same plan be evaluated against different vault states — a
// saved view compiled once and run against successive snapshots, for instance.
// When the plan needs graph metrics and env carries only the graph, metrics are
// derived once here rather than per term.
func (p *Plan) Predicate(env Env) (query.Predicate, error) {
	if p == nil || p.Expr == nil {
		return query.And(), nil
	}
	build, err := exprBuilder(p.Expr, p.Source, env)
	if err != nil {
		return nil, err
	}
	if env.Metrics == nil && env.Graph != nil && p.needsMetrics() {
		env.Metrics = query.NewGraphMetrics(env.Graph)
	}
	if env.memo == nil {
		env.memo = newGraphMemo()
	}
	return build(env)
}

// NeedsGraph reports whether any term reads the link graph, letting a caller
// build one lazily the way findFlags.needsGraph does for the flag surface.
func (p *Plan) NeedsGraph() bool {
	return p.anyField(func(f reservedField) bool { return f.needsGraph })
}

// needsMetrics reports whether any term needs GraphMetrics on top of the graph.
func (p *Plan) needsMetrics() bool {
	return p.anyField(func(f reservedField) bool { return f.needsMetrics })
}

// anyField reports whether any term names a reserved field satisfying want.
func (p *Plan) anyField(want func(reservedField) bool) bool {
	if p == nil {
		return false
	}
	found := false
	forEachTerm(p.Expr, func(t *Term) {
		if found {
			return
		}
		if f, ok := lookupReserved(t.Field); ok && want(f) {
			found = true
		}
	})
	return found
}

// forEachTerm visits every Term in the tree in source order.
func forEachTerm(e Expr, visit func(*Term)) {
	switch node := e.(type) {
	case nil:
		return
	case *And:
		for _, child := range node.Operands {
			forEachTerm(child, visit)
		}
	case *Or:
		for _, child := range node.Operands {
			forEachTerm(child, visit)
		}
	case *Not:
		forEachTerm(node.Operand, visit)
	case *Term:
		visit(node)
	}
}

// exprBuilder validates a subtree and returns the builder for it. Combining
// happens at build time so an And of graph terms still resolves its targets
// against the Env it is finally given.
func exprBuilder(e Expr, src string, env Env) (predicateBuilder, error) {
	switch node := e.(type) {
	case nil:
		return func(Env) (query.Predicate, error) { return query.And(), nil }, nil
	case *And:
		children, err := operandBuilders(node.Operands, src, env)
		if err != nil {
			return nil, err
		}
		return combine(children, query.And), nil
	case *Or:
		children, err := operandBuilders(node.Operands, src, env)
		if err != nil {
			return nil, err
		}
		// A zero-operand Or matches nothing, which the parser never produces
		// but a hand-built AST can.
		return combine(children, query.Or), nil
	case *Not:
		inner, err := exprBuilder(node.Operand, src, env)
		if err != nil {
			return nil, err
		}
		return func(env Env) (query.Predicate, error) {
			pred, err := inner(env)
			if err != nil {
				return nil, err
			}
			return negate(pred), nil
		}, nil
	case *Term:
		return termBuilder(node, src, env)
	}
	return nil, NewParseError(src, e.Range(), "unsupported expression node", nil)
}

func operandBuilders(operands []Expr, src string, env Env) ([]predicateBuilder, error) {
	out := make([]predicateBuilder, 0, len(operands))
	for _, child := range operands {
		if child == nil {
			continue
		}
		build, err := exprBuilder(child, src, env)
		if err != nil {
			return nil, err
		}
		out = append(out, build)
	}
	return out, nil
}

func combine(children []predicateBuilder, join func(...query.Predicate) query.Predicate) predicateBuilder {
	return func(env Env) (query.Predicate, error) {
		ps := make([]query.Predicate, 0, len(children))
		for _, child := range children {
			pred, err := child(env)
			if err != nil {
				return nil, err
			}
			ps = append(ps, pred)
		}
		return join(ps...), nil
	}
}
