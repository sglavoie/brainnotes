package web

import (
	"fmt"
	"net/url"
	"testing"
)

func TestSearchSelectionRechecksTheLimitedQueryAcrossPages(t *testing.T) {
	candidates := make([]searchCandidate, 80)
	for i := range candidates {
		candidates[i].result.Key = fmt.Sprintf("note-%02d", i)
	}
	got := limitSearchCandidates(candidates, resultShape{Limit: 65, Offset: 50, Selected: []string{"note-01", "note-60", "note-70", "missing"}})
	if len(got) != 2 || got[0].result.Key != "note-01" || got[1].result.Key != "note-60" || got[1].result.Position != 61 {
		t.Fatalf("selection = %#v", got)
	}
	params := url.Values{}
	for i := 0; i < 51; i++ {
		params.Add("selected", fmt.Sprint(i))
	}
	if _, err := parseResultShape(params); err == nil {
		t.Fatal("accepted an unbounded selection")
	}
}

func TestSearchSelectionAPIHonorsFiltersAndLimit(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Alpha.md": []byte("---\nstatus: draft\n---\nneedle\n"),
		"Beta.md":  []byte("---\nstatus: done\n---\nneedle\n"),
		"Gamma.md": []byte("---\nstatus: draft\n---\nneedle\n"),
	})
	results, _, status := fetchSearchJSON(t, ts.URL+"/api/search?q=needle&status=draft&sort=title&limit=1&selected=alpha&selected=beta&selected=gamma")
	if status != 200 || len(results) != 1 || results[0].Key != "alpha" {
		t.Fatalf("status=%d results=%#v", status, results)
	}
	results, _, status = fetchSearchJSON(t, ts.URL+"/api/search?selected=alpha")
	if status != 200 || len(results) != 0 {
		t.Fatalf("selection expanded empty search: %d %#v", status, results)
	}
}
