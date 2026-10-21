package query

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sglavoie/brainnotes/internal/vault"
)

// metricsFixture builds a vault whose graph exercises every structural case the
// metrics need: a four-note chain with a diamond, an isolated note, a separate
// two-note component, a self-link, and a broken link target.
func metricsFixture(t *testing.T) (*Graph, *GraphMetrics) {
	t.Helper()
	root := t.TempDir()
	rels := map[string]string{
		"A.md": "---\ntitle: A\n---\n[[B]] and [[C]] and [[Nowhere]].\n",
		"B.md": "---\ntitle: B\n---\n[[D]]\n",
		"C.md": "---\ntitle: C\n---\n[[D]]\n",
		"D.md": "---\ntitle: D\n---\n[[D]] links to itself.\n",
		"E.md": "---\ntitle: E\n---\nIsolated.\n",
		"F.md": "---\ntitle: F\n---\n[[G]]\n",
		"G.md": "---\ntitle: G\n---\nLeaf.\n",
	}
	notes := make([]*vault.Note, 0, len(rels))
	for _, rel := range []string{"A.md", "B.md", "C.md", "D.md", "E.md", "F.md", "G.md"} {
		writeNote(t, root, rel, rels[rel])
		notes = append(notes, &vault.Note{Path: filepath.Join(root, rel)})
	}
	g := BuildGraph(notes)
	return g, NewGraphMetrics(g)
}

func TestGraphMetrics_Components(t *testing.T) {
	_, m := metricsFixture(t)

	want := [][]string{
		{"a", "b", "c", "d"},
		{"e"},
		{"f", "g"},
	}
	if got := m.Components(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Components() = %v, want %v", got, want)
	}

	for key, size := range map[string]int{
		"a": 4, "b": 4, "c": 4, "d": 4,
		"e": 1,
		"f": 2, "g": 2,
	} {
		if got := m.ComponentSize(key); got != size {
			t.Errorf("ComponentSize(%q) = %d, want %d", key, got, size)
		}
	}
	if got := m.ComponentSize("missing"); got != 0 {
		t.Errorf("ComponentSize(missing) = %d, want 0", got)
	}
}

func TestGraphMetrics_Degree(t *testing.T) {
	_, m := metricsFixture(t)

	// Degree is undirected and counts distinct neighbours. D links to itself,
	// which contributes nothing.
	for key, want := range map[string]int{
		"a": 2, "b": 2, "c": 2, "d": 2,
		"e": 0,
		"f": 1, "g": 1,
	} {
		if got := m.Degree(key); got != want {
			t.Errorf("Degree(%q) = %d, want %d", key, got, want)
		}
	}
	if got := m.Degree("missing"); got != 0 {
		t.Errorf("Degree(missing) = %d, want 0", got)
	}
}

func TestGraphMetrics_SelfLinkContributesNoEdge(t *testing.T) {
	g, m := metricsFixture(t)

	if targets := g.Forward["d"]; len(targets) != 0 {
		t.Fatalf("Forward[d] = %v, want empty (self-link excluded at build time)", targets)
	}
	if got := m.Degree("d"); got != 2 {
		t.Fatalf("Degree(d) = %d, want 2 (b and c only)", got)
	}
	if d, ok := m.Distance("d", "d", DirBoth, -1); !ok || d != 0 {
		t.Fatalf("Distance(d, d) = (%d, %v), want (0, true)", d, ok)
	}
	if got := m.ShortestPath("d", "d", DirOut); !reflect.DeepEqual(got, []string{"d"}) {
		t.Fatalf("ShortestPath(d, d) = %v, want [d]", got)
	}
}

func TestGraphMetrics_BrokenLinkTargetIsNotANode(t *testing.T) {
	g, m := metricsFixture(t)

	// Precondition: the broken target really is present in Backward, which is
	// exactly the trap that reading Backward instead of inverting Forward would
	// fall into.
	if _, ok := g.Backward["nowhere"]; !ok {
		t.Fatalf("Backward is missing the unresolved link key %q; fixture no longer exercises the trap", "nowhere")
	}
	if _, ok := g.Titles["nowhere"]; ok {
		t.Fatalf("Titles unexpectedly contains %q", "nowhere")
	}

	if got := m.Degree("nowhere"); got != 0 {
		t.Errorf("Degree(nowhere) = %d, want 0", got)
	}
	if got := m.ComponentSize("nowhere"); got != 0 {
		t.Errorf("ComponentSize(nowhere) = %d, want 0", got)
	}
	if got := m.Distances("nowhere", DirBoth, -1); got != nil {
		t.Errorf("Distances(nowhere) = %v, want nil", got)
	}
	if _, ok := m.Distance("a", "nowhere", DirOut, -1); ok {
		t.Errorf("Distance(a, nowhere) reported reachable")
	}
	if got := m.ShortestPath("a", "nowhere", DirOut); got != nil {
		t.Errorf("ShortestPath(a, nowhere) = %v, want nil", got)
	}

	total := 0
	for _, component := range m.Components() {
		total += len(component)
	}
	if total != len(g.Titles) {
		t.Fatalf("components cover %d nodes, want %d (phantom nodes leaked in)", total, len(g.Titles))
	}
	if got := m.Degree("a"); got != 2 {
		t.Fatalf("Degree(a) = %d, want 2; the broken link must not count", got)
	}
}

func TestGraphMetrics_Distances(t *testing.T) {
	_, m := metricsFixture(t)

	cases := []struct {
		name    string
		from    string
		dir     Direction
		maxHops int
		want    map[string]int
	}{
		{"out-unbounded", "a", DirOut, -1, map[string]int{"a": 0, "b": 1, "c": 1, "d": 2}},
		{"out-one-hop", "a", DirOut, 1, map[string]int{"a": 0, "b": 1, "c": 1}},
		{"out-zero-hops", "a", DirOut, 0, map[string]int{"a": 0}},
		{"in-unbounded", "d", DirIn, -1, map[string]int{"d": 0, "b": 1, "c": 1, "a": 2}},
		{"out-from-sink", "d", DirOut, -1, map[string]int{"d": 0}},
		{"both-unbounded", "d", DirBoth, -1, map[string]int{"d": 0, "b": 1, "c": 1, "a": 2}},
		{"isolated", "e", DirBoth, -1, map[string]int{"e": 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := m.Distances(tc.from, tc.dir, tc.maxHops)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Distances(%q) = %v, want %v", tc.from, got, tc.want)
			}
		})
	}

	if got := m.Distances("missing", DirBoth, -1); got != nil {
		t.Fatalf("Distances(missing) = %v, want nil", got)
	}
}

func TestGraphMetrics_Distance(t *testing.T) {
	_, m := metricsFixture(t)

	cases := []struct {
		name    string
		from    string
		to      string
		dir     Direction
		maxHops int
		want    int
		wantOK  bool
	}{
		{"self", "a", "a", DirOut, -1, 0, true},
		{"two-hops", "a", "d", DirOut, -1, 2, true},
		{"beyond-bound", "a", "d", DirOut, 1, 0, false},
		{"at-bound", "a", "d", DirOut, 2, 2, true},
		{"wrong-direction", "d", "a", DirOut, -1, 0, false},
		{"reverse-direction", "d", "a", DirIn, -1, 2, true},
		{"other-component", "a", "f", DirBoth, -1, 0, false},
		{"unknown-node", "a", "missing", DirBoth, -1, 0, false},
		{"zero-bound-self", "a", "a", DirOut, 0, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := m.Distance(tc.from, tc.to, tc.dir, tc.maxHops)
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("Distance(%q, %q) = (%d, %v), want (%d, %v)", tc.from, tc.to, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestGraphMetrics_Distance_MatchesDistances(t *testing.T) {
	_, m := metricsFixture(t)

	for _, dir := range []Direction{DirOut, DirIn, DirBoth} {
		for _, from := range []string{"a", "b", "c", "d", "e", "f", "g"} {
			dist := m.Distances(from, dir, -1)
			for _, to := range []string{"a", "b", "c", "d", "e", "f", "g"} {
				want, wantOK := dist[to]
				got, ok := m.Distance(from, to, dir, -1)
				if ok != wantOK || (ok && got != want) {
					t.Fatalf("dir=%d Distance(%q, %q) = (%d, %v), want (%d, %v)", dir, from, to, got, ok, want, wantOK)
				}
			}
		}
	}
}

func TestGraphMetrics_ShortestPathIsLexicographicallySmallest(t *testing.T) {
	_, m := metricsFixture(t)

	// Both a->b->d and a->c->d are shortest; b sorts first.
	if got := m.ShortestPath("a", "d", DirOut); !reflect.DeepEqual(got, []string{"a", "b", "d"}) {
		t.Fatalf("ShortestPath(a, d, out) = %v, want [a b d]", got)
	}
	if got := m.ShortestPath("d", "a", DirIn); !reflect.DeepEqual(got, []string{"d", "b", "a"}) {
		t.Fatalf("ShortestPath(d, a, in) = %v, want [d b a]", got)
	}
	if got := m.ShortestPath("a", "d", DirBoth); !reflect.DeepEqual(got, []string{"a", "b", "d"}) {
		t.Fatalf("ShortestPath(a, d, both) = %v, want [a b d]", got)
	}
	if got := m.ShortestPath("d", "a", DirOut); got != nil {
		t.Fatalf("ShortestPath(d, a, out) = %v, want nil", got)
	}
	if got := m.ShortestPath("a", "f", DirBoth); got != nil {
		t.Fatalf("ShortestPath(a, f) = %v, want nil across components", got)
	}
	if got := m.ShortestPath("missing", "a", DirBoth); got != nil {
		t.Fatalf("ShortestPath(missing, a) = %v, want nil", got)
	}
}

func TestGraphMetrics_ShortestPathSkipsSmallerDeadEnd(t *testing.T) {
	root := t.TempDir()
	rels := map[string]string{
		"Start.md":  "---\ntitle: Start\n---\n[[Beta]] and [[Zeta]].\n",
		"Beta.md":   "---\ntitle: Beta\n---\n[[Mid]]\n",
		"Mid.md":    "---\ntitle: Mid\n---\nDead end.\n",
		"Zeta.md":   "---\ntitle: Zeta\n---\n[[Target]]\n",
		"Target.md": "---\ntitle: Target\n---\nGoal.\n",
	}
	notes := make([]*vault.Note, 0, len(rels))
	for _, rel := range []string{"Start.md", "Beta.md", "Mid.md", "Zeta.md", "Target.md"} {
		writeNote(t, root, rel, rels[rel])
		notes = append(notes, &vault.Note{Path: filepath.Join(root, rel)})
	}
	m := NewGraphMetrics(BuildGraph(notes))

	// "beta" sorts before "zeta" but leads nowhere, so the greedy walk must
	// take the branch that actually shortens the remaining distance.
	want := []string{"start", "zeta", "target"}
	if got := m.ShortestPath("start", "target", DirOut); !reflect.DeepEqual(got, want) {
		t.Fatalf("ShortestPath(start, target) = %v, want %v", got, want)
	}
}

func TestGraphMetrics_DuplicateTitles(t *testing.T) {
	root := t.TempDir()
	writeNote(t, root, "Foo.md", "---\ntitle: Foo\n---\nRoot Foo.\n")
	writeNote(t, root, "Areas/Foo.md", "---\ntitle: Foo\n---\nArea Foo.\n")
	writeNote(t, root, "Hub.md", "---\ntitle: Hub\n---\n[[Foo]]\n")
	notes := []*vault.Note{
		{Path: filepath.Join(root, "Foo.md")},
		{Path: filepath.Join(root, "Areas", "Foo.md")},
		{Path: filepath.Join(root, "Hub.md")},
	}
	g := BuildGraph(notes)
	m := NewGraphMetrics(g)

	if keys := g.NoteKeysByTitle["foo"]; len(keys) != 2 {
		t.Fatalf("NoteKeysByTitle[foo] = %v, want 2 keys", keys)
	}

	// An ambiguous title links to every matching note, so the hub has degree 2
	// and the three notes form one component.
	if got := m.Degree("hub"); got != 2 {
		t.Fatalf("Degree(hub) = %d, want 2", got)
	}
	want := [][]string{{"areas/foo", "foo", "hub"}}
	if got := m.Components(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Components() = %v, want %v", got, want)
	}
	if got := m.Distances("hub", DirOut, -1); !reflect.DeepEqual(got, map[string]int{"hub": 0, "areas/foo": 1, "foo": 1}) {
		t.Fatalf("Distances(hub) = %v", got)
	}
	if got := m.ShortestPath("areas/foo", "foo", DirBoth); !reflect.DeepEqual(got, []string{"areas/foo", "hub", "foo"}) {
		t.Fatalf("ShortestPath(areas/foo, foo, both) = %v, want [areas/foo hub foo]", got)
	}
}

func TestGraphMetrics_Deterministic(t *testing.T) {
	root := t.TempDir()
	rels := []string{"A.md", "B.md", "C.md", "D.md", "E.md", "F.md", "G.md"}
	bodies := map[string]string{
		"A.md": "---\ntitle: A\n---\n[[B]] [[C]] [[Nowhere]]\n",
		"B.md": "---\ntitle: B\n---\n[[D]]\n",
		"C.md": "---\ntitle: C\n---\n[[D]]\n",
		"D.md": "---\ntitle: D\n---\n[[D]]\n",
		"E.md": "---\ntitle: E\n---\nnone\n",
		"F.md": "---\ntitle: F\n---\n[[G]]\n",
		"G.md": "---\ntitle: G\n---\nnone\n",
	}
	for _, rel := range rels {
		writeNote(t, root, rel, bodies[rel])
	}

	var first *GraphMetrics
	for i := range 8 {
		notes := make([]*vault.Note, 0, len(rels))
		// Vary the note order across builds; the result must not.
		for j := range rels {
			rel := rels[(j+i)%len(rels)]
			notes = append(notes, &vault.Note{Path: filepath.Join(root, rel)})
		}
		m := NewGraphMetrics(BuildGraph(notes))
		if first == nil {
			first = m
			continue
		}
		if !reflect.DeepEqual(m.Components(), first.Components()) {
			t.Fatalf("build %d: Components() = %v, want %v", i, m.Components(), first.Components())
		}
		for _, key := range []string{"a", "b", "c", "d", "e", "f", "g"} {
			if m.Degree(key) != first.Degree(key) {
				t.Fatalf("build %d: Degree(%q) drifted", i, key)
			}
			if m.ComponentSize(key) != first.ComponentSize(key) {
				t.Fatalf("build %d: ComponentSize(%q) drifted", i, key)
			}
			for _, dir := range []Direction{DirOut, DirIn, DirBoth} {
				if !reflect.DeepEqual(m.Distances(key, dir, -1), first.Distances(key, dir, -1)) {
					t.Fatalf("build %d: Distances(%q, %d) drifted", i, key, dir)
				}
				if !reflect.DeepEqual(m.ShortestPath(key, "d", dir), first.ShortestPath(key, "d", dir)) {
					t.Fatalf("build %d: ShortestPath(%q, d, %d) drifted", i, key, dir)
				}
			}
		}
	}
}

func TestGraphMetrics_ComponentsReturnsCopies(t *testing.T) {
	_, m := metricsFixture(t)

	got := m.Components()
	if len(got) == 0 || len(got[0]) == 0 {
		t.Fatalf("Components() = %v, want non-empty", got)
	}
	got[0][0] = "mutated"
	if again := m.Components(); again[0][0] == "mutated" {
		t.Fatalf("Components() leaked its internal slice")
	}
}

func TestGraphMetrics_NilInputs(t *testing.T) {
	m := NewGraphMetrics(nil)
	if m == nil {
		t.Fatal("NewGraphMetrics(nil) = nil, want empty metrics")
	}
	if got := m.Components(); got != nil {
		t.Errorf("Components() = %v, want nil", got)
	}
	if got := m.Degree("a"); got != 0 {
		t.Errorf("Degree(a) = %d, want 0", got)
	}

	var nilMetrics *GraphMetrics
	if got := nilMetrics.Degree("a"); got != 0 {
		t.Errorf("nil Degree = %d, want 0", got)
	}
	if got := nilMetrics.ComponentSize("a"); got != 0 {
		t.Errorf("nil ComponentSize = %d, want 0", got)
	}
	if got := nilMetrics.Components(); got != nil {
		t.Errorf("nil Components = %v, want nil", got)
	}
	if got := nilMetrics.Distances("a", DirBoth, -1); got != nil {
		t.Errorf("nil Distances = %v, want nil", got)
	}
	if _, ok := nilMetrics.Distance("a", "b", DirBoth, -1); ok {
		t.Errorf("nil Distance reported reachable")
	}
	if got := nilMetrics.ShortestPath("a", "b", DirBoth); got != nil {
		t.Errorf("nil ShortestPath = %v, want nil", got)
	}
}

func TestDirectionReverse(t *testing.T) {
	for dir, want := range map[Direction]Direction{
		DirOut:  DirIn,
		DirIn:   DirOut,
		DirBoth: DirBoth,
	} {
		if got := dir.reverse(); got != want {
			t.Errorf("Direction(%d).reverse() = %d, want %d", dir, got, want)
		}
	}
}
