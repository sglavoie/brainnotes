package query

import "sort"

// Direction selects which edge orientation a graph traversal follows.
type Direction int

const (
	// DirOut follows outgoing links (the note links to the neighbour).
	DirOut Direction = iota
	// DirIn follows incoming links (the neighbour links to the note).
	DirIn
	// DirBoth treats every link as undirected.
	DirBoth
)

// reverse returns the direction that traverses the same edges backwards.
func (d Direction) reverse() Direction {
	switch d {
	case DirOut:
		return DirIn
	case DirIn:
		return DirOut
	default:
		return DirBoth
	}
}

// GraphMetrics is an immutable, lock-free view of a *Graph's structure: sorted
// adjacency in each direction, undirected connected components, and undirected
// degree. It is built once per command run or web snapshot and is safe to share
// across concurrent readers — every accessor returns freshly allocated results
// and no method mutates the receiver.
//
// Adjacency is derived by inverting Graph.Forward, never by reading
// Graph.Backward: Backward maps a key to source *paths* (a different keyspace)
// and holds entries for unresolved link targets that are not notes. Every node
// is therefore restricted to Graph.Titles membership, so a broken-link target
// contributes no phantom node and cannot inflate component counts.
type GraphMetrics struct {
	nodes    []string
	nodeSet  map[string]struct{}
	out      map[string][]string
	in       map[string][]string
	both     map[string][]string
	compOf   map[string]int
	compSize map[string]int
	comps    [][]string
	degrees  map[string]int
}

// NewGraphMetrics builds the metrics view for g. A nil graph yields an empty
// but usable value.
func NewGraphMetrics(g *Graph) *GraphMetrics {
	m := &GraphMetrics{
		nodeSet:  make(map[string]struct{}),
		out:      make(map[string][]string),
		in:       make(map[string][]string),
		both:     make(map[string][]string),
		compOf:   make(map[string]int),
		compSize: make(map[string]int),
		degrees:  make(map[string]int),
	}
	if g == nil {
		return m
	}

	nodes := make([]string, 0, len(g.Titles))
	for key := range g.Titles {
		if key == "" {
			continue
		}
		nodes = append(nodes, key)
		m.nodeSet[key] = struct{}{}
	}
	sort.Strings(nodes)
	m.nodes = nodes

	outSets := make(map[string]map[string]struct{})
	inSets := make(map[string]map[string]struct{})
	bothSets := make(map[string]map[string]struct{})
	addEdge := func(sets map[string]map[string]struct{}, from, to string) {
		set := sets[from]
		if set == nil {
			set = make(map[string]struct{})
			sets[from] = set
		}
		set[to] = struct{}{}
	}

	for _, src := range nodes {
		for _, dst := range g.Forward[src] {
			if dst == src {
				continue
			}
			if _, ok := m.nodeSet[dst]; !ok {
				continue
			}
			addEdge(outSets, src, dst)
			addEdge(inSets, dst, src)
			addEdge(bothSets, src, dst)
			addEdge(bothSets, dst, src)
		}
	}

	for key, set := range outSets {
		m.out[key] = mapKeysSorted(set)
	}
	for key, set := range inSets {
		m.in[key] = mapKeysSorted(set)
	}
	for key, set := range bothSets {
		neighbours := mapKeysSorted(set)
		m.both[key] = neighbours
		m.degrees[key] = len(neighbours)
	}

	m.buildComponents()
	return m
}

// buildComponents walks the undirected adjacency in sorted node order, so each
// component is discovered by its lexicographically smallest member.
func (m *GraphMetrics) buildComponents() {
	seen := make(map[string]struct{}, len(m.nodes))
	for _, start := range m.nodes {
		if _, ok := seen[start]; ok {
			continue
		}
		id := len(m.comps)
		component := []string{start}
		seen[start] = struct{}{}
		frontier := []string{start}
		for len(frontier) > 0 {
			var next []string
			for _, key := range frontier {
				for _, nb := range m.both[key] {
					if _, ok := seen[nb]; ok {
						continue
					}
					seen[nb] = struct{}{}
					component = append(component, nb)
					next = append(next, nb)
				}
			}
			sort.Strings(next)
			frontier = next
		}
		sort.Strings(component)
		m.comps = append(m.comps, component)
		for _, key := range component {
			m.compOf[key] = id
			m.compSize[key] = len(component)
		}
	}
}

func (m *GraphMetrics) adjacency(dir Direction) map[string][]string {
	switch dir {
	case DirOut:
		return m.out
	case DirIn:
		return m.in
	default:
		return m.both
	}
}

func (m *GraphMetrics) has(key string) bool {
	if m == nil {
		return false
	}
	_, ok := m.nodeSet[key]
	return ok
}

// Distances returns the hop count from fromKey to every node reachable in at
// most maxHops steps, including fromKey itself at distance 0. A negative
// maxHops means unbounded; zero returns only the source. An unknown fromKey
// returns nil. Frontiers expand in sorted key order.
func (m *GraphMetrics) Distances(fromKey string, dir Direction, maxHops int) map[string]int {
	if !m.has(fromKey) {
		return nil
	}
	adj := m.adjacency(dir)
	dist := map[string]int{fromKey: 0}
	frontier := []string{fromKey}
	for hop := 1; maxHops < 0 || hop <= maxHops; hop++ {
		if len(frontier) == 0 {
			break
		}
		var next []string
		for _, key := range frontier {
			for _, nb := range adj[key] {
				if _, seen := dist[nb]; seen {
					continue
				}
				dist[nb] = hop
				next = append(next, nb)
			}
		}
		sort.Strings(next)
		frontier = next
	}
	return dist
}

// Distance returns the hop count from fromKey to toKey within maxHops (negative
// means unbounded). The boolean reports whether toKey is reachable inside that
// bound.
func (m *GraphMetrics) Distance(fromKey, toKey string, dir Direction, maxHops int) (int, bool) {
	if !m.has(fromKey) || !m.has(toKey) {
		return 0, false
	}
	if fromKey == toKey {
		return 0, true
	}
	adj := m.adjacency(dir)
	seen := map[string]struct{}{fromKey: {}}
	frontier := []string{fromKey}
	for hop := 1; maxHops < 0 || hop <= maxHops; hop++ {
		if len(frontier) == 0 {
			break
		}
		var next []string
		for _, key := range frontier {
			for _, nb := range adj[key] {
				if _, ok := seen[nb]; ok {
					continue
				}
				if nb == toKey {
					return hop, true
				}
				seen[nb] = struct{}{}
				next = append(next, nb)
			}
		}
		sort.Strings(next)
		frontier = next
	}
	return 0, false
}

// ShortestPath returns the lexicographically smallest shortest path from
// fromKey to toKey inclusive, or nil when either key is unknown or toKey is
// unreachable. A path from a node to itself is that single node.
func (m *GraphMetrics) ShortestPath(fromKey, toKey string, dir Direction) []string {
	if !m.has(fromKey) || !m.has(toKey) {
		return nil
	}
	if fromKey == toKey {
		return []string{fromKey}
	}
	// Distances measured towards toKey: distTo[v] is the hop count from v to
	// toKey, so a greedy smallest-first walk that always steps to a neighbour
	// one hop closer yields the lexicographically smallest shortest path.
	distTo := m.Distances(toKey, dir.reverse(), -1)
	remaining, ok := distTo[fromKey]
	if !ok {
		return nil
	}
	adj := m.adjacency(dir)
	path := make([]string, 0, remaining+1)
	path = append(path, fromKey)
	cur := fromKey
	for remaining > 0 {
		stepped := false
		for _, nb := range adj[cur] {
			if d, ok := distTo[nb]; ok && d == remaining-1 {
				path = append(path, nb)
				cur = nb
				remaining--
				stepped = true
				break
			}
		}
		if !stepped {
			return nil
		}
	}
	return path
}

// ComponentSize returns the number of nodes in the undirected connected
// component containing key, or 0 for an unknown key. An isolated note is a
// component of size 1.
func (m *GraphMetrics) ComponentSize(key string) int {
	if m == nil {
		return 0
	}
	return m.compSize[key]
}

// Components returns every undirected connected component, each sorted, with
// the outer slice sorted by first member.
func (m *GraphMetrics) Components() [][]string {
	if m == nil || len(m.comps) == 0 {
		return nil
	}
	out := make([][]string, len(m.comps))
	for i, component := range m.comps {
		out[i] = append([]string(nil), component...)
	}
	return out
}

// Degree returns the undirected degree of key: the number of distinct notes it
// links to or is linked from. Unknown keys have degree 0.
//
// This is a different keyspace from Graph.OutDegree/InDegree, which take a
// *title*; Degree takes a note key.
func (m *GraphMetrics) Degree(key string) int {
	if m == nil {
		return 0
	}
	return m.degrees[key]
}
