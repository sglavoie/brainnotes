package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

func TestSearchCountsBeforeQueryAndPayloadLimits(t *testing.T) {
	files := make(map[string][]byte, 60)
	for i := range 60 {
		files[fmt.Sprintf("Note%02d.md", i)] = []byte("needle\n")
	}
	ts, _ := newHTTPServerForFiles(t, files)
	for _, tc := range []struct {
		query string
		total int
		limit int
		shown int
	}{
		{"q=needle", 60, 0, 50},
		{"q=needle&limit=10&sort=-path", 60, 10, 10},
		{"q=needle&limit=500", 60, 500, 50},
		{"q=Note00", 1, 0, 1},
		{"q=missing", 0, 0, 0},
		{"limit=10", 0, 10, 0},
	} {
		t.Run(tc.query, func(t *testing.T) {
			resp, err := ts.Client().Get(ts.URL + "/api/search?" + tc.query)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			assertSearchCountHeaders(t, resp.Header, tc.total, tc.limit)

			var rows []searchResult
			if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
				t.Fatal(err)
			}
			if rows == nil || len(rows) != tc.shown {
				t.Fatalf("rows = %#v, want array of %d", rows, tc.shown)
			}
		})
	}
}

func assertSearchCountHeaders(t *testing.T, headers http.Header, total, limit int) {
	t.Helper()
	for header, want := range map[string]int{
		"X-BN-Search-Total": total,
		"X-BN-Search-Limit": limit,
		"X-BN-Search-Cap":   searchResultLimit,
	} {
		if got := headers.Get(header); got != strconv.Itoa(want) {
			t.Errorf("%s = %q, want %d", header, got, want)
		}
	}
}

func TestSearchPaginationHonorsSortAndQueryLimit(t *testing.T) {
	files := make(map[string][]byte)
	for i := range 125 {
		files[fmt.Sprintf("Note%03d.md", i)] = []byte("needle\n")
	}
	ts, _ := newHTTPServerForFiles(t, files)
	for _, tc := range []struct {
		query string
		shown int
		first string
	}{
		{"q=needle&sort=path&offset=50", 50, "Note050"},
		{"q=needle&sort=-path&offset=50", 50, "Note074"},
		{"q=needle&sort=path&limit=65&offset=50", 15, "Note050"},
		{"q=needle&sort=path&offset=100", 25, "Note100"},
		{"q=needle&limit=10&offset=50", 0, ""},
		{"q=needle&offset=9223372036854775807", 0, ""},
	} {
		t.Run(tc.query, func(t *testing.T) {
			resp, err := ts.Client().Get(ts.URL + "/api/search?" + tc.query)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			var rows []searchResult
			if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
				t.Fatal(err)
			}
			if len(rows) != tc.shown {
				t.Fatalf("got %d, want %d", len(rows), tc.shown)
			}
			if len(rows) > 0 && rows[0].Title != tc.first {
				t.Fatalf("first title = %s, want %s", rows[0].Title, tc.first)
			}
		})
	}
}

func TestSearchPaginationRejectsInvalidOffsets(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{"Note.md": []byte("needle")})
	for _, offset := range []string{"-1", "1.2", "abc", "999999999999999999999999999"} {
		resp, err := ts.Client().Get(ts.URL + "/api/search?q=needle&offset=" + offset)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("offset %s: %d", offset, resp.StatusCode)
		}
	}
}
