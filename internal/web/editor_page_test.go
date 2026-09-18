package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

func getEditorPage(t *testing.T, baseURL, query string) (int, string) {
	t.Helper()

	resp, err := http.Get(baseURL + editorRoutePath + query) // #nosec G107 -- test-only local listener URL
	if err != nil {
		t.Fatalf("GET %s%s: %v", editorRoutePath, query, err)
	}
	return resp.StatusCode, readResponseBody(t, resp)
}

// TestEditorPage_RendersTheEndpointsAndIdentityTheEditorNeeds is the Go half of
// the contract between this template and the jsdom fixture in
// static_src/source-editor.test-helpers.ts. That fixture is a second expression
// of this markup — a Node process cannot render a Go template — so every
// attribute the editor's TypeScript reads has to be pinned *here*, against the
// page a browser is actually served. An attribute asserted only in the fixture
// can be renamed in this template with the whole suite staying green and the
// control it names going inert in production, which is exactly what the conflict
// panel's four attributes were before this list grew to hold them.
func TestEditorPage_RendersTheEndpointsAndIdentityTheEditorNeeds(t *testing.T) {
	ts, srv, _ := newSourceTestServer(t, Options{})

	status, body := getEditorPage(t, ts.URL, "?key=alpha")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}

	for _, want := range []string{
		`data-source-editor`,
		`data-source-key="alpha"`,
		`data-source-csrf="` + srv.csrfToken + `"`,
		`data-source-rel-path="Alpha.md"`,
		`data-source-read-endpoint="` + sourceAPIRoutePath + `"`,
		`data-source-preview-endpoint="` + sourcePreviewAPIRoutePath + `"`,
		`data-source-save-endpoint="` + sourceSaveAPIRoutePath + `"`,
		`data-source-search-endpoint="` + searchAPIRoutePath + `"`,
		`data-source-max-bytes="` + strconv.Itoa(noteops.MaxSourceBytes) + `"`,
		`data-source-surface`,
		`data-source-save`,
		`data-source-preview`,
		`data-source-status`,
		`data-source-preview-status`,
		`data-source-panes`,
		`data-source-tab="edit"`,
		`data-source-tab="preview"`,
		`data-source-pane="edit"`,
		`data-source-pane="preview"`,
		`data-source-command="bold"`,
		`data-source-command="italic"`,
		`data-source-command="code"`,
		`data-source-command="heading" data-source-heading-level="1"`,
		`data-source-command="bullet"`,
		`data-source-command="link"`,
		`data-source-command="wikilink"`,
		`data-source-conflict`,
		`data-source-conflict-source`,
		`data-source-conflict-overwrite`,
		`data-source-conflict-discard`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("editor page is missing %q", want)
		}
	}
}

// TestEditorPage_ConflictPanelStartsHidden pins the one piece of that markup
// whose *initial state* is behaviour rather than decoration: the panel holds the
// vault's version of a note, so a page that shipped it visible would open every
// edit with an empty "this note changed on disk" warning.
func TestEditorPage_ConflictPanelStartsHidden(t *testing.T) {
	ts, _, _ := newSourceTestServer(t, Options{})

	_, body := getEditorPage(t, ts.URL, "?key=alpha")
	if !strings.Contains(body, `data-source-conflict hidden`) {
		t.Fatal("the conflict panel is not hidden on a freshly served editor page")
	}
}

// TestEditorPage_DoesNotCarryTheNoteSource is the page's half of the revision
// contract. The editor's bytes have to come from /api/source, which hands out the
// revision those exact bytes hash to; a copy rendered into the HTML would be a
// second read with nothing tying it to the revision a save is planned against.
func TestEditorPage_DoesNotCarryTheNoteSource(t *testing.T) {
	ts, _, _ := newSourceTestServer(t, Options{})

	_, body := getEditorPage(t, ts.URL, "?key=alpha")
	if strings.Contains(body, "Alpha body, see") {
		t.Fatal("editor page rendered the note's source into the document")
	}
}

func TestEditorPage_RequiresExactlyOneKey(t *testing.T) {
	ts, _, _ := newSourceTestServer(t, Options{})

	for name, query := range map[string]string{
		"absent": "",
		"blank":  "?key=%20",
		"two":    "?key=alpha&key=beta",
	} {
		t.Run(name, func(t *testing.T) {
			status, _ := getEditorPage(t, ts.URL, query)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", status)
			}
		})
	}
}

func TestEditorPage_UnknownNoteIsNotFound(t *testing.T) {
	ts, _, _ := newSourceTestServer(t, Options{})

	status, _ := getEditorPage(t, ts.URL, "?key="+url.QueryEscape("nope"))
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
}

// TestEditorPage_ReadOnlyReplicaRefusesIt pins that the page is governed by the
// same allowlist as the four /api/source routes it calls. A replica that served
// the page would offer an editing affordance for saves it refuses.
func TestEditorPage_ReadOnlyReplicaRefusesIt(t *testing.T) {
	ts, _, _ := newSourceTestServer(t, Options{ReadOnly: true, StateDir: t.TempDir()})

	status, body := getEditorPage(t, ts.URL, "?key=alpha")
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", status)
	}
	if !strings.Contains(body, "read-only replica") {
		t.Fatalf("refusal did not name the replica: %s", body)
	}
}

// TestNotePage_LinksToTheEditorOnlyWhereWritesAreAvailable covers the entry
// point rather than the page: a mode that refuses the editor must not render a
// link to it.
func TestNotePage_LinksToTheEditorOnlyWhereWritesAreAvailable(t *testing.T) {
	writable, _, _ := newSourceTestServer(t, Options{})
	_, body := getNotePage(t, writable.URL, "/n/alpha")
	// The attribute is pinned alongside the href because it is the selector
	// internal/web/e2e/editor.spec.js opens the editor with, and that suite is
	// deliberately outside `just ci` — renaming it here would otherwise break the
	// only browser coverage the editor has with every gate still green.
	if !strings.Contains(body, `href="/edit?key=alpha" data-source-editor-launch`) {
		t.Fatal("writable note page does not link to the editor")
	}

	replica, _, _ := newSourceTestServer(t, Options{ReadOnly: true, StateDir: t.TempDir()})
	_, replicaBody := getNotePage(t, replica.URL, "/n/alpha")
	if strings.Contains(replicaBody, "/edit?key=") {
		t.Fatal("read-only note page links to an editor it refuses")
	}
}

func getNotePage(t *testing.T, baseURL, path string) (int, string) {
	t.Helper()

	resp, err := http.Get(baseURL + path) // #nosec G107 -- test-only local listener URL
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp.StatusCode, readResponseBody(t, resp)
}
