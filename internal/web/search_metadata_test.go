package web

import (
	"encoding/json"
	"testing"
)

func TestSearchModifiedMatchesSortProperty(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Date.md":     []byte("---\nlast: 2026-10-03\n---\nneedle\n"),
		"Time.md":     []byte("---\nlast: 2026-10-04T18:25:12+05:30\n---\nneedle\n"),
		"Midnight.md": []byte("---\nlast: 2026-10-04T00:00:00Z\n---\nneedle\n"),
		"Missing.md":  []byte("needle\n"),
		"Invalid.md":  []byte("---\nlast: yesterday\n---\nneedle\n"),
	})
	resp, err := ts.Client().Get(ts.URL + "/api/search?q=needle&sort=-modified")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var rows []searchResult
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatal(err)
	}
	want := []struct{ title, modified string }{
		{"Time", "2026-10-04T18:25:12+05:30"},
		{"Midnight", "2026-10-04T00:00:00Z"},
		{"Date", "2026-10-03"},
		{"Invalid", ""},
		{"Missing", ""},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d results, want %d", len(rows), len(want))
	}
	for i, row := range rows {
		if row.Title != want[i].title || row.Modified != want[i].modified {
			t.Errorf("row %d = %s %q, want %+v", i, row.Title, row.Modified, want[i])
		}
	}
}
