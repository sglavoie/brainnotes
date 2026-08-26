package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestServer_SearchAPI_ResultShapeAndRanking(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"NeedleGuide.md":  []byte("---\ntitle: Needle Guide\n---\nIntro text\n"),
		"BodyOne.md":      []byte("---\ntitle: Body One\n---\nThis body has needle in it.\n"),
		"Work/BodyTwo.md": []byte("---\ntitle: Body Two\n---\nThis body has NEEDLE in it.\n"),
		"Escape.md":       []byte("---\ntitle: Escape\n---\n<script>alert(1)</script> with needle\n"),
		"Other.md":        []byte("---\ntitle: Other\n---\nNo hit here.\n"),
	})

	results, contentType, statusCode := fetchSearchJSON(t, ts.URL+"/api/search?q=needle")
	if statusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", statusCode, http.StatusOK)
	}
	if !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("content-type: got %q, want application/json...", contentType)
	}
	if len(results) != 4 {
		t.Fatalf("result count: got %d, want 4", len(results))
	}

	if results[0].Title != "NeedleGuide" {
		t.Fatalf("expected title match to rank first, got %#v", results[0])
	}
	if !sameStrings(results[0].MatchedFields, []string{"title"}) {
		t.Fatalf("title result matched fields: got %#v, want [title]", results[0].MatchedFields)
	}
	if results[1].Title != "BodyOne" || results[2].Title != "BodyTwo" || results[3].Title != "Escape" {
		t.Fatalf("unexpected body-match ordering: %#v", results)
	}
	if !sameStrings(results[1].MatchedFields, []string{"body"}) {
		t.Fatalf("body result matched fields: got %#v, want [body]", results[1].MatchedFields)
	}

	for _, result := range results {
		if result.Key == "" {
			t.Fatalf("expected non-empty key in result: %#v", result)
		}
		if !strings.HasPrefix(result.Path, "/n/") {
			t.Fatalf("expected note path to start with /n/, got %#v", result)
		}
	}
	pathsByTitle := make(map[string]string, len(results))
	for _, result := range results {
		pathsByTitle[result.Title] = result.Path
	}
	if got, want := pathsByTitle["BodyTwo"], "/n/work/bodytwo"; got != want {
		t.Fatalf("nested result path: got %q, want %q; results=%#v", got, want, results)
	}
}

func TestServer_SearchAPI_FilterOnlyAndCombinedFilters(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Work/Alpha.md":     []byte("---\ntags: [foo]\nstatus: active\n---\nAlpha body.\n"),
		"Work/Needle.md":    []byte("---\ntags: [foo]\nstatus: active\n---\nNeedle body.\n"),
		"Archive/Needle.md": []byte("---\ntags: [foo]\nstatus: archived\n---\nNeedle body.\n"),
		"Work/Other.md":     []byte("---\ntags: [bar]\nstatus: active\n---\nNeedle body.\n"),
	})

	filterOnly, _, filterStatus := fetchSearchJSON(t, ts.URL+"/api/search?tag=foo&folder=Work")
	if filterStatus != http.StatusOK {
		t.Fatalf("filter-only status: got %d, want %d", filterStatus, http.StatusOK)
	}
	if len(filterOnly) != 2 {
		t.Fatalf("filter-only result count: got %d, want 2; results=%#v", len(filterOnly), filterOnly)
	}
	if filterOnly[0].Title != "Alpha" || filterOnly[1].Title != "Needle" {
		t.Fatalf("filter-only results not sorted as expected: %#v", filterOnly)
	}
	if filterOnly[0].Snippet != "" || len(filterOnly[0].MatchedFields) != 0 {
		t.Fatalf("filter-only result should not include text metadata: %#v", filterOnly[0])
	}

	combined, _, combinedStatus := fetchSearchJSON(t, ts.URL+"/api/search?q=needle&tag=foo&folder=Work&status=active")
	if combinedStatus != http.StatusOK {
		t.Fatalf("combined status: got %d, want %d", combinedStatus, http.StatusOK)
	}
	if len(combined) != 1 || combined[0].Title != "Needle" {
		t.Fatalf("combined result mismatch: %#v", combined)
	}
	if !sameStrings(combined[0].MatchedFields, []string{"title", "body"}) {
		t.Fatalf("combined matched fields: got %#v, want [title body]", combined[0].MatchedFields)
	}
}

func TestServer_SearchAPI_IssueMetadata(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Broken.md": []byte("---\ntitle: Broken\ndead_end: false\n---\nNeedle [[Missing]]\n"),
		"Linked.md": []byte("---\ntitle: Linked\n---\n[[Broken]]\n"),
	})

	results, _, statusCode := fetchSearchJSON(t, ts.URL+"/api/search?q=needle")
	if statusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", statusCode, http.StatusOK)
	}
	if len(results) != 1 {
		t.Fatalf("result count: got %d, want 1; results=%#v", len(results), results)
	}
	if !sameStrings(results[0].MatchedFields, []string{"body"}) {
		t.Fatalf("matched fields: got %#v, want [body]", results[0].MatchedFields)
	}
	if !sameStrings(results[0].Issues, []string{"broken"}) {
		t.Fatalf("issue metadata: got %#v, want [broken] because dead-end is exempt", results[0].Issues)
	}
}

func TestServer_SearchAPI_InvalidFilterParams(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Note.md": []byte("---\ntitle: Note\n---\nBody text.\n"),
	})

	resp, err := http.Get(ts.URL + "/api/search?broken=maybe") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /api/search: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestServer_SearchAPI_EmptyAndNoMatchQueries(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Note.md": []byte("---\ntitle: Note\n---\nBody text.\n"),
	})

	emptyResults, _, emptyStatus := fetchSearchJSON(t, ts.URL+"/api/search?q=")
	if emptyStatus != http.StatusOK {
		t.Fatalf("status for empty query: got %d, want %d", emptyStatus, http.StatusOK)
	}
	if len(emptyResults) != 0 {
		t.Fatalf("expected empty result list for empty query, got %#v", emptyResults)
	}

	noMatchResults, _, noMatchStatus := fetchSearchJSON(t, ts.URL+"/api/search?q=missing")
	if noMatchStatus != http.StatusOK {
		t.Fatalf("status for no-match query: got %d, want %d", noMatchStatus, http.StatusOK)
	}
	if len(noMatchResults) != 0 {
		t.Fatalf("expected empty result list for no-match query, got %#v", noMatchResults)
	}
}

func TestServer_SearchAPI_SnippetEscaping(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Danger.md": []byte("---\ntitle: Danger\n---\n<script>alert(1)</script> marker\n"),
	})

	results, _, statusCode := fetchSearchJSON(t, ts.URL+"/api/search?q=alert")
	if statusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", statusCode, http.StatusOK)
	}
	if len(results) != 1 {
		t.Fatalf("result count: got %d, want 1", len(results))
	}
	if strings.Contains(results[0].Snippet, "<script>") {
		t.Fatalf("expected escaped snippet, got %q", results[0].Snippet)
	}
	if !strings.Contains(results[0].Snippet, "&lt;script&gt;") {
		t.Fatalf("missing escaped script tag in snippet: %q", results[0].Snippet)
	}
}

func TestServer_SearchAPI_ResultCap(t *testing.T) {
	files := make(map[string][]byte, 60)
	for i := 0; i < 60; i++ {
		rel := fmt.Sprintf("Note%02d.md", i)
		files[rel] = []byte(fmt.Sprintf("---\ntitle: Note %02d\n---\nNeedle body text\n", i))
	}
	ts, _ := newHTTPServerForFiles(t, files)

	results, _, statusCode := fetchSearchJSON(t, ts.URL+"/api/search?q=needle")
	if statusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", statusCode, http.StatusOK)
	}
	if len(results) != searchResultLimit {
		t.Fatalf("result count: got %d, want %d", len(results), searchResultLimit)
	}
}

func TestServer_SearchAPI_ExprComposesWithFacetsAndStandsAlone(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Work/Alpha.md":    []byte("---\ntitle: Alpha\ntags: [keep]\nstatus: wip\n---\nneedle body\n"),
		"Work/Beta.md":     []byte("---\ntitle: Beta\ntags: [keep]\nstatus: done\n---\nneedle body\n"),
		"Archive/Gamma.md": []byte("---\ntitle: Gamma\ntags: [drop]\nstatus: wip\n---\nneedle body\n"),
	})

	exprOnly, _, exprStatus := fetchSearchJSON(t, ts.URL+"/api/search?expr="+url.QueryEscape("status:wip"))
	if exprStatus != http.StatusOK {
		t.Fatalf("expr-only status: got %d, want %d", exprStatus, http.StatusOK)
	}
	if len(exprOnly) != 2 {
		t.Fatalf("an expression-only request must return matches, got %#v", exprOnly)
	}
	if exprOnly[0].Title != "Alpha" || exprOnly[1].Title != "Gamma" {
		t.Fatalf("expr-only results: %#v", exprOnly)
	}

	withFacet, _, facetStatus := fetchSearchJSON(t, ts.URL+"/api/search?tag=keep&expr="+url.QueryEscape("status:wip"))
	if facetStatus != http.StatusOK {
		t.Fatalf("expr+facet status: got %d, want %d", facetStatus, http.StatusOK)
	}
	if len(withFacet) != 1 || withFacet[0].Title != "Alpha" {
		t.Fatalf("expression must compose with the facet chip, got %#v", withFacet)
	}

	withText, _, textStatus := fetchSearchJSON(t, ts.URL+"/api/search?q=needle&expr="+url.QueryEscape("status:done"))
	if textStatus != http.StatusOK {
		t.Fatalf("expr+q status: got %d, want %d", textStatus, http.StatusOK)
	}
	if len(withText) != 1 || withText[0].Title != "Beta" {
		t.Fatalf("expression must compose with the free-text query, got %#v", withText)
	}
}

func TestServer_SearchAPI_InvalidExprReturnsStructuredError(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Note.md": []byte("---\ntitle: Note\n---\nBody text.\n"),
	})

	resp, err := http.Get(ts.URL + "/api/search?expr=" + url.QueryEscape("statuz:wip")) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /api/search: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("content-type: got %q, want application/json...", contentType)
	}

	var payload filterErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode error payload: %v", err)
	}
	if payload.Error.Param != "expr" || payload.Error.Query != "statuz:wip" {
		t.Fatalf("error payload identity: %#v", payload.Error)
	}
	if payload.Error.Message == "" {
		t.Fatalf("error payload message is empty: %#v", payload.Error)
	}
	if payload.Error.Position == nil {
		t.Fatalf("error payload carries no caret position: %#v", payload.Error)
	}
	if payload.Error.Position.Line != 1 || payload.Error.Position.Column != 1 {
		t.Fatalf("error position: %#v", payload.Error.Position)
	}
	if payload.Error.Position.End <= payload.Error.Position.Start {
		t.Fatalf("error span is empty: %#v", payload.Error.Position)
	}
	if !sameStrings(payload.Error.Suggestions, []string{"status"}) {
		t.Fatalf("error suggestions: got %#v, want [status]", payload.Error.Suggestions)
	}
}

func TestServer_SearchAPI_ResultCapAppliesAfterRanking(t *testing.T) {
	files := make(map[string][]byte, 60)
	for i := 0; i < 60; i++ {
		// Every note matches in the body; only the last one alphabetically also
		// matches in the title, so it survives the cap only if the ranking runs
		// before the truncation.
		rel := fmt.Sprintf("Note%02d.md", i)
		files[rel] = []byte("---\ntitle: Note\n---\nNeedle body text\n")
	}
	files["ZZTitleNeedle.md"] = []byte("---\ntitle: ZZ Title Needle\n---\nUnrelated body\n")
	ts, _ := newHTTPServerForFiles(t, files)

	results, _, statusCode := fetchSearchJSON(t, ts.URL+"/api/search?q=needle")
	if statusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", statusCode, http.StatusOK)
	}
	if len(results) != searchResultLimit {
		t.Fatalf("result count: got %d, want %d", len(results), searchResultLimit)
	}
	if results[0].Title != "ZZTitleNeedle" {
		t.Fatalf("the title match must rank first and survive the cap, got %#v", results[0])
	}
}
