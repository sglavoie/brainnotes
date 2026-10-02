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
