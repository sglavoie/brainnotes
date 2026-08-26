package queryexpr

import (
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

func TestResolveViewCompilesEveryField(t *testing.T) {
	_, _, env := fixtureVault(t)

	view := ResolveView(config.SavedView{
		Name:       "Active work",
		Query:      "status:wip and not folder:Archive",
		Sort:       "-modified",
		Limit:      20,
		RelativeTo: "",
	}, env)

	if !view.Enabled() {
		t.Fatalf("view should be enabled, got %v", view.Err)
	}
	if view.Detail() != "" {
		t.Fatalf("an enabled view has no detail, got %q", view.Detail())
	}
	if view.Name() != "Active work" {
		t.Fatalf("Name: got %q", view.Name())
	}
	if want := (SortSpec{Key: SortModified, Descending: true}); view.Plan.Sort != want {
		t.Fatalf("Sort: got %+v, want %+v", view.Plan.Sort, want)
	}
	if view.Plan.Limit != 20 {
		t.Fatalf("Limit: got %d, want 20", view.Plan.Limit)
	}
	if view.Plan.Source != "status:wip and not folder:Archive" {
		t.Fatalf("Source: got %q", view.Plan.Source)
	}
}

// TestResolveViewBindsRelativeToFromItsOwnField pins why RelativeTo is a
// separate config key: SortSpec.String() drops it, so it could never survive a
// round trip through the sort text.
func TestResolveViewBindsRelativeToFromItsOwnField(t *testing.T) {
	_, _, env := fixtureVault(t)

	view := ResolveView(config.SavedView{
		Name:       "Hub neighbourhood",
		Query:      "status:wip",
		Sort:       "relevance",
		RelativeTo: "Hub",
	}, env)
	if !view.Enabled() {
		t.Fatalf("view should be enabled, got %v", view.Err)
	}
	if view.Plan.Sort.Key != SortRelevance || view.Plan.Sort.RelativeTo != "Hub" {
		t.Fatalf("Sort: got %+v", view.Plan.Sort)
	}
}

// TestResolveViewDisablesRatherThanFails is the session-11 contract: config
// cannot check a saved expression, so a view whose query rotted must come back
// disabled with its error attached — never dropped, never fatal.
func TestResolveViewDisablesRatherThanFails(t *testing.T) {
	_, _, env := fixtureVault(t)

	cases := []struct {
		name       string
		saved      config.SavedView
		wantDetail string
		wantParse  bool
	}{
		{
			name:       "unknown field",
			saved:      config.SavedView{Name: "Rotted", Query: "backlnks>3"},
			wantDetail: "unknown field",
			wantParse:  true,
		},
		{
			name:       "syntax error",
			saved:      config.SavedView{Name: "Broken", Query: "status:wip and"},
			wantDetail: "query:",
			wantParse:  true,
		},
		{
			name:       "unknown sort key",
			saved:      config.SavedView{Name: "Odd sort", Query: "status:wip", Sort: "nonsense"},
			wantDetail: "nonsense",
		},
		{
			name:       "relevance without an anchor",
			saved:      config.SavedView{Name: "Anchorless", Query: "status:wip", Sort: "relevance"},
			wantDetail: "requires relative_to",
		},
		{
			name:       "anchor without relevance",
			saved:      config.SavedView{Name: "Stray anchor", Query: "status:wip", Sort: "title", RelativeTo: "Hub"},
			wantDetail: `relative_to requires sort "relevance"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := ResolveView(tc.saved, env)
			if view.Enabled() {
				t.Fatal("view should be disabled")
			}
			if view.Plan != nil {
				t.Fatalf("a disabled view carries no plan, got %+v", view.Plan)
			}
			if view.Saved != tc.saved {
				t.Fatalf("the saved view must come back verbatim: %+v", view.Saved)
			}
			if !strings.Contains(view.Detail(), tc.wantDetail) {
				t.Fatalf("detail %q should contain %q", view.Detail(), tc.wantDetail)
			}
			_, isParse := view.Err.(*ParseError)
			if isParse != tc.wantParse {
				t.Fatalf("*ParseError = %v, want %v (err %T)", isParse, tc.wantParse, view.Err)
			}
			if tc.wantParse && !strings.Contains(view.Detail(), "^") {
				t.Fatalf("a parse failure must render carets so the user sees what rotted:\n%s", view.Detail())
			}
		})
	}
}

// TestResolveViewsKeepsEveryEntryInOrder is the other half of "never silently
// dropped": one bad view in the middle must not shorten the list or reorder it.
func TestResolveViewsKeepsEveryEntryInOrder(t *testing.T) {
	_, _, env := fixtureVault(t)

	saved := []config.SavedView{
		{Name: "First", Query: "status:wip"},
		{Name: "Rotted", Query: "backlnks>3"},
		{Name: "Last", Query: "tag:alpha"},
	}
	views := ResolveViews(saved, env)
	if len(views) != len(saved) {
		t.Fatalf("got %d resolved views, want %d", len(views), len(saved))
	}
	for i, view := range views {
		if view.Name() != saved[i].Name {
			t.Fatalf("view %d: got %q, want %q", i, view.Name(), saved[i].Name)
		}
	}
	if !views[0].Enabled() || views[1].Enabled() || !views[2].Enabled() {
		t.Fatalf("enabled flags: %v %v %v", views[0].Enabled(), views[1].Enabled(), views[2].Enabled())
	}

	if got := ResolveViews(nil, env); got != nil {
		t.Fatalf("ResolveViews(nil): got %v", got)
	}
}

// TestResolveViewCompilesWithoutAGraph keeps saved views cheap to list: a view
// naming a graph field must resolve against an Env with no graph, reporting the
// need through Plan.NeedsGraph instead.
func TestResolveViewCompilesWithoutAGraph(t *testing.T) {
	_, _, env := fixtureVault(t)
	env.Graph = nil

	view := ResolveView(config.SavedView{Name: "Neighbours", Query: "backlinks>1"}, env)
	if !view.Enabled() {
		t.Fatalf("view should be enabled, got %v", view.Err)
	}
	if !view.Plan.NeedsGraph() {
		t.Fatal("a backlinks view must report that it needs the graph")
	}
}
