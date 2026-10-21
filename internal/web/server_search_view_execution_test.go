package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/noteops"
)

// A saved view's nav link carries `expr`, `sort`, `relative_to`, and `limit`.
// These tests pin the server half of that contract: all four are executed, so a
// view opened in the browser shows the result set it was saved as rather than
// the same filter with the page's own ranking and the fixed 50-result cap.

// searchOrderFixture writes four tagged notes whose `last` dates are out of step
// with their titles in both directions, so path/title order, `modified`
// ascending, and `modified` descending each produce a different first two. A
// response that quietly kept the default best-match ranking therefore cannot
// pass for a correctly sorted one.
func searchOrderFixture() map[string][]byte {
	return map[string][]byte{
		"Alpha.md": []byte("---\ntags: [foo]\nlast: 2024-01-02\n---\nAlpha body links to [[Delta]].\n"),
		"Bravo.md": []byte("---\ntags: [foo]\nlast: 2024-01-01\n---\nBravo body.\n"),
		"Delta.md": []byte("---\ntags: [foo]\nlast: 2024-01-03\n---\nDelta body.\n"),
		"Echo.md":  []byte("---\ntags: [bar]\nlast: 2024-01-04\n---\nEcho body.\n"),
	}
}

func searchTitles(results []searchResult) []string {
	titles := make([]string, 0, len(results))
	for _, result := range results {
		titles = append(titles, result.Title)
	}
	return titles
}

func searchQueryURL(base string, params url.Values) string {
	return base + "/api/search?" + params.Encode()
}

// TestSearchAppliesSavedViewSortAndLimit is the core regression: `sort=` and
// `limit=` from a saved view link must both reach the result set, with the cap
// applied after the ordering so it keeps the top N.
func TestSearchAppliesSavedViewSortAndLimit(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, searchOrderFixture())

	cases := []struct {
		name   string
		params url.Values
		want   []string
	}{
		{
			name:   "no ordering keeps the best-match ranking",
			params: url.Values{"tag": {"foo"}},
			want:   []string{"Alpha", "Bravo", "Delta"},
		},
		{
			name:   "a stored descending sort is executed",
			params: url.Values{"tag": {"foo"}, "sort": {"-modified"}},
			want:   []string{"Delta", "Alpha", "Bravo"},
		},
		{
			name:   "a stored ascending sort is executed",
			params: url.Values{"tag": {"foo"}, "sort": {"modified"}},
			want:   []string{"Bravo", "Alpha", "Delta"},
		},
		{
			// The whole finding in one row: sort and limit together. Dropping
			// the sort would answer Alpha, Bravo; dropping the limit would
			// answer all three; applying the limit first would answer Alpha,
			// Bravo re-sorted.
			name:   "a stored sort and a non-zero limit are executed together",
			params: url.Values{"tag": {"foo"}, "sort": {"-modified"}, "limit": {"2"}},
			want:   []string{"Delta", "Alpha"},
		},
		{
			name:   "a limit with no sort caps the default ranking",
			params: url.Values{"tag": {"foo"}, "limit": {"2"}},
			want:   []string{"Alpha", "Bravo"},
		},
		{
			// A cap wider than the payload cap is not a way to raise it.
			name:   "a limit above the payload cap does not widen the response",
			params: url.Values{"tag": {"foo"}, "limit": {"500"}},
			want:   []string{"Alpha", "Bravo", "Delta"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results, _, status := fetchSearchJSON(t, searchQueryURL(ts.URL, tc.params))
			if status != http.StatusOK {
				t.Fatalf("status: got %d, want %d", status, http.StatusOK)
			}
			if !slices.Equal(searchTitles(results), tc.want) {
				t.Fatalf("ordering: got %v, want %v", searchTitles(results), tc.want)
			}
		})
	}
}

// TestSearchAppliesSavedViewRelevanceSortAndLimit covers the one ordering the
// shared query.OrderNotes cannot produce: relevance reads the recall corpus, so
// it runs through a ranker the handler binds to the resolved anchor.
//
// Alpha links to Delta, so both outrank the unconnected Bravo; the anchor itself
// is never dropped from a result set it matched.
func TestSearchAppliesSavedViewRelevanceSortAndLimit(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, searchOrderFixture())

	params := url.Values{"tag": {"foo"}, "sort": {"relevance"}, "relative_to": {"alpha"}}
	results, _, status := fetchSearchJSON(t, searchQueryURL(ts.URL, params))
	if status != http.StatusOK {
		t.Fatalf("status: got %d, want %d", status, http.StatusOK)
	}
	titles := searchTitles(results)
	if len(titles) != 3 {
		t.Fatalf("relevance ordering must not change which notes match: got %v", titles)
	}
	if titles[0] != "Alpha" {
		t.Fatalf("the anchor must lead its own relevance ordering: got %v", titles)
	}
	if titles[1] != "Delta" {
		t.Fatalf("a linked note must outrank an unconnected one: got %v", titles)
	}

	params.Set("limit", "2")
	capped, _, status := fetchSearchJSON(t, searchQueryURL(ts.URL, params))
	if status != http.StatusOK {
		t.Fatalf("capped status: got %d, want %d", status, http.StatusOK)
	}
	if !slices.Equal(searchTitles(capped), []string{"Alpha", "Delta"}) {
		t.Fatalf("capped relevance ordering: got %v, want [Alpha Delta]", searchTitles(capped))
	}
}

// relevanceTieFixture is internal/cli's buildRelevanceTieVault, note for note.
//
// Mike and November carry the same body and the same frontmatter and differ only
// by name, so nothing the ranker reads separates them: they tie, and only the
// path tie-break orders them. Delta is linked from the anchor and outranks both,
// which puts the tied pair at the bottom of a `relevance` ordering and at the
// top of a `-relevance` one, where a limit can see them.
func relevanceTieFixture() map[string][]byte {
	return map[string][]byte{
		"Notes/Alpha.md":    []byte("---\nstatus: wip\n---\nAlpha body links to [[Delta]].\n"),
		"Notes/Delta.md":    []byte("---\nstatus: wip\n---\nDelta body.\n"),
		"Notes/Mike.md":     []byte("---\nstatus: wip\n---\nTie body.\n"),
		"Notes/November.md": []byte("---\nstatus: wip\n---\nTie body.\n"),
	}
}

// searchVaultPaths renders results as the vault-relative paths `bn find` prints,
// so a web ordering and a CLI ordering can be compared as the same list of
// strings. searchResult.Path is a route rather than a path, and a route is
// lowercased, so it cannot serve as that common spelling itself.
func searchVaultPaths(results []searchResult) []string {
	paths := make([]string, 0, len(results))
	for _, result := range results {
		rel := result.Title + ".md"
		if result.Folder != "" {
			rel = result.Folder + "/" + rel
		}
		paths = append(paths, rel)
	}
	return paths
}

// TestSearchAppliesSavedViewDescendingRelevanceAndLimit is the web half of the
// one definition of descending relevance, and the regression for a saved view
// selecting a different top N in the browser than at the terminal.
//
// `-relevance` reverses the score comparison and nothing else: equal-score notes
// stay in ascending path order in both directions. The paths asserted here are
// the paths internal/cli's TestFindViewHonorsDescendingRelevanceAndLimit asserts
// for the same vault, the same anchor, and the same cap — the two together are
// the parity claim, so neither may be updated alone.
func TestSearchAppliesSavedViewDescendingRelevanceAndLimit(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, relevanceTieFixture())

	ascending := url.Values{"status": {"wip"}, "sort": {"relevance"}, "relative_to": {"notes/alpha"}}
	results, _, status := fetchSearchJSON(t, searchQueryURL(ts.URL, ascending))
	if status != http.StatusOK {
		t.Fatalf("status: got %d, want %d", status, http.StatusOK)
	}
	want := []string{"Notes/Alpha.md", "Notes/Delta.md", "Notes/Mike.md", "Notes/November.md"}
	if !slices.Equal(searchVaultPaths(results), want) {
		t.Fatalf("relevance ordering: got %v, want %v", searchVaultPaths(results), want)
	}

	// Scores reverse, the tie does not. Reversing the whole ordered slice would
	// answer November before Mike.
	descending := url.Values{"status": {"wip"}, "sort": {"-relevance"}, "relative_to": {"notes/alpha"}}
	results, _, status = fetchSearchJSON(t, searchQueryURL(ts.URL, descending))
	if status != http.StatusOK {
		t.Fatalf("descending status: got %d, want %d", status, http.StatusOK)
	}
	want = []string{"Notes/Mike.md", "Notes/November.md", "Notes/Delta.md", "Notes/Alpha.md"}
	if !slices.Equal(searchVaultPaths(results), want) {
		t.Fatalf("-relevance ordering: got %v, want %v", searchVaultPaths(results), want)
	}

	// The cap is what makes the tie orientation observable as *which* notes come
	// back rather than only their order.
	descending.Set("limit", "2")
	capped, _, status := fetchSearchJSON(t, searchQueryURL(ts.URL, descending))
	if status != http.StatusOK {
		t.Fatalf("capped status: got %d, want %d", status, http.StatusOK)
	}
	want = []string{"Notes/Mike.md", "Notes/November.md"}
	if !slices.Equal(searchVaultPaths(capped), want) {
		t.Fatalf("capped -relevance ordering: got %v, want %v", searchVaultPaths(capped), want)
	}
}

// TestSearchRejectsAnUnrunnableResultShape pins that an ordering the server
// cannot execute is a rejected request rather than an ordering silently dropped
// — the same rule queryexpr.ResolveView applies to a stored view and
// validateFindSort applies to `bn find`'s flags.
func TestSearchRejectsAnUnrunnableResultShape(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, searchOrderFixture())

	cases := []struct {
		name   string
		params url.Values
	}{
		{name: "unknown sort key", params: url.Values{"tag": {"foo"}, "sort": {"recency"}}},
		{name: "relevance with no anchor", params: url.Values{"tag": {"foo"}, "sort": {"relevance"}}},
		{name: "anchor without relevance", params: url.Values{"tag": {"foo"}, "sort": {"-modified"}, "relative_to": {"alpha"}}},
		{name: "unknown anchor", params: url.Values{"tag": {"foo"}, "sort": {"relevance"}, "relative_to": {"nope"}}},
		{name: "non-numeric limit", params: url.Values{"tag": {"foo"}, "limit": {"many"}}},
		{name: "negative limit", params: url.Values{"tag": {"foo"}, "limit": {"-1"}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Get(searchQueryURL(ts.URL, tc.params)) // #nosec G107 -- test-only local URL
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusBadRequest)
			}
		})
	}
}

// TestSearchOrderingDoesNotSelect pins that an ordering or a cap alone is not a
// filter. hasNonTextFilters deliberately ignores both, so a bare `sort=` must
// still answer nothing rather than serialize the whole vault.
func TestSearchOrderingDoesNotSelect(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, searchOrderFixture())

	for _, params := range []url.Values{
		{"sort": {"-modified"}},
		{"limit": {"2"}},
	} {
		results, _, status := fetchSearchJSON(t, searchQueryURL(ts.URL, params))
		if status != http.StatusOK {
			t.Fatalf("%v status: got %d, want %d", params, status, http.StatusOK)
		}
		if len(results) != 0 {
			t.Fatalf("%v must not select anything: got %v", params, searchTitles(results))
		}
	}
}

// A relevance ordering the server cannot build (a configuration that will not
// reload, a corpus that will not build) is the server's failure, not the
// request's, so it is a 500 in the management error shape. An anchor that names
// no note stays a 400 filter error. Both messages are the reader's copy, with
// the vault root taken out for a remote one.
func TestSearchOrderErrorSeparatesClientMistakesFromServerFailures(t *testing.T) {
	const root = "/srv/vault"
	redacting := func() (*httptest.ResponseRecorder, http.ResponseWriter) {
		rec := httptest.NewRecorder()
		return rec, &redactingResponseWriter{ResponseWriter: rec, redactor: vaultRootRedactor{roots: []string{root}}}
	}

	rec, w := redacting()
	writeSearchOrderError(w, fmt.Errorf("relative_to: build relevance corpus: open %s/a.md: denied", root))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("server failure status = %d, want 500", rec.Code)
	}
	internal := decodeManagementError(t, rec.Body.String())
	if internal.Code != string(noteops.CodeInternal) || !strings.Contains(internal.Message, "build relevance corpus") {
		t.Fatalf("server failure payload = %#v", internal)
	}
	assertVaultRootRedaction(t, "server failure", internal.Message, root, true)

	rec, w = redacting()
	writeSearchOrderError(w, searchClientErrorf("relative_to: no note matches %q", root+"/nope"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("client mistake status = %d, want 400", rec.Code)
	}
	var client filterErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &client); err != nil {
		t.Fatalf("decode filter error: %v", err)
	}
	assertVaultRootRedaction(t, "client mistake", client.Error.Message, root, true)
}
