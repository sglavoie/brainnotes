package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// failingNoteSource is a note source whose every load fails, the one way a
// snapshot itself (rather than a single note in it) can be unavailable.
type failingNoteSource struct{}

func (failingNoteSource) loadNotes(*vault.Vault, noteLoader, time.Duration, map[string]struct{}) (loadedVault, error) {
	return loadedVault{}, errors.New("walk /private/vault: permission denied")
}

// A JSON route that cannot load the vault answers in JSON too: a client reading
// the management error shape gets a code and a message rather than a plain-text
// body it cannot parse. The cause stays in the server; only the public message
// is sent.
func TestJSONRoutesReportSnapshotFailuresAsManagementErrors(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "Alpha.md", "---\ntitle: Alpha\n---\nBody\n")
	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")
	srv, err := NewServer(cfg, v, Options{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv.state.source = failingNoteSource{}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	for _, route := range []string{
		"/api/search?q=alpha",
		"/api/facets",
		"/api/graph.json",
		queryValidateAPIRoutePath + "?expr=tag:a",
		queryCompletionsAPIRoutePath + "?expr=tag:",
	} {
		t.Run(route, func(t *testing.T) {
			resp, err := http.Get(ts.URL + route) // #nosec G107 -- test-only local URL
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			body := readResponseBody(t, resp)
			if resp.StatusCode != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500; body=%q", resp.StatusCode, body)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Fatalf("content type = %q, want JSON", ct)
			}
			payload := decodeManagementError(t, body)
			if payload.Code != string(noteops.CodeInternal) || payload.Message != "could not load the vault" {
				t.Fatalf("payload = %#v", payload)
			}
			if strings.Contains(body, "permission denied") {
				t.Fatalf("the cause reached the client: %q", body)
			}
		})
	}
}
