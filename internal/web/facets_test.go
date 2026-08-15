package web

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestBuildFacetsResponseFlattensValuesAndDeduplicatesPerNote(t *testing.T) {
	v, snapshot, _ := newFilterTestSnapshot(t, map[string]string{
		"A.md": "---\ntags:\n  - Foo\n  - foo\n  - 2026\n  - true\n  - 3.0\n  - nested: skipped\ncategories:\n  - \"[[Library/Books.md#Now|Alias]]\"\n  - Books\nstatus: Reading\ntype: Note\nkind: Project\n---\nBody\n",
		"B.md": "---\ntags: [foo, bar]\ncategories: \"[[Books]]\"\nstatus: reading\nkind: Project\n---\nBody\n",
	})

	facets, err := buildFacetsResponse(snapshot, v.Root)
	if err != nil {
		t.Fatalf("buildFacetsResponse: %v", err)
	}

	tagCounts := optionCounts(facets.Tags)
	for value, want := range map[string]int{
		"Foo":  2,
		"2026": 1,
		"3.0":  1,
		"bar":  1,
		"true": 1,
	} {
		if got := tagCounts[value]; got != want {
			t.Fatalf("tag %q count = %d, want %d; all tags=%#v", value, got, want, facets.Tags)
		}
	}
	if _, ok := tagCounts["map[nested:skipped]"]; ok {
		t.Fatalf("map-typed list item should not be counted as a tag: %#v", facets.Tags)
	}

	if got := optionCounts(facets.Categories)["Books"]; got != 2 {
		t.Fatalf("Books category count = %d, want 2; categories=%#v", got, facets.Categories)
	}
	if got := optionCounts(facets.Status)["Reading"]; got != 2 {
		t.Fatalf("Reading status count = %d, want 2; status=%#v", got, facets.Status)
	}

	typeCounts := optionCounts(facets.Types)
	if got := typeCounts["Project"]; got != 2 {
		t.Fatalf("Project type count = %d, want 2; types=%#v", got, facets.Types)
	}
	if got := typeCounts["Note"]; got != 1 {
		t.Fatalf("Note type count = %d, want 1; types=%#v", got, facets.Types)
	}
}

func TestBuildFacetsResponseFoldersAndIssues(t *testing.T) {
	v, snapshot, _ := newFilterTestSnapshot(t, map[string]string{
		"Work/Source.md":     "---\ntitle: Source\n---\n[[Target]] [[Exempt]] [[Missing]]\n",
		"Work/Sub/Target.md": "---\ntitle: Target\n---\nNo links.\n",
		"Isolated.md":        "---\ntitle: Isolated\n---\nNo links.\n",
		"Exempt.md":          "---\ntitle: Exempt\ndead_end: false\n---\nNo links.\n",
	})

	facets, err := buildFacetsResponse(snapshot, v.Root)
	if err != nil {
		t.Fatalf("buildFacetsResponse: %v", err)
	}

	folderCounts := optionCounts(facets.Folders)
	if got := folderCounts["Work"]; got != 2 {
		t.Fatalf("Work folder count = %d, want 2; folders=%#v", got, facets.Folders)
	}
	if got := folderCounts["Work/Sub"]; got != 1 {
		t.Fatalf("Work/Sub folder count = %d, want 1; folders=%#v", got, facets.Folders)
	}
	if _, ok := folderCounts[""]; ok {
		t.Fatalf("root folder should not be emitted as a selectable option: %#v", facets.Folders)
	}

	issues := issueCounts(facets.Issues)
	if got := issues["broken"]; got != 1 {
		t.Fatalf("broken issue count = %d, want 1; issues=%#v", got, facets.Issues)
	}
	if got := issues["orphan"]; got != 2 {
		t.Fatalf("orphan issue count = %d, want 2; issues=%#v", got, facets.Issues)
	}
	if got := issues["dead_end"]; got != 2 {
		t.Fatalf("dead_end issue count = %d, want 2 (excluding Exempt); issues=%#v", got, facets.Issues)
	}
}

func TestBuildFacetsResponsePropertyOrdering(t *testing.T) {
	v, snapshot, _ := newFilterTestSnapshot(t, map[string]string{
		"A.md": "---\nstatus: active\ntags: [a]\nalpha: 1\n---\nBody\n",
		"B.md": "---\nstatus: done\nbeta: 1\n---\nBody\n",
		"C.md": "---\ngamma: 1\n---\nBody\n",
	})

	facets, err := buildFacetsResponse(snapshot, v.Root)
	if err != nil {
		t.Fatalf("buildFacetsResponse: %v", err)
	}

	got := optionValues(facets.Properties)
	want := []string{"status", "alpha", "beta", "gamma", "tags"}
	if !sameStrings(got, want) {
		t.Fatalf("property ordering = %#v, want %#v; properties=%#v", got, want, facets.Properties)
	}
}

func TestServerFacetsAPI(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"A.md": []byte("---\ntags: [foo]\n---\nBody\n"),
	})

	resp, err := http.Get(ts.URL + "/api/facets") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /api/facets: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%q", resp.StatusCode, http.StatusOK, string(body))
	}
	if got, want := resp.Header.Get("Content-Type"), "application/json; charset=utf-8"; got != want {
		t.Fatalf("content type = %q, want %q", got, want)
	}

	var payload facetsResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode facets response: %v; body=%q", err, string(body))
	}
	if got := optionCounts(payload.Tags)["foo"]; got != 1 {
		t.Fatalf("foo tag count = %d, want 1; payload=%#v", got, payload)
	}

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/facets", nil)
	if err != nil {
		t.Fatalf("new POST request: %v", err)
	}
	postResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/facets: %v", err)
	}
	defer postResp.Body.Close()
	if postResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want %d", postResp.StatusCode, http.StatusMethodNotAllowed)
	}
	if got := postResp.Header.Get("Allow"); got != http.MethodGet {
		t.Fatalf("POST Allow header = %q, want %q", got, http.MethodGet)
	}
}

func optionCounts(options []facetOption) map[string]int {
	counts := make(map[string]int, len(options))
	for _, option := range options {
		counts[option.Value] = option.Count
	}
	return counts
}

func issueCounts(options []facetIssueOption) map[string]int {
	counts := make(map[string]int, len(options))
	for _, option := range options {
		counts[option.Param] = option.Count
	}
	return counts
}

func optionValues(options []facetOption) []string {
	values := make([]string, 0, len(options))
	for _, option := range options {
		values = append(values, option.Value)
	}
	return values
}
