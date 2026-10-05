package web

import (
	"net/http"
	"strings"
	"testing"
)

func pinnedPageTestVault() map[string][]byte {
	return map[string][]byte{
		"Notes/Alpha.md":  []byte("---\ntitle: Alpha\npinned: 2.0\n---\nAlpha body\n"),
		"Notes/Beta.md":   []byte("---\ntitle: Beta\npinned: 1.0\n---\nBeta body\n"),
		"Notes/Gamma.md":  []byte("---\ntitle: Gamma\npinned: true\n---\nGamma body\n"),
		"Notes/Delta.md":  []byte("---\ntitle: Delta\n---\nDelta body\n"),
		"Notes/Empty.md":  []byte("---\ntitle: Empty\npinned: false\n---\nEmpty body\n"),
		"Notes/Search.md": []byte("---\ntitle: Search\npinned: 3.0\n---\nSearch body\n"),
	}
}

func TestPinnedPage_RendersListInPinOrder(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, pinnedPageTestVault())

	resp, err := http.Get(ts.URL + pinnedRoutePath) // #nosec G107 -- test-only local listener URL
	if err != nil {
		t.Fatalf("GET %s: %v", pinnedRoutePath, err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	for _, want := range []string{
		"data-pinned-root",
		"data-pinned-csrf=",
		"data-pinned-list",
		`data-pinned-key="notes/beta"`,
		"data-pinned-handle",
		"data-pinned-empty",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("pinned page missing %q", want)
		}
	}

	// Ranked pins ascend, the unranked pin sorts last, and an absent or false
	// `pinned` key keeps a note off the list entirely.
	order := []string{"notes/beta", "notes/alpha", "notes/search", "notes/gamma"}
	at := 0
	for _, key := range order {
		next := strings.Index(body[at:], `data-pinned-key="`+key+`"`)
		if next < 0 {
			t.Fatalf("pinned page did not list %q after the preceding rows", key)
		}
		at += next
	}
	for _, key := range []string{"notes/delta", "notes/empty"} {
		if strings.Contains(body, `data-pinned-key="`+key+`"`) {
			t.Fatalf("pinned page listed unpinned note %q", key)
		}
	}
}

func TestPinnedPage_EmptyVaultRendersEmptyState(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Notes/Delta.md": []byte("---\ntitle: Delta\n---\nDelta body\n"),
	})

	resp, err := http.Get(ts.URL + pinnedRoutePath) // #nosec G107 -- test-only local listener URL
	if err != nil {
		t.Fatalf("GET %s: %v", pinnedRoutePath, err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(body, "pinned-empty") {
		t.Fatalf("pinned page missing the empty state")
	}
	if strings.Contains(body, "data-pinned-list") {
		t.Fatalf("pinned page rendered a list with no pins")
	}
}

func TestPinnedPage_AppearsInSiteNav(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, pinnedPageTestVault())

	for _, path := range []string{"/", libraryRoutePath, healthRoutePath, pinnedRoutePath, "/n/notes/alpha"} {
		resp, err := http.Get(ts.URL + path) // #nosec G107 -- test-only local listener URL
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body := readResponseBody(t, resp)
		if !strings.Contains(body, `<a href="/pinned">`) && !strings.Contains(body, `<a href="/pinned" aria-current="page">`) {
			t.Fatalf("%s is missing the Pinned navigation link", path)
		}
	}
}

// TestIndexPage_PinnedSection pins more notes than the overview shows so the
// top-7 cap and the "see all" link are both exercised.
func TestIndexPage_PinnedSection(t *testing.T) {
	files := map[string][]byte{}
	for i := 1; i <= 9; i++ {
		name := string(rune('a' + i - 1))
		files["Notes/"+strings.ToUpper(name)+".md"] =
			[]byte("---\ntitle: " + strings.ToUpper(name) + "\npinned: " + string(rune('0'+i)) + ".0\n---\nBody\n")
	}
	ts, _ := newHTTPServerForFiles(t, files)

	resp, err := http.Get(ts.URL + "/") // #nosec G107 -- test-only local listener URL
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if got := strings.Count(body, "data-pinned-key="); got != indexPinnedLimit {
		t.Fatalf("overview rendered %d pinned rows, want %d", got, indexPinnedLimit)
	}
	if !strings.Contains(body, `<a href="/pinned">See all pinned notes</a>`) {
		t.Fatalf("overview is missing the see-all pinned link")
	}
	// The pinned section sits between the health snapshot and recent notes.
	health := strings.Index(body, "health-snapshot-grid")
	pinned := strings.Index(body, "data-pinned-key=")
	recent := strings.Index(body, "recent-list")
	if health < 0 || pinned < 0 || recent < 0 || !(health < pinned && pinned < recent) {
		t.Fatalf("pinned section is not between the health snapshot and recent notes (health=%d pinned=%d recent=%d)",
			health, pinned, recent)
	}
}

func TestNotePage_PinToggleReflectsState(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, pinnedPageTestVault())

	cases := []struct {
		path    string
		state   string
		pressed string
		label   string
	}{
		{"/n/notes/alpha", "pinned", "true", ">Unpin<"},
		{"/n/notes/delta", "unpinned", "false", ">Pin<"},
		{"/n/notes/gamma", "pinned", "true", ">Unpin<"},
		{"/n/notes/empty", "unpinned", "false", ">Pin<"},
	}
	for _, testCase := range cases {
		resp, err := http.Get(ts.URL + testCase.path) // #nosec G107 -- test-only local listener URL
		if err != nil {
			t.Fatalf("GET %s: %v", testCase.path, err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, want %d", testCase.path, resp.StatusCode, http.StatusOK)
		}
		for _, want := range []string{
			"data-pin-note",
			`data-pin-state="` + testCase.state + `"`,
			`aria-pressed="` + testCase.pressed + `"`,
			testCase.label,
			"data-pin-status",
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s pin group missing %q", testCase.path, want)
			}
		}
	}
}

func TestSearchAPI_ReportsPinnedState(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, pinnedPageTestVault())

	var results []searchResult
	getJSON(t, ts, "/api/search?q=body", &results)
	if len(results) == 0 {
		t.Fatalf("search returned no results")
	}

	pinnedByKey := map[string]bool{}
	for _, result := range results {
		pinnedByKey[result.Key] = result.Pinned
	}
	for key, want := range map[string]bool{
		"notes/alpha": true,
		"notes/gamma": true,
		"notes/delta": false,
		"notes/empty": false,
	} {
		got, ok := pinnedByKey[key]
		if !ok {
			t.Fatalf("search results missing %q: %+v", key, results)
		}
		if got != want {
			t.Fatalf("search result %q pinned = %v, want %v", key, got, want)
		}
	}
}
