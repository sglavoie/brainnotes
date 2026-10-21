package queryexpr

import (
	"sort"
	"strings"

	"github.com/sglavoie/brainnotes/internal/query"
	"github.com/sglavoie/brainnotes/internal/textnorm"
)

// Graph-traversal fields: the half of the reserved vocabulary that reads link
// structure rather than frontmatter. They live here rather than in fields.go
// because they are the only fields with two properties nothing else has.
//
// First, a graph term names a *note*, so it can be wrong in a way `tag:alpah`
// cannot: the field is spelled correctly, the operator is legal, and the query
// still refers to nothing. Session 3's builders answer that with a predicate
// that matches no note (internal/query/filter_graph_ops.go:19), which is right
// for a layer with no error channel and wrong for a user typing at a prompt —
// an empty result set is indistinguishable from a correct empty result set. So
// every target here is resolved through Graph.ResolveTarget *before* the
// predicate is built, and an unresolvable one is a *ParseError with
// did-you-mean candidates drawn from the vault's own titles.
//
// Second, they are the only fields whose evaluation cost is a graph traversal.
// near and path-to are both reachability, so they share one memo — see
// graphMemo — that lives for exactly one Plan.Predicate call. It is deliberately
// not on GraphMetrics: that value is built once per command run or web snapshot
// and read concurrently by every request, and a cache hung off it would either
// need a mutex on the hot path or would be a data race. Here, the memo is
// consulted only while predicates are being built and is never touched during
// the scan itself, so concurrent scans over one shared GraphMetrics stay
// lock-free.

// graphFields is the six traversal and structure fields, merged into
// reservedFields by withGraphFields. Splitting the table this way keeps the
// "which fields need a graph" answer in one place per file rather than making
// fields.go the only thing that knows the whole vocabulary.
var graphFields = map[string]reservedField{
	"links-to": {name: "links-to", needsGraph: true, lowerWithEnv: adjacencyField("links-to",
		func(env Env, target string) query.Predicate { return query.LinksTo(env.Graph, target) })},
	"linked-from": {name: "linked-from", needsGraph: true, lowerWithEnv: adjacencyField("linked-from",
		func(env Env, target string) query.Predicate { return query.LinkedFrom(env.Graph, target) })},

	"near":    {name: "near", needsGraph: true, needsMetrics: true, lowerWithEnv: lowerNear},
	"path-to": {name: "path-to", needsGraph: true, needsMetrics: true, lowerWithEnv: lowerPathTo},

	"degree":       {name: "degree", needsGraph: true, needsMetrics: true, lower: metricField("degree", query.Degree)},
	"cluster-size": {name: "cluster-size", needsGraph: true, needsMetrics: true, lower: metricField("cluster-size", query.ClusterSize)},
}

// withGraphFields returns base extended with the graph fields. It panics on a
// duplicate name, which can only be a programming error: two tables claiming
// one field name would make which lowering runs depend on map merge order.
func withGraphFields(base map[string]reservedField) map[string]reservedField {
	out := make(map[string]reservedField, len(base)+len(graphFields))
	for name, field := range base {
		out[name] = field
	}
	for name, field := range graphFields {
		if _, dup := out[name]; dup {
			panic("queryexpr: reserved field " + name + " is declared in both fields.go and graph_terms.go")
		}
		out[name] = field
	}
	return out
}

// adjacencyField lowers a graph term whose predicate reads direct adjacency and
// runs no traversal: links-to and linked-from. Both spellings the grammar allows
// reach the same place — `links-to:[[Deep Work]]` (the value form) and
// `links-to("Deep Work")` (the argument form) — and a list is any-of.
//
// Targets are checked twice on purpose. Once here, so a query compiled against
// an Env that already carries a graph fails at compile time, and once inside the
// builder, so a query compiled without one (the NeedsGraph-then-build path every
// caller uses) still fails rather than matching nothing.
func adjacencyField(name string, build func(Env, string) query.Predicate) func(*Term, string, Env) (predicateBuilder, error) {
	return func(t *Term, src string, env Env) (predicateBuilder, error) {
		atoms, negated, err := targetAtoms(t, src, name)
		if err != nil {
			return nil, err
		}
		if err := checkTargets(atoms, src, env.Graph); err != nil {
			return nil, err
		}
		return func(env Env) (query.Predicate, error) {
			if err := checkTargets(atoms, src, env.Graph); err != nil {
				return nil, err
			}
			ps := make([]query.Predicate, 0, len(atoms))
			for _, atom := range atoms {
				ps = append(ps, build(env, atom.Text))
			}
			return anyOf(ps, negated), nil
		}, nil
	}
}

// lowerNear lowers the one term that carries both a target and a bound:
// near:[[Deep Work]]<=2. The hop count is strict for `<`, so `<2` is two hops
// minus one. Without a bound — `near:[[Deep Work]]` or `near([[Deep Work]])` —
// the radius is one hop.
//
// Direction is query.DirBoth: the surface has no direction syntax, so the
// candidate-anchored reading session 3 defined (DirOut means "the note reaches
// the target by its own outgoing links") is not yet reachable from a query. Note
// that the anchor note itself is at distance 0 and therefore always matches.
func lowerNear(t *Term, src string, env Env) (predicateBuilder, error) {
	const name = "near"
	var target Atom
	hops := 1

	switch {
	case t.HasArg && t.Op == OpNone:
		target = t.Arg
	case t.HasArg:
		target = t.Arg
		switch t.Op {
		case OpLe, OpLt:
		default:
			return nil, NewParseError(src, t.Span, "operator "+quote(string(t.Op))+" is not defined for field "+quote(name)+"; use \"<=\" or \"<\"", nil)
		}
		n, err := singleInt(t, src, name)
		if err != nil {
			return nil, err
		}
		hops = n
		if t.Op == OpLt {
			hops = n - 1
		}
	case t.Op == OpMatch || t.Op == OpEq:
		atoms, _, err := targetAtoms(t, src, name)
		if err != nil {
			return nil, err
		}
		if len(atoms) != 1 {
			return nil, NewParseError(src, t.Value.Span, "field "+quote(name)+" takes a single target, not a list", nil)
		}
		target = atoms[0]
	default:
		return nil, NewParseError(src, t.Span, "field "+quote(name)+" expects a target, e.g. near:[[Deep Work]]<=2", nil)
	}

	if hops < 0 {
		// near:[[X]]<0 asks for a negative radius. That is the empty result,
		// not the unbounded traversal a negative bound means inside
		// reachabilityBuilder — but the target is still validated first, so a
		// nonsense radius does not mask a misspelled note.
		if err := checkTargets([]Atom{target}, src, env.Graph); err != nil {
			return nil, err
		}
		return func(env Env) (query.Predicate, error) {
			if err := checkTargets([]Atom{target}, src, env.Graph); err != nil {
				return nil, err
			}
			return matchNothing, nil
		}, nil
	}
	return reachabilityBuilder([]Atom{target}, src, false, query.DirBoth, hops, env)
}

// lowerPathTo lowers path-to onto unbounded reachability. It is Near with no hop
// limit rather than an A-to-B path membership test: the grammar's single-target
// form has nowhere to put a second anchor.
func lowerPathTo(t *Term, src string, env Env) (predicateBuilder, error) {
	atoms, negated, err := targetAtoms(t, src, "path-to")
	if err != nil {
		return nil, err
	}
	return reachabilityBuilder(atoms, src, negated, query.DirBoth, -1, env)
}

// reachabilityBuilder is the shared lowering behind near and path-to: resolve
// the anchors, BFS out of each of them through the memo, and lower the union
// onto query.Reachable — the membership test both of session 3's traversal
// predicates already end in.
//
// A negative maxHops means unbounded, which is what path-to wants; a negative
// hop count the *user* wrote (near:[[X]]<0) is caught by lowerNear before it
// gets here and collapses to the empty result.
func reachabilityBuilder(atoms []Atom, src string, negated bool, dir query.Direction, maxHops int, env Env) (predicateBuilder, error) {
	if err := checkTargets(atoms, src, env.Graph); err != nil {
		return nil, err
	}
	return func(env Env) (query.Predicate, error) {
		if err := checkTargets(atoms, src, env.Graph); err != nil {
			return nil, err
		}
		// The BFS runs from the anchors outwards along the reversed direction,
		// because dir is read as travel from the candidate note *towards* the
		// target. One traversal per anchor serves the whole scan.
		reachable := make(map[string]struct{})
		for _, atom := range atoms {
			for _, key := range env.Graph.ResolveTarget(atom.Text) {
				for reached, hops := range env.memo.distances(env.Metrics, key, reverseDirection(dir)) {
					if maxHops >= 0 && hops > maxHops {
						continue
					}
					reachable[reached] = struct{}{}
				}
			}
		}
		pred := query.Reachable(env.Graph, reachable)
		if negated {
			pred = negate(pred)
		}
		return pred, nil
	}, nil
}

// metricField lowers degree and cluster-size onto a builder taking the operator
// as a string, which is exactly the shape query.compareInt accepts. `:` means
// `=`, and the operator reaching the builder is always one the lexer produced,
// so the silently-false branch of compareInt is unreachable from a parsed query.
//
// Neither field names a target, so neither can be unresolvable and neither runs
// a traversal — they read the precomputed component and degree tables.
func metricField(name string, build func(*query.Graph, *query.GraphMetrics, string, int) query.Predicate) func(*Term, string) (predicateBuilder, error) {
	return func(t *Term, src string) (predicateBuilder, error) {
		op, want, err := comparison(t, src, name)
		if err != nil {
			return nil, err
		}
		if op == OpMatch {
			op = OpEq
		}
		return func(env Env) (query.Predicate, error) {
			return build(env.Graph, env.Metrics, string(op), want), nil
		}, nil
	}
}

// targetAtoms accepts both graph-target spellings: the argument form
// `links-to("Deep Work")` and the value form `links-to:[[Deep Work]]`.
func targetAtoms(t *Term, src, name string) ([]Atom, bool, error) {
	if t.HasArg && t.Op == OpNone {
		return []Atom{t.Arg}, false, nil
	}
	if t.HasArg {
		return nil, false, NewParseError(src, t.Span, "field "+quote(name)+" takes a target, not an argument and a comparison", nil)
	}
	return matchAtoms(t, src, name)
}

// checkTargets rejects a graph target that names no note in the vault.
//
// A nil graph means the caller has not assembled one yet: Compile is routinely
// called with Graph nil precisely so Plan.NeedsGraph can decide whether building
// one is worth it, and validating against a graph that does not exist would
// reject every graph query. The check is therefore deferred, not skipped — the
// builder repeats it once the graph arrives, so the diagnostic is only ever
// later, never absent.
func checkTargets(atoms []Atom, src string, g *query.Graph) error {
	if g == nil {
		return nil
	}
	for _, atom := range atoms {
		if len(g.ResolveTarget(atom.Text)) > 0 {
			continue
		}
		return NewParseError(src, atom.Span,
			"no note matches target "+quote(atom.Text), suggestTargets(g, atom.Text))
	}
	return nil
}

// suggestTargets returns up to three did-you-mean candidates for a target that
// resolves to nothing, drawn from the vault's own note titles.
//
// The candidate keyspace is Graph.NoteKeysByTitle, whose keys are *title keys*
// (lower-cased and NFC-normalized) rather than display titles, so what is
// rendered is Graph.OriginalTitles for the matching note keys — a suggestion has
// to be copy-pasteable straight back into the query, and a folded key is not.
//
// A candidate qualifies by edit distance (<= 2, the same bound unknown-field
// suggestions use) or by containing the probe, which is what makes a
// half-remembered title like `links-to:[[Work]]` suggest "Deep Work" when no
// number of single-rune edits would.
func suggestTargets(g *query.Graph, target string) []string {
	const maxDistance = 2
	const maxSuggestions = 3

	probe := targetProbe(target)
	if probe == "" || g == nil {
		return nil
	}

	type scored struct {
		title string
		dist  int
	}
	var hits []scored
	seen := make(map[string]struct{})
	for titleKey, keys := range g.NoteKeysByTitle {
		dist := damerauLevenshtein(probe, titleKey)
		if dist > maxDistance && !strings.Contains(titleKey, probe) {
			continue
		}
		for _, key := range keys {
			title := g.OriginalTitles[key]
			if title == "" {
				continue
			}
			if _, dup := seen[title]; dup {
				continue
			}
			seen[title] = struct{}{}
			hits = append(hits, scored{title: title, dist: dist})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].dist != hits[j].dist {
			return hits[i].dist < hits[j].dist
		}
		return hits[i].title < hits[j].title
	})
	if len(hits) > maxSuggestions {
		hits = hits[:maxSuggestions]
	}
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		out = append(out, hit.title)
	}
	return out
}

// targetProbe reduces a raw wikilink target to the folded title it is compared
// against: the last path segment of a folder-qualified target, with any heading
// or alias suffix dropped, folded the way Graph keys its titles.
func targetProbe(target string) string {
	target = strings.ReplaceAll(target, "\\", "/")
	if i := strings.LastIndex(target, "/"); i >= 0 {
		target = target[i+1:]
	}
	if i := strings.IndexAny(target, "#|"); i >= 0 {
		target = target[:i]
	}
	target = strings.TrimSpace(target)
	if strings.EqualFold(filepathExt(target), ".md") {
		target = target[:len(target)-len(".md")]
	}
	return strings.ToLower(textnorm.NFC(target))
}

// filepathExt is path.Ext over the final segment, spelled out so this file does
// not import a path package for one call on an already-split segment.
func filepathExt(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i:]
	}
	return ""
}

// reverseDirection is query.Direction.reverse, which is unexported. It is
// spelled out rather than exported from query because the only caller is the
// candidate-anchored reading of near/path-to, and today every call passes
// DirBoth, for which the reversal is the identity. Writing it out anyway means
// adding direction syntax to the surface is a change to one lowering rather
// than a silently wrong traversal.
func reverseDirection(d query.Direction) query.Direction {
	switch d {
	case query.DirOut:
		return query.DirIn
	case query.DirIn:
		return query.DirOut
	default:
		return query.DirBoth
	}
}

// bfsKey identifies one traversal: the anchor note key it starts from and the
// direction it expands in. The hop bound is deliberately not part of the key —
// see graphMemo.distances.
type bfsKey struct {
	key string
	dir query.Direction
}

// graphMemo caches BFS distance maps for the lifetime of exactly one
// Plan.Predicate call.
//
// Two things follow from that lifetime. It is shared across terms, so
// `near:[[Hub]]<=2 or path-to:[[Hub]]` costs one traversal rather than two, and
// it holds nothing once the predicate is built, so a memo can never serve a
// stale graph: a plan re-evaluated against a newer snapshot gets a new memo with
// it. Neither is true of a cache on GraphMetrics, which outlives any one query
// and is read concurrently.
//
// The zero value is not usable; Plan.Predicate installs one. A nil *graphMemo
// still answers correctly, just without caching, so a hand-built Env cannot
// panic here.
type graphMemo struct {
	dist map[bfsKey]map[string]int
	// runs counts cache misses, which is the number of BFS traversals actually
	// performed. Tests assert it is one per anchor per scan rather than one per
	// note; nothing in production reads it.
	runs int
}

func newGraphMemo() *graphMemo {
	return &graphMemo{dist: make(map[bfsKey]map[string]int)}
}

// distances returns hop counts from key to every node reachable from it.
//
// The traversal is always unbounded, even when the term that asked for it has a
// hop limit, and the limit is applied by filtering the result. That is what lets
// one entry serve every bound: near:[[Hub]]<=1, near:[[Hub]]<=5, and
// path-to:[[Hub]] are three filters over one BFS. On a vault where the whole
// graph is a few hundred nodes, exploring a component that a bounded search
// would have cut short costs far less than a second traversal.
func (g *graphMemo) distances(m *query.GraphMetrics, key string, dir query.Direction) map[string]int {
	if g == nil || g.dist == nil {
		return m.Distances(key, dir, -1)
	}
	id := bfsKey{key: key, dir: dir}
	if cached, ok := g.dist[id]; ok {
		return cached
	}
	dist := m.Distances(key, dir, -1)
	if dist == nil {
		// An unknown key yields nil; cache the empty map so a repeated miss
		// does not look like a repeated traversal.
		dist = map[string]int{}
	}
	g.dist[id] = dist
	g.runs++
	return dist
}
