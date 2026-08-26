package queryexpr

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// fixtureVault builds the vault every lowering test scans, mirroring the
// newVault helper in internal/query's tests.
//
// Forward edges (by graph key):
//
//	hub   -> alpha, beta
//	alpha -> gamma
//	gamma -> (nowhere: an unresolved link)
//
// so backlink counts are hub 0, alpha 1, beta 1, gamma 1, archive/old 0, solo 0,
// and outgoing counts are hub 2, alpha 1, and 0 for everything else.
func fixtureVault(t *testing.T) (string, []*vault.Note, Env) {
	t.Helper()

	today := time.Now().Format("2006-01-02")
	files := map[string]string{
		"Hub.md": "---\ntags: [alpha, beta]\nstatus: wip\ntype: note\nlast: 2000-01-01\n---\nHub links [[Alpha]] and [[Beta]].\n",
		"Alpha.md": "---\ntags: [alpha]\nstatus: done\ncategories: \"[[Books]]\"\nlast: " + today + "\n---\n" +
			"Alpha links [[Gamma]] and mentions widgets.\n",
		"Beta.md":  "---\ntags: [beta]\npinned: true\nrating: 4\n---\nBeta has no links.\n",
		"Gamma.md": "---\nstatus: wip\n---\nGamma links [[Nowhere At All]].\n",
		"Archive/Old.md": "---\ntags: [beta]\nstatus: done\nlast: 2000-01-01\n---\n" +
			"An archived note about widgets.\n",
		"Solo.md": "---\ntype: note\n---\nSolo is isolated.\n",
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

	env := Env{
		Root: v.Root,
		Frontmatter: &config.FrontmatterConfig{Properties: map[string]config.PropertyDefinition{
			"tags":   {Type: config.KindList, Items: config.KindString},
			"status": {Type: config.KindString},
			"rating": {Type: config.KindInt},
		}},
	}
	return v.Root, notes, env
}

// matchPaths compiles src and returns the sorted vault-relative paths it
// selects, building the graph exactly when the plan says one is needed.
func matchPaths(t *testing.T, src, root string, notes []*vault.Note, env Env) []string {
	t.Helper()
	plan, err := Compile(src, env)
	if err != nil {
		t.Fatalf("compile %q: %v", src, err)
	}
	if plan.NeedsGraph() {
		env.Graph = query.BuildGraphForRoot(root, notes)
	}
	pred, err := plan.Predicate(env)
	if err != nil {
		t.Fatalf("predicate %q: %v", src, err)
	}
	var out []string
	for _, n := range notes {
		if pred(n) {
			rel, err := filepath.Rel(root, n.Path)
			if err != nil {
				t.Fatalf("rel: %v", err)
			}
			out = append(out, filepath.ToSlash(rel))
		}
	}
	sort.Strings(out)
	return out
}

func TestCompile_ReservedFieldLowering(t *testing.T) {
	root, notes, env := fixtureVault(t)

	cases := []struct {
		src  string
		want []string
	}{
		// The identity: an empty query matches everything, like query.And().
		{"", []string{"Alpha.md", "Archive/Old.md", "Beta.md", "Gamma.md", "Hub.md", "Solo.md"}},

		// Frontmatter fields.
		{"tag:alpha", []string{"Alpha.md", "Hub.md"}},
		{"tag:[alpha, beta]", []string{"Alpha.md", "Archive/Old.md", "Beta.md", "Hub.md"}},
		{"tag:alpha and tag:beta", []string{"Hub.md"}},
		{"tag!=alpha", []string{"Archive/Old.md", "Beta.md", "Gamma.md", "Solo.md"}},
		{"category:books", []string{"Alpha.md"}},
		{"status:wip", []string{"Gamma.md", "Hub.md"}},
		{"status=done", []string{"Alpha.md", "Archive/Old.md"}},
		{"type:note", []string{"Hub.md", "Solo.md"}},
		{"has:rating", []string{"Beta.md"}},
		{"missing:status", []string{"Beta.md", "Solo.md"}},
		{"folder:Archive", []string{"Archive/Old.md"}},
		{"not folder:Archive", []string{"Alpha.md", "Beta.md", "Gamma.md", "Hub.md", "Solo.md"}},
		{"age:last>90d", []string{"Archive/Old.md", "Hub.md"}},
		{"age:last<90d", []string{"Alpha.md"}},

		// Boolean fields. A bare `orphan` is free text by the grammar, so the
		// spelling is orphan:true.
		{"orphan:true", []string{"Archive/Old.md", "Hub.md", "Solo.md"}},
		{"orphan:false", []string{"Alpha.md", "Beta.md", "Gamma.md"}},
		{"pinned:true", []string{"Beta.md"}},
		{"dead-end:true", []string{"Archive/Old.md", "Beta.md", "Gamma.md", "Solo.md"}},
		// Alpha's categories: "[[Books]]" is an unresolved frontmatter link,
		// which query.UnresolvedLinks counts as broken just like a body link.
		{"broken-links:true", []string{"Alpha.md", "Gamma.md"}},

		// Link counts. `>` and `<` are strict.
		{"backlinks>0", []string{"Alpha.md", "Beta.md", "Gamma.md"}},
		{"backlinks>=1", []string{"Alpha.md", "Beta.md", "Gamma.md"}},
		{"backlinks=0", []string{"Archive/Old.md", "Hub.md", "Solo.md"}},
		{"backlinks<1", []string{"Archive/Old.md", "Hub.md", "Solo.md"}},
		{"backlinks!=0", []string{"Alpha.md", "Beta.md", "Gamma.md"}},
		{"links>1", []string{"Hub.md"}},
		{"links>=1", []string{"Alpha.md", "Hub.md"}},
		{"links<=0", []string{"Archive/Old.md", "Beta.md", "Gamma.md", "Solo.md"}},

		// Graph operators.
		{"links-to:[[Alpha]]", []string{"Hub.md"}},
		{"links-to(\"Alpha\")", []string{"Hub.md"}},
		{"linked-from:[[Hub]]", []string{"Alpha.md", "Beta.md"}},
		{"near:[[Hub]]", []string{"Alpha.md", "Beta.md", "Hub.md"}},
		{"near:[[Hub]]<=2", []string{"Alpha.md", "Beta.md", "Gamma.md", "Hub.md"}},
		{"near:[[Hub]]<2", []string{"Alpha.md", "Beta.md", "Hub.md"}},
		{"path-to:[[Hub]]", []string{"Alpha.md", "Beta.md", "Gamma.md", "Hub.md"}},
		{"degree=0", []string{"Archive/Old.md", "Solo.md"}},
		{"degree>=2", []string{"Alpha.md", "Hub.md"}},
		{"cluster-size>1", []string{"Alpha.md", "Beta.md", "Gamma.md", "Hub.md"}},
		{"cluster-size:1", []string{"Archive/Old.md", "Solo.md"}},

		// Bare words and phrases lower onto query.Text.
		{"widgets", []string{"Alpha.md", "Archive/Old.md"}},
		{"\"archived note\"", []string{"Archive/Old.md"}},
		{"widgets and not folder:Archive", []string{"Alpha.md"}},

		// Composition: or is reachable from the surface for the first time.
		{"tag:alpha or pinned:true", []string{"Alpha.md", "Beta.md", "Hub.md"}},
		{"(tag:alpha or tag:beta) and status:done", []string{"Alpha.md", "Archive/Old.md"}},
		{"not (tag:alpha or tag:beta)", []string{"Gamma.md", "Solo.md"}},
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

// TestCompile_StrictBoundsMatchFlagSemantics pins the lowering the plan calls
// out by name: backlinks>3 must behave as MinBacklinks(g, 4), and no bound may
// reach the builders as the -1 that query.Flags reads as "filter disabled".
func TestCompile_StrictBoundsMatchFlagSemantics(t *testing.T) {
	root, notes, env := fixtureVault(t)
	graph := query.BuildGraphForRoot(root, notes)
	env.Graph = graph

	equivalents := []struct {
		src  string
		want query.Predicate
	}{
		{"backlinks>0", query.MinBacklinks(graph, 1)},
		{"backlinks>=1", query.MinBacklinks(graph, 1)},
		{"backlinks<2", query.MaxBacklinks(graph, 1)},
		{"backlinks<=1", query.MaxBacklinks(graph, 1)},
		{"links>1", query.MinLinks(graph, 2)},
		{"links<1", query.MaxLinks(graph, 0)},
	}
	for _, tc := range equivalents {
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
				t.Errorf("%q on %s = %v, want %v (the strict bound did not lower to the flag equivalent)", tc.src, n.Path, got, want)
			}
		}
	}

	// A bound that would be -1 collapses to a constant instead of reaching a
	// builder, where -1 is the sentinel meaning "not set".
	for _, tc := range []struct {
		src  string
		want int
	}{
		{"backlinks<0", 0},
		{"links<0", 0},
		{"backlinks>=0", len(notes)},
		{"links>=0", len(notes)},
	} {
		got := matchPaths(t, tc.src, root, notes, env)
		if len(got) != tc.want {
			t.Errorf("%q selected %d notes, want %d", tc.src, len(got), tc.want)
		}
	}
}

func TestPlan_NeedsGraph(t *testing.T) {
	_, _, env := fixtureVault(t)

	cases := []struct {
		src        string
		wantGraph  bool
		wantMetric bool
	}{
		{"", false, false},
		{"tag:alpha", false, false},
		{"status:wip and folder:Archive", false, false},
		{"pinned:true", false, false},
		{"age:last>30d", false, false},
		{"widgets", false, false},
		{"orphan:true", true, false},
		{"dead-end:true", true, false},
		{"broken-links:true", true, false},
		{"backlinks>3", true, false},
		{"links<=2", true, false},
		{"tag:alpha and not orphan:true", true, false},
		{"tag:alpha or (widgets and links-to:[[Hub]])", true, false},
		{"linked-from:[[Hub]]", true, false},
		{"near:[[Hub]]<=2", true, true},
		{"path-to:[[Hub]]", true, true},
		{"degree>=2", true, true},
		{"cluster-size<=3", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			plan, err := Compile(tc.src, env)
			if err != nil {
				t.Fatalf("compile %q: %v", tc.src, err)
			}
			if got := plan.NeedsGraph(); got != tc.wantGraph {
				t.Errorf("NeedsGraph(%q) = %v, want %v", tc.src, got, tc.wantGraph)
			}
			if got := plan.needsMetrics(); got != tc.wantMetric {
				t.Errorf("needsMetrics(%q) = %v, want %v", tc.src, got, tc.wantMetric)
			}
			if plan.needsMetrics() && !plan.NeedsGraph() {
				t.Errorf("%q needs metrics without needing a graph", tc.src)
			}
		})
	}
}

// TestPlan_NeedsGraphCoversEveryGraphBuilder guards the table against a new
// graph-reading field being added without its needsGraph bit, which would make
// a plan silently ask for a nil graph at evaluation time.
func TestPlan_NeedsGraphCoversEveryGraphBuilder(t *testing.T) {
	want := map[string]bool{
		"orphan": true, "dead-end": true, "broken-links": true,
		"backlinks": true, "links": true,
		"links-to": true, "linked-from": true, "near": true, "path-to": true,
		"degree": true, "cluster-size": true,
	}
	for _, name := range reservedFieldNames() {
		field, _ := lookupReserved(name)
		if field.needsGraph != want[name] {
			t.Errorf("reserved field %q has needsGraph=%v, want %v", name, field.needsGraph, want[name])
		}
		if field.needsMetrics && !field.needsGraph {
			t.Errorf("reserved field %q needs metrics without needing a graph", name)
		}
	}
}

// TestPlan_PredicateWithoutGraph pins that a plan needing vault state its Env
// lacks fails loudly rather than matching nothing.
func TestPlan_PredicateWithoutGraph(t *testing.T) {
	_, _, env := fixtureVault(t)

	for _, tc := range []struct {
		src   string
		env   Env
		field string
		need  string
	}{
		{src: "orphan:true", env: env, field: "orphan", need: "link graph"},
		{src: "near:[[Hub]]<=2", env: env, field: "near", need: "link graph"},
		{src: "folder:Archive", env: Env{}, field: "folder", need: "vault root path"},
	} {
		plan, err := Compile(tc.src, tc.env)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		_, err = plan.Predicate(tc.env)
		var missing *MissingStateError
		if !asMissingState(err, &missing) {
			t.Fatalf("Predicate(%q) error = %v, want *MissingStateError", tc.src, err)
		}
		if missing.Field != tc.field || missing.Need != tc.need {
			t.Errorf("Predicate(%q) = %v, want field %q needing %q", tc.src, missing, tc.field, tc.need)
		}
	}
}

// TestPlan_PredicateDerivesMetricsFromGraph pins that a caller supplying only
// the graph still gets the traversal fields working.
func TestPlan_PredicateDerivesMetricsFromGraph(t *testing.T) {
	root, notes, env := fixtureVault(t)
	env.Graph = query.BuildGraphForRoot(root, notes)
	env.Metrics = nil

	plan, err := Compile("degree>=2", env)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	pred, err := plan.Predicate(env)
	if err != nil {
		t.Fatalf("predicate: %v", err)
	}
	matched := 0
	for _, n := range notes {
		if pred(n) {
			matched++
		}
	}
	if matched != 2 {
		t.Fatalf("degree>=2 matched %d notes, want 2 (metrics were not derived from the graph)", matched)
	}
}

func TestCompile_UnknownFieldSuggestions(t *testing.T) {
	_, _, env := fixtureVault(t)

	cases := []struct {
		src         string
		wantSpan    string
		wantMsg     string
		wantSuggest []string
	}{
		{"backlnks>3", "backlnks", "unknown field \"backlnks\"", []string{"backlinks"}},
		{"tga:alpha", "tga", "unknown field \"tga\"", []string{"tag", "age", "tags"}},
		{"ratign>3", "ratign", "unknown field \"ratign\"", []string{"rating"}},
		{"statuz:wip", "statuz", "unknown field \"statuz\"", []string{"status"}},
		{"linkz-to:[[Hub]]", "linkz-to", "unknown field \"linkz-to\"", []string{"links-to"}},
		{"tag:alpha and dgree>2", "dgree", "unknown field \"dgree\"", []string{"degree"}},
		{"absolutelynothinglikeit:1", "absolutelynothinglikeit", "unknown field \"absolutelynothinglikeit\"", nil},
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
			// Carets point at the field name, not the whole term.
			if got := tc.src[perr.Span.Start:perr.Span.End]; got != tc.wantSpan {
				t.Errorf("span covers %q, want %q", got, tc.wantSpan)
			}
		})
	}
}

// TestCompile_SuggestionsAreCappedAndSorted pins the three properties the
// suggestion list promises: distance <= 2, at most three entries, and a
// deterministic order.
func TestCompile_SuggestionsAreCappedAndSorted(t *testing.T) {
	env := Env{Frontmatter: &config.FrontmatterConfig{Properties: map[string]config.PropertyDefinition{
		"tar": {Type: config.KindString},
		"tab": {Type: config.KindString},
		"tan": {Type: config.KindString},
		"tap": {Type: config.KindString},
	}}}

	_, err := Compile("tag2:x", env)
	perr, ok := err.(*ParseError)
	if !ok {
		t.Fatalf("error = %v (%T), want *ParseError", err, err)
	}
	if len(perr.Suggest) > 3 {
		t.Fatalf("suggestions = %v, want at most 3", perr.Suggest)
	}
	// Distance 1 (tag) sorts ahead of the distance-2 candidates, which are
	// themselves alphabetical.
	want := []string{"tag", "age", "tab"}
	if strings.Join(perr.Suggest, ",") != strings.Join(want, ",") {
		t.Fatalf("suggestions = %v, want %v", perr.Suggest, want)
	}
	// Map iteration order must not leak into the list: repeat the compile and
	// require the same answer every time.
	for i := 0; i < 20; i++ {
		_, again := Compile("tag2:x", env)
		repeat, ok := again.(*ParseError)
		if !ok {
			t.Fatalf("repeat %d: error = %v (%T), want *ParseError", i, again, again)
		}
		if strings.Join(repeat.Suggest, ",") != strings.Join(want, ",") {
			t.Fatalf("suggestions are not deterministic: %v on repeat %d, want %v", repeat.Suggest, i, want)
		}
	}
}

// TestCompile_DeclaredPropertyCompiles pins the middle step of field
// resolution: a property declared in brainnotes.toml is neither a typo nor an
// unimplemented stub, it is a typed comparison.
func TestCompile_DeclaredPropertyCompiles(t *testing.T) {
	_, _, env := fixtureVault(t)

	if _, err := Compile("rating>3", env); err != nil {
		t.Fatalf("Compile(%q) = %v, want a declared int property to compile", "rating>3", err)
	}
	// The reserved table still wins over a declared property of the same name,
	// and a declared property still never reaches the graph.
	plan, err := Compile("rating>3 and status:wip", env)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if plan.NeedsGraph() {
		t.Errorf("NeedsGraph() = true, want false for a frontmatter-only query")
	}
}

func TestCompile_TermErrors(t *testing.T) {
	_, _, env := fixtureVault(t)

	cases := []struct {
		src     string
		wantMsg string
	}{
		{"tag>alpha", "operator \">\" is not defined for field \"tag\""},
		{"status<=done", "operator \"<=\" is not defined for field \"status\""},
		{"backlinks>many", "field \"backlinks\" expects a whole number"},
		{"backlinks:[1, 2]", "field \"backlinks\" takes a single whole number"},
		{"age>=30d", "operator \">=\" is not defined for field \"age\""},
		{"age>30x", "invalid age unit"},
		{"age:last", "operator \":\" is not defined for field \"age\""},
		{"near:[[Hub]]>2", "operator \">\" is not defined for field \"near\""},
		{"orphan:maybe", "field \"orphan\" expects true or false"},
		{"tag(alpha)", "field \"tag\" takes no argument"},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			_, err := Compile(tc.src, env)
			perr, ok := err.(*ParseError)
			if !ok {
				t.Fatalf("Compile(%q) error = %v (%T), want *ParseError", tc.src, err, err)
			}
			if !strings.Contains(perr.Msg, tc.wantMsg) {
				t.Errorf("message = %q, want it to contain %q", perr.Msg, tc.wantMsg)
			}
			if perr.Column < 1 {
				t.Errorf("column = %d, want a 1-based column", perr.Column)
			}
		})
	}
}

// TestCompile_KeepsSourceForRendering pins that Plan.Source is what the user
// typed, so a later Predicate error can still render carets against it.
func TestCompile_KeepsSourceForRendering(t *testing.T) {
	_, _, env := fixtureVault(t)
	const src = "tag:alpha and status:wip"

	plan, err := Compile(src, env)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if plan.Source != src {
		t.Errorf("Source = %q, want %q", plan.Source, src)
	}
	if plan.Expr.String() != src {
		t.Errorf("Expr.String() = %q, want %q", plan.Expr.String(), src)
	}
	if plan.Limit != 0 || plan.Sort != (SortSpec{}) {
		t.Errorf("Compile set Sort/Limit (%+v, %d); they are caller-supplied", plan.Sort, plan.Limit)
	}
}

// TestCompile_FieldNamesAreCaseInsensitive pins the case policy this package
// chose: the parser keeps a field name verbatim, and lookup folds it.
func TestCompile_FieldNamesAreCaseInsensitive(t *testing.T) {
	root, notes, env := fixtureVault(t)
	lower := matchPaths(t, "tag:alpha", root, notes, env)
	upper := matchPaths(t, "TAG:alpha", root, notes, env)
	if strings.Join(lower, ",") != strings.Join(upper, ",") {
		t.Fatalf("TAG:alpha selected %v, want the same as tag:alpha (%v)", upper, lower)
	}
}

func TestDamerauLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"tag", "tag", 0},
		{"tag", "", 3},
		{"", "tag", 3},
		{"tga", "tag", 1}, // one transposition, not two edits
		{"backlnks", "backlinks", 1},
		{"statuz", "status", 1},
		{"near", "degree", 4},
		// One rune edit, not two bytes. The accented rune is escaped
		// deliberately: a literal here is easy to mis-normalize.
		{"café", "cafe", 1},
	}
	for _, tc := range cases {
		if got := damerauLevenshtein(tc.a, tc.b); got != tc.want {
			t.Errorf("damerauLevenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if got := damerauLevenshtein(tc.b, tc.a); got != tc.want {
			t.Errorf("damerauLevenshtein(%q, %q) = %d, want %d (not symmetric)", tc.b, tc.a, got, tc.want)
		}
	}
}

// asMissingState is errors.As without the import, kept local so the layering
// test's import inventory stays about package dependencies.
func asMissingState(err error, target **MissingStateError) bool {
	m, ok := err.(*MissingStateError)
	if !ok {
		return false
	}
	*target = m
	return true
}
