package web

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/queryexpr"
)

func TestViewSaveIncludesTextAndAllFacets(t *testing.T) {
	server, _, root := newViewsFixtureServer(t)
	defer server.Close()
	params := url.Values{"q": {"Alpha body"}, "tag": {"alpha", "shared"}, "status": {"draft"}, "expr": {"not tag:beta"}}
	preview := previewView(t, server.URL, viewMutationRequest{
		Op: viewOpUpsert, Name: "Full search", View: &viewDefinition{FilterParams: params.Encode(), Limit: 1},
	})
	status, result := applyView(t, server.URL, preview.PreviewID)
	if status != http.StatusOK {
		t.Fatalf("apply: %d", status)
	}
	saved := findViewSummary(t, result.Views, "Full search")
	if saved.Search != "Alpha body" || saved.Sort != "match" || saved.Limit != 1 {
		t.Fatalf("shape lost: %+v", saved)
	}
	plan := queryexpr.ResolveView(config.SavedView{Query: saved.Query, Search: saved.Search, Sort: saved.Sort}, queryexpr.Env{Root: root})
	if plan.Err != nil {
		t.Fatal(plan.Err)
	}
	for _, term := range []string{"tag:alpha", "tag:shared", "status:draft", "not tag:beta", `"Alpha body"`} {
		if !strings.Contains(plan.Plan.Expr.String(), term) {
			t.Fatalf("missing %q: %s", term, plan.Plan.Expr.String())
		}
	}
}

func TestViewSaveTextOnlyAndInvalidFilters(t *testing.T) {
	server, _, _ := newViewsFixtureServer(t)
	defer server.Close()
	preview := previewView(t, server.URL, viewMutationRequest{
		Op: viewOpUpsert, Name: "Text only", View: &viewDefinition{FilterParams: "q=Alpha"},
	})
	if !findViewSummary(t, preview.Views, "Text only").Enabled {
		t.Fatal("text-only view disabled")
	}
	status, _ := postViews(t, server.URL, viewsPreviewAPIRoutePath, propertyTestToken, viewMutationRequest{
		Op: viewOpUpsert, Name: "Invalid", View: &viewDefinition{FilterParams: "q=Alpha&min_links=oops"},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("invalid filter status: %d", status)
	}
}

func TestSavedMatchSortKeepsBrowserRanking(t *testing.T) {
	server, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Alpha.md":        []byte("Café mentioned only in the body."),
		"Z/Cafe\u0301.md": []byte("A title match."),
		"A/Café.md":       []byte("Another title match."),
	})
	var before, after []searchResult
	getJSON(t, server, "/api/search?q=Caf%C3%A9&limit=2", &before)
	getJSON(t, server, "/api/search?q=Caf%C3%A9&sort=match&limit=2", &after)
	if len(before) != 2 || before[0].Folder != "A" {
		t.Fatalf("unexpected default order: %+v", before)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("saved ordering changed results: before=%+v after=%+v", before, after)
	}
}
