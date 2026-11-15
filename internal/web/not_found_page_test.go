package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// getAccepting fetches path saying what the client accepts, and returns the
// status, the content type and the body.
func getAccepting(t *testing.T, ts *httptest.Server, path, accept string) (int, string, string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", accept)
	response, err := ts.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, response.Header.Get("Content-Type"), readResponseBody(t, response)
}

func newNotFoundServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Concepts/Deliberate Practice.md": []byte("Focused practice.\n"),
	})
	return ts
}

func TestMissingNotePageOffersRecovery(t *testing.T) {
	ts := newNotFoundServer(t)
	status, contentType, body := getAccepting(t, ts, "/n/concepts/delibrate%20pratice", "text/html,*/*")
	if status != http.StatusNotFound || !strings.HasPrefix(contentType, "text/html") {
		t.Fatalf("missing note: %d %s", status, contentType)
	}
	for _, want := range []string{
		"No note here",
		`href="/n/concepts/deliberate%20practice"`,
		`href="/library?q=delibrate%20pratice"`,
		`href="/new?title=delibrate%20pratice"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing note page lacks %q", want)
		}
	}
}

// The wanted title is attacker-controlled text from the address.
func TestMissingNotePageEscapesTheWantedTitle(t *testing.T) {
	ts := newNotFoundServer(t)
	_, _, body := getAccepting(t, ts, "/n/%3Cscript%3Ealert(1)%3C%2Fscript%3E", "text/html")
	if strings.Contains(body, "<script>alert(1)") {
		t.Fatal("the missing title reached the page unescaped")
	}
}

func TestUnknownAddressGetsAPageAndAClientGetsPlainText(t *testing.T) {
	ts := newNotFoundServer(t)
	status, _, body := getAccepting(t, ts, "/nowhere", "text/html")
	if status != http.StatusNotFound || !strings.Contains(body, "Nothing here") {
		t.Fatalf("unknown address: %d %q", status, body)
	}

	// A client that did not ask for a page keeps the plain body.
	status, contentType, body := getAccepting(t, ts, "/n/missing", "application/json")
	if status != http.StatusNotFound || strings.HasPrefix(contentType, "text/html") || strings.Contains(body, "<html") {
		t.Fatalf("non-navigation 404: %d %s %q", status, contentType, body)
	}
}
