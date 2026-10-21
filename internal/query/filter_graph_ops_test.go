package query

import (
	"sort"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/vault"
)

// graphOpsFixture builds the shared vault every graph-op test scans.
//
// Forward edges (by note key):
//
//	hub        -> alpha, beta
//	alpha      -> gamma
//	extra      -> hub
//	twins/twin -> beta
//	other/twin -> gamma
//	seeker     -> twins/twin, other/twin   (ambiguous [[Twin]] fans out to both)
//	solo       -> (none, and nothing links to it)
//
// Undirected, that is one component of nine notes plus solo alone.
func graphOpsFixture(t *testing.T) (*Graph, *GraphMetrics, map[string]*vault.Note) {
	t.Helper()
	_, notes := newVault(t, map[string]string{
		"Hub.md":        "Links [[Alpha]] and [[Beta]].\n",
		"Alpha.md":      "Links [[Gamma]].\n",
		"Beta.md":       "No links.\n",
		"Gamma.md":      "No links.\n",
		"Extra.md":      "Links [[Hub]].\n",
		"Solo.md":       "Isolated note.\n",
		"Seeker.md":     "Links [[Twin]].\n",
		"Twins/Twin.md": "Links [[Beta]].\n",
		"Other/Twin.md": "Links [[Gamma]].\n",
	})

	all := make([]*vault.Note, 0, len(notes))
	for _, n := range notes {
		all = append(all, n)
	}
	g := BuildGraph(all)
	return g, NewGraphMetrics(g), notes
}

// matchedKeys returns the sorted graph keys of every fixture note p matches.
func matchedKeys(g *Graph, notes map[string]*vault.Note, p Predicate) []string {
	var keys []string
	for _, n := range notes {
		if p(n) {
			keys = append(keys, g.KeyForNote(n))
		}
	}
	sort.Strings(keys)
	return keys
}

func assertKeys(t *testing.T, name string, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s: got %v, want %v", name, got, want)
	}
}

func TestLinksToAndLinkedFrom(t *testing.T) {
	g, _, notes := graphOpsFixture(t)

	cases := []struct {
		name string
		pred Predicate
		want []string
	}{
		{"links-to bare title", LinksTo(g, "Beta"), []string{"hub", "twins/twin"}},
		{"links-to is case- and alias-insensitive", LinksTo(g, "beta|Bee"), []string{"hub", "twins/twin"}},
		// Duplicate titles are any-of: Seeker's single [[Twin]] resolves to both.
		{"links-to ambiguous title", LinksTo(g, "Twin"), []string{"seeker"}},
		{"links-to folder-qualified", LinksTo(g, "Twins/Twin"), []string{"seeker"}},
		{"links-to unresolvable target", LinksTo(g, "Nonexistent"), nil},
		{"links-to empty target", LinksTo(g, ""), nil},
		{"linked-from", LinkedFrom(g, "Hub"), []string{"alpha", "beta"}},
		{"linked-from unions ambiguous sources", LinkedFrom(g, "Twin"), []string{"beta", "gamma"}},
		{"linked-from leaf", LinkedFrom(g, "Beta"), nil},
		{"linked-from unresolvable target", LinkedFrom(g, "Nonexistent"), nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertKeys(t, tc.name, matchedKeys(g, notes, tc.pred), tc.want)
		})
	}
}

// TestLinksToIgnoresSelfLinks pins that a note linking only to itself does not
// match LinksTo on its own title, because Graph.Forward excludes self-edges.
func TestLinksToIgnoresSelfLinks(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"SelfOnly.md": "I link to [[SelfOnly]] only.\n",
	})
	g := BuildGraph([]*vault.Note{notes["SelfOnly.md"]})
	if LinksTo(g, "SelfOnly")(notes["SelfOnly.md"]) {
		t.Errorf("a self-link should not satisfy links-to on the note's own title")
	}
}

func TestNearAndPathTo(t *testing.T) {
	g, m, notes := graphOpsFixture(t)

	everything := []string{"alpha", "beta", "extra", "gamma", "hub", "other/twin", "seeker", "twins/twin"}

	cases := []struct {
		name string
		pred Predicate
		want []string
	}{
		// The anchor is at distance 0, so it matches any non-negative bound.
		{"near zero hops is the anchor", Near(g, m, "Hub", DirBoth, 0), []string{"hub"}},
		{"near one hop", Near(g, m, "Hub", DirBoth, 1), []string{"alpha", "beta", "extra", "hub"}},
		{"near two hops", Near(g, m, "Hub", DirBoth, 2), []string{"alpha", "beta", "extra", "gamma", "hub", "twins/twin"}},
		{"near unbounded covers the component", Near(g, m, "Hub", DirBoth, -1), everything},
		// DirOut reads as "the candidate reaches the target by its own links".
		{"near outgoing", Near(g, m, "Hub", DirOut, 2), []string{"extra", "hub"}},
		{"near incoming", Near(g, m, "Hub", DirIn, 1), []string{"alpha", "beta", "hub"}},
		{"near an isolated note", Near(g, m, "Solo", DirBoth, 3), []string{"solo"}},
		{"near ambiguous title unions anchors", Near(g, m, "Twin", DirBoth, 0), []string{"other/twin", "twins/twin"}},
		{"near unresolvable target", Near(g, m, "Nonexistent", DirBoth, -1), nil},
		{"path-to outgoing", PathTo(g, m, "Gamma", DirOut), []string{"alpha", "extra", "gamma", "hub", "other/twin", "seeker"}},
		// DirIn is the mirror: the notes the target itself reaches.
		{"path-to incoming", PathTo(g, m, "Hub", DirIn), []string{"alpha", "beta", "gamma", "hub"}},
		{"path-to incoming from a sink", PathTo(g, m, "Gamma", DirIn), []string{"gamma"}},
		{"path-to undirected covers the component", PathTo(g, m, "Gamma", DirBoth), everything},
		{"path-to never crosses components", PathTo(g, m, "Solo", DirBoth), []string{"solo"}},
		{"path-to unresolvable target", PathTo(g, m, "Nonexistent", DirBoth), nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertKeys(t, tc.name, matchedKeys(g, notes, tc.pred), tc.want)
		})
	}
}

func TestClusterSizeAndDegree(t *testing.T) {
	g, m, notes := graphOpsFixture(t)

	cases := []struct {
		name string
		pred Predicate
		want []string
	}{
		// An isolated note is a component of size 1, so small-cluster queries
		// surface orphans too.
		{"cluster-size <=1 is the isolated note", ClusterSize(g, m, "<=", 1), []string{"solo"}},
		{"cluster-size =8 is the main component", ClusterSize(g, m, "=", 8), []string{"alpha", "beta", "extra", "gamma", "hub", "other/twin", "seeker", "twins/twin"}},
		{"cluster-size >=2 excludes the isolate", ClusterSize(g, m, ">=", 2), []string{"alpha", "beta", "extra", "gamma", "hub", "other/twin", "seeker", "twins/twin"}},
		{"cluster-size unknown operator matches nothing", ClusterSize(g, m, "~", 8), nil},
		{"degree =0 is the isolated note", Degree(g, m, "=", 0), []string{"solo"}},
		{"degree =1 is the leaf", Degree(g, m, "=", 1), []string{"extra"}},
		{"degree >=3 is the hub", Degree(g, m, ">=", 3), []string{"hub"}},
		{"degree !=2 is the hub and the leaf and the isolate", Degree(g, m, "!=", 2), []string{"extra", "hub", "solo"}},
		{"degree unknown operator matches nothing", Degree(g, m, "?", 0), nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertKeys(t, tc.name, matchedKeys(g, notes, tc.pred), tc.want)
		})
	}
}

// TestGraphOpsRejectNilNotes covers the nil-note contract every Predicate holds.
func TestGraphOpsRejectNilNotes(t *testing.T) {
	g, m, _ := graphOpsFixture(t)

	preds := map[string]Predicate{
		"LinksTo":     LinksTo(g, "Hub"),
		"LinkedFrom":  LinkedFrom(g, "Hub"),
		"Near":        Near(g, m, "Hub", DirBoth, -1),
		"PathTo":      PathTo(g, m, "Hub", DirBoth),
		"ClusterSize": ClusterSize(g, m, ">=", 0),
		"Degree":      Degree(g, m, ">=", 0),
	}
	for name, p := range preds {
		if p(nil) {
			t.Errorf("%s matched a nil note", name)
		}
	}
}

// TestGraphOpsResolveTargetsOnce pins the session-7 contract that a target is
// resolved (and its BFS run) when the predicate is built, not once per note: a
// predicate built against a graph keeps answering from that graph's structure
// even after the caller mutates the graph afterwards.
func TestGraphOpsResolveTargetsOnce(t *testing.T) {
	g, m, notes := graphOpsFixture(t)
	near := Near(g, m, "Hub", DirBoth, 1)
	if !near(notes["Alpha.md"]) {
		t.Fatalf("Alpha should be one hop from Hub")
	}

	delete(g.NoteKeysByTitle, "hub")
	if !near(notes["Alpha.md"]) {
		t.Errorf("predicate re-resolved its target per note instead of once at build time")
	}
}

func TestCompareInt(t *testing.T) {
	cases := []struct {
		got  int
		op   string
		want int
		ok   bool
	}{
		{3, "=", 3, true},
		{3, "==", 3, true},
		{3, "=", 4, false},
		{3, "!=", 4, true},
		{3, "!=", 3, false},
		{3, "<", 4, true},
		{4, "<", 4, false},
		{4, "<=", 4, true},
		{5, ">", 4, true},
		{4, ">", 4, false},
		{4, ">=", 4, true},
		{3, ">=", 4, false},
		{3, "", 3, false},
		{3, "=>", 3, false},
	}
	for _, tc := range cases {
		if got := compareInt(tc.got, tc.op, tc.want); got != tc.ok {
			t.Errorf("compareInt(%d, %q, %d): got %t, want %t", tc.got, tc.op, tc.want, got, tc.ok)
		}
	}
}
