package queryexpr

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/query"
	"github.com/sglavoie/brainnotes/internal/vault"
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
