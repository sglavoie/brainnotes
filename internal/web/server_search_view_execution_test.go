package web

import (
	"net/http"
	"net/url"
	"testing"
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
			if !sameStrings(searchTitles(results), tc.want) {
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
	if !sameStrings(searchTitles(capped), []string{"Alpha", "Delta"}) {
		t.Fatalf("capped relevance ordering: got %v, want [Alpha Delta]", searchTitles(capped))
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
