package query

import "github.com/sglavoie/sglavoie_brainnotes/internal/vault"

// Graph-shaped predicate builders. Like Orphan and DeadEnd in filter_structural.go, each
// returns a closure over an already-built *Graph (and, where structure beyond
// direct adjacency is needed, an already-built *GraphMetrics); like BrokenLinks
// in broken.go they live in their own file rather than growing filter.go.
//
// Two conventions hold across every builder here:
//
//   - Ambiguous targets are any-of. A duplicate title makes Graph.ResolveTarget
//     return several note keys, and a predicate matches if it matches for any one
//     of them — the same union ForwardLinks/BackLinks already perform over
//     lookupKeys.
//   - Whatever a target resolves to is computed once, when the predicate is
//     built, not once per note. A near: term therefore runs one BFS per scan.
//     An unresolvable target yields a predicate that matches nothing; callers
//     that want a "no such note" diagnostic must check ResolveTarget themselves
//     before building the predicate.
//
// Note keys, not titles, are the currency here: GraphMetrics is keyed by note
// key (e.g. `areas/foo`), so every builder maps a candidate note through
// Graph.KeyForNote and a target string through Graph.ResolveTarget.

// LinksTo matches notes with a direct outgoing link to any note target resolves
// to. target is a raw wikilink target: a bare title ("Deep Work") or a
// folder-qualified path ("Areas/Deep Work"), with or without a heading or alias.
//
// Self-links do not count, because Graph.Forward already excludes them: a note
// that only links to itself does not match LinksTo on its own title.
func LinksTo(g *Graph, target string) Predicate {
	targets := resolvedTargetSet(g, target)
	return func(n *vault.Note) bool {
		key := g.KeyForNote(n)
		if key == "" || len(targets) == 0 {
			return false
		}
		for _, dst := range g.Forward[key] {
			if _, ok := targets[dst]; ok {
				return true
			}
		}
		return false
	}
}

// LinkedFrom matches notes that any note target resolves to links *to* — the
// inverse of LinksTo. It is derived by walking Graph.Forward from the resolved
// sources rather than reading Graph.Backward, because Backward is keyed to
// source *paths* (a different keyspace) and also holds entries for unresolved
// link targets that are not notes.
func LinkedFrom(g *Graph, target string) Predicate {
	linked := make(map[string]struct{})
	for src := range resolvedTargetSet(g, target) {
		for _, dst := range g.Forward[src] {
			linked[dst] = struct{}{}
		}
	}
	return Reachable(g, linked)
}

// Reachable matches notes whose graph key is in keys: the membership test every
// set-shaped predicate here ends in.
//
// It is exported because the set is the expensive part and the test is not. A
// caller that has already computed which keys it wants — internal/queryexpr
// memoizes BFS distance maps across the terms of one compiled predicate, so
// near:[[A]]<=2 and path-to:[[A]] share a single traversal — lowers onto this
// rather than re-running the traversal inside Near or PathTo.
func Reachable(g *Graph, keys map[string]struct{}) Predicate {
	return func(n *vault.Note) bool {
		key := g.KeyForNote(n)
		if key == "" {
			return false
		}
		_, ok := keys[key]
		return ok
	}
}

// Near matches notes within maxHops link hops of any note target resolves to.
// A negative maxHops is unbounded (equivalent to PathTo); zero matches only the
// anchor notes themselves.
//
// dir is read as the direction of travel *from the candidate note towards the
// target*: DirOut matches notes that reach the target by following their own
// outgoing links, DirIn matches notes the target reaches, and DirBoth ignores
// edge orientation. The BFS is run once, from the target outwards along the
// reversed direction, so the whole scan costs one traversal per resolved target
// key rather than one per note.
//
// The anchor notes are at distance 0 and therefore match any non-negative
// maxHops. Callers that want strictly "the neighbourhood but not the note
// itself" must exclude the anchors explicitly.
func Near(g *Graph, m *GraphMetrics, target string, dir Direction, maxHops int) Predicate {
	return withinDistance(g, m, target, dir, maxHops)
}

// PathTo matches notes from which any note target resolves to is reachable at
// all, with no hop limit. dir is read exactly as in Near. Anchors match
// themselves, and unreachable notes — including notes in another connected
// component — do not.
func PathTo(g *Graph, m *GraphMetrics, target string, dir Direction) Predicate {
	return withinDistance(g, m, target, dir, -1)
}

// withinDistance builds the reachable-key set once, unioning the BFS from every
// resolved target key, and returns a membership test over it.
func withinDistance(g *Graph, m *GraphMetrics, target string, dir Direction, maxHops int) Predicate {
	reachable := make(map[string]struct{})
	for key := range resolvedTargetSet(g, target) {
		for reached := range m.Distances(key, dir.reverse(), maxHops) {
			reachable[reached] = struct{}{}
		}
	}
	return Reachable(g, reachable)
}

// ClusterSize matches notes whose undirected connected component has a size
// satisfying `size op want` (see compareInt for the accepted operators).
//
// An isolated note is a component of size 1, not 0, so `<=3` matches orphans as
// well as small clusters — that is the intended hub/orphan discovery semantics.
// A note absent from the graph (an unresolvable key) never matches.
func ClusterSize(g *Graph, m *GraphMetrics, op string, want int) Predicate {
	return func(n *vault.Note) bool {
		key := g.KeyForNote(n)
		if key == "" || !m.has(key) {
			return false
		}
		return compareInt(m.ComponentSize(key), op, want)
	}
}

// Degree matches notes whose undirected degree — the number of distinct notes
// they link to or are linked from — satisfies `degree op want`.
//
// This is GraphMetrics.Degree, keyed by note key, not Graph.OutDegree/InDegree,
// which are keyed by title and count each direction separately. A note absent
// from the graph never matches, so `degree=0` means "an unlinked note" rather
// than "not a note".
func Degree(g *Graph, m *GraphMetrics, op string, want int) Predicate {
	return func(n *vault.Note) bool {
		key := g.KeyForNote(n)
		if key == "" || !m.has(key) {
			return false
		}
		return compareInt(m.Degree(key), op, want)
	}
}

// resolvedTargetSet resolves a raw wikilink target to the set of note keys it
// names, which is more than one exactly when the vault has duplicate titles.
func resolvedTargetSet(g *Graph, target string) map[string]struct{} {
	keys := g.ResolveTarget(target)
	if len(keys) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		set[key] = struct{}{}
	}
	return set
}

// compareInt evaluates `got op want`. Accepted operators are "=", "==", "!=",
// "<", "<=", ">", and ">="; any other operator compares false, matching how Age
// treats an unrecognized operator rather than panicking mid-scan.
func compareInt(got int, op string, want int) bool {
	switch op {
	case "=", "==":
		return got == want
	case "!=":
		return got != want
	case "<":
		return got < want
	case "<=":
		return got <= want
	case ">":
		return got > want
	case ">=":
		return got >= want
	}
	return false
}
