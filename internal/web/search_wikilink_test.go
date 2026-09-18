package web

import (
	"strings"
	"testing"
)

// TestSearchAPI_PublishesTheWikilinkTargetForEachResult pins the field the
// editor's completion writes into a note.
//
// The two rows are the two ways the obvious derivations fail. `Notes/Renamed.md`
// carries a frontmatter title that is not its filename, so a completion built
// from Title would insert a link the graph cannot resolve; `Notes/Mixed Case.md`
// is exact-case, so one built from Key — which is folded — would insert a link
// that resolves but reads as somebody else's spelling of the note.
func TestSearchAPI_PublishesTheWikilinkTargetForEachResult(t *testing.T) {
	files := map[string][]byte{
		"Notes/Renamed.md":    []byte("---\ntitle: Something Else\n---\n\nSearchable body.\n"),
		"Notes/Mixed Case.md": []byte("---\ntitle: Mixed Case\n---\n\nSearchable body.\n"),
		"Top.md":              []byte("---\ntitle: Top\n---\n\nSearchable body.\n"),
	}
	ts, _ := newHTTPServerForFiles(t, files)

	var results []searchResult
	getJSON(t, ts, "/api/search?q=Searchable", &results)

	wikilinkByKey := map[string]string{}
	for _, result := range results {
		wikilinkByKey[result.Key] = result.Wikilink
	}
	for key, want := range map[string]string{
		"notes/renamed":    "Notes/Renamed",
		"notes/mixed case": "Notes/Mixed Case",
		"top":              "Top",
	} {
		got, ok := wikilinkByKey[key]
		if !ok {
			t.Fatalf("search results missing %q: %+v", key, results)
		}
		if got != want {
			t.Fatalf("search result %q wikilink = %q, want %q", key, got, want)
		}
	}
}

// TestWikilinkTargetMatchesTheCopyAffordance keeps the completion and the note
// page's "Copy wikilink" button on one derivation. They are the same promise
// made in two places, and a person who pastes one and completes the other must
// get the same text.
func TestWikilinkTargetMatchesTheCopyAffordance(t *testing.T) {
	files := map[string][]byte{
		"Notes/Renamed.md": []byte("---\ntitle: Something Else\n---\n\nSearchable body.\n"),
	}
	ts, _ := newHTTPServerForFiles(t, files)

	var results []searchResult
	getJSON(t, ts, "/api/search?q=Searchable", &results)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}

	resp, err := ts.Client().Get(ts.URL + "/n/notes/renamed")
	if err != nil {
		t.Fatalf("get note page: %v", err)
	}
	body := readResponseBody(t, resp)
	want := `data-copy-text="[[` + results[0].Wikilink + `]]"`
	if !strings.Contains(body, want) {
		t.Fatalf("note page copy affordance does not carry %q", want)
	}
}
