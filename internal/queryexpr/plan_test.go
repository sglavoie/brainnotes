package queryexpr

import (
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
)

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
