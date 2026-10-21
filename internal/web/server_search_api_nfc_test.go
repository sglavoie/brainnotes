package web

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/textnorm"
)

// The same word in both Unicode forms: composed "é" versus "e" plus a
// combining acute accent. macOS writes filenames decomposed while browsers and
// editors normally send composed text, so /api/search has to fold both to one
// form before comparing.
const (
	searchCafeNFC = "Caf\u00e9"
	searchCafeNFD = "Cafe\u0301"
)

// TestServer_SearchAPI_MatchesAccentedTitlesAcrossUnicodeForms pins the NFC step
// foldSearchQuery gained by routing through query.FoldText. Before that step a
// decomposed query never matched a composed title (or the reverse), even though
// the two render identically.
func TestServer_SearchAPI_MatchesAccentedTitlesAcrossUnicodeForms(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		searchCafeNFC + ".md": []byte("---\n---\nA plain ascii body.\n"),
		"Morning.md":          []byte("---\n---\nDiscussion du " + searchCafeNFD + " matinal.\n"),
		"Other.md":            []byte("---\n---\nNothing relevant here.\n"),
	})

	for name, queryForm := range map[string]string{"composed": searchCafeNFC, "decomposed": searchCafeNFD} {
		t.Run(name, func(t *testing.T) {
			results, _, statusCode := fetchSearchJSON(t, ts.URL+"/api/search?q="+url.QueryEscape(queryForm))
			if statusCode != http.StatusOK {
				t.Fatalf("status: got %d, want %d", statusCode, http.StatusOK)
			}
			if len(results) != 2 {
				t.Fatalf("result count: got %d, want 2 (composed title + decomposed body); results=%#v", len(results), results)
			}

			fields := make(map[string][]string, len(results))
			snippets := make(map[string]string, len(results))
			for _, result := range results {
				// The filesystem may hand the name back in either form; compare
				// on a single canonical rendering.
				title := textnorm.NFC(result.Title)
				fields[title] = result.MatchedFields
				snippets[title] = result.Snippet
			}

			if !slices.Equal(fields[searchCafeNFC], []string{"title"}) {
				t.Fatalf("composed title should match on title: %#v", results)
			}
			if !slices.Equal(fields["Morning"], []string{"body"}) {
				t.Fatalf("decomposed body should match on body: %#v", results)
			}
			// The snippet is cut from the NFC-normalized body, so the byte
			// offset the match reported still indexes the string being sliced.
			if !strings.Contains(snippets["Morning"], searchCafeNFC) {
				t.Fatalf("snippet should carry the normalized accented word, got %q", snippets["Morning"])
			}
		})
	}
}

func TestFoldSearchQuery(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"  Needle ", "needle"},
		{"NEEDLE", "needle"},
		{searchCafeNFD, "caf\u00e9"},
		{searchCafeNFC, "caf\u00e9"},
		{"   ", ""},
	}
	for _, tc := range cases {
		if got := foldSearchQuery(tc.in); got != tc.want {
			t.Errorf("foldSearchQuery(%q): got %q, want %q", tc.in, got, tc.want)
		}
	}
}
