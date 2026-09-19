package web

import (
	"net/http"
	"strings"
	"testing"
)

func getHistoryPage(t *testing.T, baseURL, path string) (int, string) {
	t.Helper()

	resp, err := http.Get(baseURL + path) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp.StatusCode, readResponseBody(t, resp)
}

// The Go half of the contract between templates/history.html and the jsdom
// fixture the workspace modules are tested against, for the reason
// sync_page_test.go gives: a Node process cannot render a Go template, so an
// attribute asserted only in the fixture can be renamed here with the whole
// frontend suite staying green while the control it names goes inert on the page
// a browser is actually served.
func TestHistoryPage_NamesEveryAttributeTheWorkspaceReads(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody marker.\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	code, body := getHistoryPage(t, ts.URL, historyRoutePath)
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200: %s", historyRoutePath, code, body)
	}

	for _, want := range []string{
		// The workspace root and everything a request from this page is made of.
		`data-history-workspace`,
		`data-history-endpoint="` + historyListAPIRoutePath + `"`,
		`data-history-preview-endpoint="` + historyPreviewAPIRoutePath + `"`,
		`data-history-undo-endpoint="` + historyUndoAPIRoutePath + `"`,
		`data-history-csrf="csrf-token"`,
		`data-history-page-size="20"`,
		`data-history-max-page-size="200"`,

		// The filters. The two vocabularies are rendered empty and filled from the
		// list response, so the names in the filter and the names on the rows can
		// only ever come from the same server-side table.
		`data-history-filters`,
		`data-history-path`,
		`data-history-origin-filters`,
		`data-history-class-filters`,
		`data-history-page-size-select`,
		`data-history-refresh`,
		`data-history-clear-filters`,

		// The list, its warnings, and the pager.
		`data-history-status`,
		`data-history-warnings`,
		`data-history-list`,
		`data-history-pager`,
		`data-history-previous`,
		`data-history-range`,
		`data-history-next`,

		// The restore confirmation. Every one of these is reachable only after a
		// preview, which is what the consent checkbox and the confirm button are
		// attached to.
		`data-history-restore`,
		`data-history-restore-summary`,
		`data-history-restore-facts`,
		`data-history-restore-changes`,
		`data-history-restore-conflicts`,
		`data-history-restore-conflict-list`,
		`data-history-restore-consent`,
		`data-history-restore-confirm`,
		`data-history-restore-cancel`,
	} {
		if !namesAttribute(body, want) {
			t.Fatalf("history page is missing %q", want)
		}
	}

	// The published bounds have to be the ones the list route enforces, or the
	// page offers page sizes that come back refused.
	if !strings.Contains(body, `<option value="200">200</option>`) {
		t.Fatal("the page size control does not offer the server's own ceiling")
	}
}

// The page is a read. Every write on this surface is one of the two APIs, each of
// which checks the token; a page route that accepted a POST would be a third door
// with no such check.
func TestHistoryPage_AnswersOnlyAGet(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, nil, nil, Options{CSRFToken: "csrf-token"})

	resp, err := http.Post(ts.URL+historyRoutePath, "application/json", strings.NewReader("{}")) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("POST %s: %v", historyRoutePath, err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST %s = %d, want %d: %s", historyRoutePath, resp.StatusCode, http.StatusMethodNotAllowed, body)
	}
	if allow := resp.Header.Get("Allow"); allow != http.MethodGet {
		t.Fatalf("Allow = %q, want %q", allow, http.MethodGet)
	}
}

// The route is registered on a prefix, so without the exact-path check
// /history/anything would render this page — and a recovery surface reachable
// under paths nobody audited is a surface whose guard coverage is a guess.
func TestHistoryPage_IsNotServedUnderAnotherPath(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, nil, nil, Options{CSRFToken: "csrf-token"})

	code, body := getHistoryPage(t, ts.URL, historyRoutePath+"/entry-1")
	if code != http.StatusNotFound {
		t.Fatalf("GET %s/entry-1 = %d, want 404: %s", historyRoutePath, code, body)
	}
}

// The nav link and the page agree about who may see this surface. A link offered
// where the route refuses is a dead end, and a route offered with no link is a
// surface nobody finds.
func TestHistoryPage_IsLinkedFromEveryOtherPage(t *testing.T) {
	ts, _ := newHTTPServerForFilesWithOptions(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\nBody marker.\n"),
	}, nil, Options{CSRFToken: "csrf-token"})

	for _, path := range []string{"/", "/library", historyRoutePath} {
		code, body := getHistoryPage(t, ts.URL, path)
		if code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200: %s", path, code, body)
		}
		if !strings.Contains(body, `href="`+historyRoutePath+`"`) {
			t.Fatalf("%s does not link to the history workspace", path)
		}
	}
}
