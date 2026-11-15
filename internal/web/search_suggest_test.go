package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// suggestedTitles asks the suggest route and returns the titles it offered.
func suggestedTitles(t *testing.T, ts *httptest.Server, q string) []string {
	t.Helper()
	response, err := ts.Client().Get(ts.URL + searchSuggestAPIRoutePath + "?q=" + url.QueryEscape(q))
	if err != nil {
		t.Fatal(err)
	}
	body := readResponseBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("suggest %q: %d %s", q, response.StatusCode, body)
	}
	var results []searchResult
	if err := json.Unmarshal([]byte(body), &results); err != nil {
		t.Fatalf("suggest %q: %v in %s", q, err, body)
	}
	titles := make([]string, 0, len(results))
	for _, result := range results {
		if result.Path == "" || result.Key == "" {
			t.Fatalf("suggestion without an address: %+v", result)
		}
		titles = append(titles, result.Title)
	}
	return titles
}

func newSuggestServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Concepts/Deliberate Practice.md": []byte("Focused practice.\n"),
		"Books/Deep Work.md":              []byte("Depth.\n"),
		"Projects/Learn Rust.md":          []byte("Ownership.\n"),
	})
	return ts
}

func TestSearchSuggestOffersClosestTitles(t *testing.T) {
	ts := newSuggestServer(t)
	for q, want := range map[string]string{
		"delibrate pratice": "Deliberate Practice",
		"deepwork":          "Deep Work",
		"lern rust":         "Learn Rust",
	} {
		if got := suggestedTitles(t, ts, q); len(got) != 1 || got[0] != want {
			t.Errorf("suggest(%q) = %v, want [%s]", q, got, want)
		}
	}
}

func TestSearchSuggestOffersNothingForAnEmptyOrDistantSearch(t *testing.T) {
	ts := newSuggestServer(t)
	for _, q := range []string{"", "   ", "quantum chromodynamics"} {
		if got := suggestedTitles(t, ts, q); len(got) != 0 {
			t.Errorf("suggest(%q) = %v, want nothing", q, got)
		}
	}
}

func TestSearchSuggestRefusesWrites(t *testing.T) {
	ts := newSuggestServer(t)
	response, err := ts.Client().Post(ts.URL+searchSuggestAPIRoutePath+"?q=deep", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = readResponseBody(t, response)
	if response.StatusCode != http.StatusMethodNotAllowed && response.StatusCode != http.StatusForbidden {
		t.Fatalf("POST suggest: %d, want a refusal", response.StatusCode)
	}
}
