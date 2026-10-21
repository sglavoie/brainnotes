package queryexpr

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/query"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// graphFixture builds a vault whose link structure is a chain long enough for
// hop bounds to be distinguishable, plus one isolated note.
//
// Forward edges (by graph key):
//
//	hub   -> alpha, beta
//	alpha -> gamma
//	gamma -> deep work
//	solo  -> (nothing, and nothing links to it)
//
// so undirected distances from hub are alpha 1, beta 1, gamma 2, deep work 3,
// and solo unreachable. "Deep Work" is deliberately a two-word title: it is the
// case a half-remembered target has to suggest.
func graphFixture(t *testing.T) (string, []*vault.Note, Env) {
	t.Helper()

	files := map[string]string{
		"Hub.md":       "---\nstatus: wip\n---\nHub links [[Alpha]] and [[Beta]].\n",
		"Alpha.md":     "---\nstatus: done\n---\nAlpha links [[Gamma]].\n",
		"Beta.md":      "---\nstatus: wip\n---\nBeta is a leaf.\n",
		"Gamma.md":     "---\nstatus: wip\n---\nGamma links [[Deep Work]].\n",
		"Deep Work.md": "---\nstatus: done\n---\nThe end of the chain.\n",
		"Solo.md":      "---\nstatus: wip\n---\nSolo is isolated.\n",
	}

	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir parent of %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	v, err := vault.Open(&config.Config{Vault: root})
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	notes, err := v.Notes()
	if err != nil {
		t.Fatalf("load notes: %v", err)
	}
	return v.Root, notes, Env{Root: v.Root}
}

// graphEnv is graphFixture with the graph and metrics already assembled, which
// is the shape a caller has after Plan.NeedsGraph said yes — and the shape that
// makes an unresolvable target a compile-time error rather than a build-time
// one.
func graphEnv(t *testing.T) (string, []*vault.Note, Env) {
	t.Helper()
	root, notes, env := graphFixture(t)
	env.Graph = query.BuildGraphForRoot(root, notes)
	env.Metrics = query.NewGraphMetrics(env.Graph)
	return root, notes, env
}

// TestGraphTerms_Lowering pins what each of the six graph terms selects, in
// both spellings the grammar allows.
func TestGraphTerms_Lowering(t *testing.T) {
	root, notes, env := graphFixture(t)

	cases := []struct {
		src  string
		want []string
	}{
		// links-to / linked-from: direct adjacency, both spellings.
		{"links-to:[[Alpha]]", []string{"Hub.md"}},
		{"links-to(\"Alpha\")", []string{"Hub.md"}},
		{"links-to:[[Deep Work]]", []string{"Gamma.md"}},
		{"links-to:[ [[Alpha]], [[Gamma]] ]", []string{"Alpha.md", "Hub.md"}},
		{"linked-from:[[Hub]]", []string{"Alpha.md", "Beta.md"}},
		{"linked-from(\"Alpha\")", []string{"Gamma.md"}},
		{"links-to!=[[Alpha]]", []string{"Alpha.md", "Beta.md", "Deep Work.md", "Gamma.md", "Solo.md"}},

		// near: the anchor is at distance 0 and always matches; `<` is strict.
		{"near:[[Hub]]", []string{"Alpha.md", "Beta.md", "Hub.md"}},
		{"near([[Hub]])", []string{"Alpha.md", "Beta.md", "Hub.md"}},
		{"near:[[Hub]]<=2", []string{"Alpha.md", "Beta.md", "Gamma.md", "Hub.md"}},
		{"near:[[Hub]]<2", []string{"Alpha.md", "Beta.md", "Hub.md"}},
		{"near:[[Hub]]<=3", []string{"Alpha.md", "Beta.md", "Deep Work.md", "Gamma.md", "Hub.md"}},
		{"near:[[Hub]]<=0", []string{"Hub.md"}},
		{"near:[[Solo]]<=5", []string{"Solo.md"}},

		// path-to: unbounded reachability, so the whole component but not Solo.
		{"path-to:[[Hub]]", []string{"Alpha.md", "Beta.md", "Deep Work.md", "Gamma.md", "Hub.md"}},
		{"path-to:[[Solo]]", []string{"Solo.md"}},
		{"path-to!=[[Hub]]", []string{"Solo.md"}},

		// degree and cluster-size read the precomputed tables, not a traversal.
		{"degree=0", []string{"Solo.md"}},
		{"degree:1", []string{"Beta.md", "Deep Work.md"}},
		{"degree>=2", []string{"Alpha.md", "Gamma.md", "Hub.md"}},
		{"cluster-size:1", []string{"Solo.md"}},
		{"cluster-size>1", []string{"Alpha.md", "Beta.md", "Deep Work.md", "Gamma.md", "Hub.md"}},

		// Composition with the rest of the vocabulary still works.
		{"near:[[Hub]]<=2 and status:wip", []string{"Beta.md", "Gamma.md", "Hub.md"}},
		{"links-to:[[Alpha]] or degree=0", []string{"Hub.md", "Solo.md"}},
		{"not path-to:[[Hub]]", []string{"Solo.md"}},
	}

	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			got := matchPaths(t, tc.src, root, notes, env)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("%q selected %v, want %v", tc.src, got, tc.want)
			}
		})
	}
}

// TestGraphTerms_MatchSession3Predicates pins that the memoized lowering agrees
// note-for-note with the internal/query builders it replaced. The memo changes
// how many traversals run, never which notes come back.
func TestGraphTerms_MatchSession3Predicates(t *testing.T) {
	_, notes, env := graphEnv(t)
	g, m := env.Graph, env.Metrics

	cases := []struct {
		src  string
		want query.Predicate
	}{
		{"near:[[Hub]]", query.Near(g, m, "Hub", query.DirBoth, 1)},
		{"near:[[Hub]]<=2", query.Near(g, m, "Hub", query.DirBoth, 2)},
		{"near:[[Hub]]<3", query.Near(g, m, "Hub", query.DirBoth, 2)},
		{"path-to:[[Hub]]", query.PathTo(g, m, "Hub", query.DirBoth)},
		{"path-to:[[Solo]]", query.PathTo(g, m, "Solo", query.DirBoth)},
	}
	for _, tc := range cases {
		plan, err := Compile(tc.src, env)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		pred, err := plan.Predicate(env)
		if err != nil {
			t.Fatalf("predicate %q: %v", tc.src, err)
		}
		for _, n := range notes {
			if got, want := pred(n), tc.want(n); got != want {
				t.Errorf("%q on %s = %v, want %v", tc.src, n.Path, got, want)
			}
		}
	}
}

// TestGraphTerms_OneBFSPerScan is the memoization contract: a near: term costs
// one traversal for the whole scan, not one per note, and terms sharing an
// anchor share that traversal.
func TestGraphTerms_OneBFSPerScan(t *testing.T) {
	_, notes, env := graphEnv(t)

	cases := []struct {
		src     string
		wantBFS int
	}{
		{"near:[[Hub]]<=2", 1},
		{"path-to:[[Hub]]", 1},
		// One anchor, three bounds: the memo holds unbounded distances, so
		// every bound is a filter over the same traversal.
		{"near:[[Hub]]<=1 or near:[[Hub]]<=3 or path-to:[[Hub]]", 1},
		{"near:[[Hub]] and not near:[[Hub]]<=0", 1},
		// Distinct anchors genuinely need distinct traversals.
		{"near:[[Hub]] or near:[[Gamma]]", 2},
		{"near:[[Hub]]<=2 or path-to:[[Solo]]", 2},
		// Fields that read adjacency or the precomputed tables run none at all.
		{"links-to:[[Alpha]] or linked-from:[[Hub]]", 0},
		{"degree>=2 and cluster-size>1", 0},
	}

	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			plan, err := Compile(tc.src, env)
			if err != nil {
				t.Fatalf("compile %q: %v", tc.src, err)
			}
			memo := newGraphMemo()
			scoped := env
			scoped.memo = memo
			pred, err := plan.Predicate(scoped)
			if err != nil {
				t.Fatalf("predicate %q: %v", tc.src, err)
			}
			if memo.runs != tc.wantBFS {
				t.Fatalf("%q ran %d BFS traversals while building, want %d", tc.src, memo.runs, tc.wantBFS)
			}
			// The scan itself must add none: the reachable set is computed
			// while the predicate is built, so evaluating N notes is N map
			// lookups rather than N traversals.
			built := memo.runs
			for _, n := range notes {
				pred(n)
			}
			if memo.runs != built {
				t.Fatalf("%q ran %d traversals during the scan of %d notes; the BFS is per-note, not per-scan",
					tc.src, memo.runs-built, len(notes))
			}
		})
	}
}

// TestGraphTerms_MemoIsPerPredicateNotOnMetrics pins where the cache lives. Two
// Predicate calls sharing one *GraphMetrics each run their own traversal, which
// is exactly what "the memo is in the closure, not on GraphMetrics" means: the
// shared value stays immutable, so it stays safe to read concurrently.
func TestGraphTerms_MemoIsPerPredicateNotOnMetrics(t *testing.T) {
	_, _, env := graphEnv(t)

	plan, err := Compile("near:[[Hub]]<=2", env)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for i := 0; i < 3; i++ {
		memo := newGraphMemo()
		scoped := env
		scoped.memo = memo
		if _, err := plan.Predicate(scoped); err != nil {
			t.Fatalf("predicate %d: %v", i, err)
		}
		if memo.runs != 1 {
			t.Fatalf("call %d ran %d traversals, want 1 (a cache on GraphMetrics would make later calls run 0)", i, memo.runs)
		}
	}

	// A Predicate call that is given no memo still installs one rather than
	// traversing per anchor per term.
	plain, err := plan.Predicate(env)
	if err != nil {
		t.Fatalf("predicate without memo: %v", err)
	}
	if plain == nil {
		t.Fatal("Predicate returned a nil predicate")
	}
	if env.memo != nil {
		t.Error("Plan.Predicate mutated the caller's Env; the memo must stay local to the call")
	}
}

// TestGraphTerms_ConcurrentPredicatesShareMetrics pins the race-free claim: one
// *GraphMetrics, many simultaneous evaluations, identical answers. Nothing here
// writes to the shared value, so `go test -race` has nothing to find.
func TestGraphTerms_ConcurrentPredicatesShareMetrics(t *testing.T) {
	_, notes, env := graphEnv(t)

	plan, err := Compile("near:[[Hub]]<=2 or path-to:[[Solo]]", env)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	const workers = 8
	results := make([]string, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pred, err := plan.Predicate(env)
			if err != nil {
				results[i] = "error: " + err.Error()
				return
			}
			var matched []string
			for _, n := range notes {
				if pred(n) {
					matched = append(matched, filepath.Base(n.Path))
				}
			}
			results[i] = strings.Join(matched, ",")
		}(i)
	}
	wg.Wait()
	for i, got := range results {
		if got != results[0] {
			t.Fatalf("worker %d saw %q, worker 0 saw %q", i, got, results[0])
		}
	}
	if results[0] == "" || strings.HasPrefix(results[0], "error:") {
		t.Fatalf("concurrent evaluation produced %q", results[0])
	}
}

// TestGraphTerms_UnresolvableTargetIsCompileError is the third contract: a
// target naming no note is a *ParseError with did-you-mean, never a silently
// empty result.
func TestGraphTerms_UnresolvableTargetIsCompileError(t *testing.T) {
	_, _, env := graphEnv(t)

	cases := []struct {
		src         string
		wantSpan    string
		wantMsg     string
		wantSuggest []string
	}{
		{"links-to:[[Hubb]]", "[[Hubb]]", "no note matches target \"Hubb\"", []string{"Hub"}},
		{"links-to(\"Hubb\")", "\"Hubb\"", "no note matches target \"Hubb\"", []string{"Hub"}},
		{"linked-from:[[Alfa]]", "[[Alfa]]", "no note matches target \"Alfa\"", []string{"Alpha"}},
		{"near:[[Hubb]]<=2", "[[Hubb]]", "no note matches target \"Hubb\"", []string{"Hub"}},
		{"near:[[Hubb]]", "[[Hubb]]", "no note matches target \"Hubb\"", []string{"Hub"}},
		{"path-to:[[Hubb]]", "[[Hubb]]", "no note matches target \"Hubb\"", []string{"Hub"}},
		// Negation must not swallow the diagnostic.
		{"not links-to:[[Hubb]]", "[[Hubb]]", "no note matches target \"Hubb\"", []string{"Hub"}},
		{"status:wip and near:[[Hubb]]<=2", "[[Hubb]]", "no note matches target \"Hubb\"", []string{"Hub"}},
		// One bad element of a list is enough.
		{"links-to:[ [[Hub]], [[Gamm]] ]", "[[Gamm]]", "no note matches target \"Gamm\"", []string{"Gamma"}},
		// A half-remembered title is reachable by containment, not by edit
		// distance: "work" is five edits from "deep work".
		{"links-to:[[Work]]", "[[Work]]", "no note matches target \"Work\"", []string{"Deep Work"}},
		// Nothing close enough: the error stands on its own.
		{"links-to:[[Zzzzzzzzzz]]", "[[Zzzzzzzzzz]]", "no note matches target \"Zzzzzzzzzz\"", nil},
		// A negative radius still validates its target first.
		{"near:[[Hubb]]<0", "[[Hubb]]", "no note matches target \"Hubb\"", []string{"Hub"}},
	}

	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			_, err := Compile(tc.src, env)
			perr, ok := err.(*ParseError)
			if !ok {
				t.Fatalf("Compile(%q) error = %v (%T), want *ParseError", tc.src, err, err)
			}
			if perr.Msg != tc.wantMsg {
				t.Errorf("message = %q, want %q", perr.Msg, tc.wantMsg)
			}
			if strings.Join(perr.Suggest, ",") != strings.Join(tc.wantSuggest, ",") {
				t.Errorf("suggestions = %v, want %v", perr.Suggest, tc.wantSuggest)
			}
			if got := tc.src[perr.Span.Start:perr.Span.End]; got != tc.wantSpan {
				t.Errorf("span covers %q, want %q", got, tc.wantSpan)
			}
			if perr.Column < 1 {
				t.Errorf("column = %d, want a 1-based column", perr.Column)
			}
			if !strings.Contains(perr.Render(), "no note matches target") {
				t.Errorf("Render() = %q, want it to carry the message", perr.Render())
			}
		})
	}
}

// TestGraphTerms_UnresolvableTargetDefersWithoutGraph pins that compiling
// without a graph — the NeedsGraph-then-build path every caller uses — moves the
// diagnostic to Plan.Predicate rather than losing it.
func TestGraphTerms_UnresolvableTargetDefersWithoutGraph(t *testing.T) {
	root, notes, env := graphFixture(t)

	for _, src := range []string{"links-to:[[Hubb]]", "near:[[Hubb]]<=2", "path-to:[[Hubb]]", "linked-from:[[Hubb]]"} {
		t.Run(src, func(t *testing.T) {
			plan, err := Compile(src, env)
			if err != nil {
				t.Fatalf("Compile(%q) = %v, want a graph-free compile to succeed", src, err)
			}
			if !plan.NeedsGraph() {
				t.Fatalf("NeedsGraph(%q) = false", src)
			}
			withGraph := env
			withGraph.Graph = query.BuildGraphForRoot(root, notes)
			_, err = plan.Predicate(withGraph)
			perr, ok := err.(*ParseError)
			if !ok {
				t.Fatalf("Predicate(%q) error = %v (%T), want *ParseError", src, err, err)
			}
			if perr.Msg != "no note matches target \"Hubb\"" {
				t.Errorf("message = %q", perr.Msg)
			}
			if strings.Join(perr.Suggest, ",") != "Hub" {
				t.Errorf("suggestions = %v, want [Hub]", perr.Suggest)
			}
		})
	}
}

// TestGraphTerms_ResolvableTargetsNeverError guards the diagnostic against
// over-firing: every spelling that does name a note must compile.
func TestGraphTerms_ResolvableTargetsNeverError(t *testing.T) {
	_, _, env := graphEnv(t)

	for _, src := range []string{
		"links-to:[[Hub]]",
		"links-to:[[hub]]",       // title matching is case-insensitive
		"links-to:[[Hub.md]]",    // a .md suffix is stripped by link resolution
		"links-to:[[Hub#Notes]]", // heading and alias suffixes resolve fine
		"links-to:[[Hub|the hub]]",
		"links-to(\"Hub\")",
		"linked-from:[[Deep Work]]",
		"near:[[Deep Work]]<=2",
		"path-to:[[Deep Work]]",
	} {
		if _, err := Compile(src, env); err != nil {
			t.Errorf("Compile(%q) = %v, want a resolvable target to compile", src, err)
		}
	}
}

func TestTargetProbe(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Deep Work", "deep work"},
		{"Areas/Deep Work", "deep work"},
		{"Areas\\Deep Work", "deep work"},
		{"Deep Work#Section", "deep work"},
		{"Deep Work|alias", "deep work"},
		{"Deep Work.md", "deep work"},
		{"  Hub  ", "hub"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := targetProbe(tc.in); got != tc.want {
			t.Errorf("targetProbe(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestGraphFieldsTableIsDisjoint pins the merge withGraphFields performs: the
// two halves of the reserved vocabulary must not overlap, or which lowering wins
// would depend on map order.
func TestGraphFieldsTableIsDisjoint(t *testing.T) {
	want := []string{"cluster-size", "degree", "linked-from", "links-to", "near", "path-to"}
	if len(graphFields) != len(want) {
		t.Fatalf("graphFields has %d entries, want %d", len(graphFields), len(want))
	}
	for _, name := range want {
		field, ok := graphFields[name]
		if !ok {
			t.Fatalf("graphFields is missing %q", name)
		}
		if !field.needsGraph {
			t.Errorf("graph field %q does not declare needsGraph", name)
		}
		if field.name != name {
			t.Errorf("graph field keyed %q carries name %q", name, field.name)
		}
		merged, ok := lookupReserved(name)
		if !ok || merged.name != name {
			t.Errorf("graph field %q did not reach reservedFields", name)
		}
	}
}
