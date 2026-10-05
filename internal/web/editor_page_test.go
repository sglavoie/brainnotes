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
		`data-source-create-endpoint="` + sourceCreateAPIRoutePath + `"`,
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
		`data-source-draft-status`,
		`data-source-recovery`,
		`data-source-recovery-heading`,
		`data-source-recovery-lede`,
		`data-source-recovery-source`,
		`data-source-recovery-restore`,
		`data-source-recovery-recreate`,
		`data-source-recovery-discard`,

		// Attaching: the two staging routes, this vault's own ceiling, and the
		// three controls the panel is read from.
		`data-source-upload-endpoint="` + attachmentUploadAPIRoutePath + `"`,
		`data-source-upload-cancel-endpoint="` + attachmentCancelAPIRoutePath + `"`,
		`data-source-max-attachment-bytes="` + strconv.FormatInt(srv.config().Attachments.MaxFileBytes, 10) + `"`,
		`data-source-attachment-input`,
		`data-source-attachment-choose`,
		`data-source-attachments`,
		`data-source-attachment-list`,

		// Managing: the two destinations the page navigates to, the ten routes it
		// calls, and every control that calls one.
		`data-source-editor-path="` + editorRoutePath + `"`,
		`data-source-home-path="` + editorHomeRoutePath + `"`,
		`data-source-capture-endpoint="` + editorCaptureRoutePath + `"`,
		`data-source-note-create-endpoint="` + editorNoteCreateRoutePath + `"`,
		`data-source-rename-endpoint="` + editorNoteRenameRoutePath + `"`,
		`data-source-move-endpoint="` + editorNoteMoveRoutePath + `"`,
		`data-source-trash-endpoint="` + editorNoteTrashRoutePath + `"`,
		`data-source-frontmatter-set-endpoint="` + editorFrontmatterSetRoutePath + `"`,
		`data-source-frontmatter-unset-endpoint="` + editorFrontmatterUnsetRoutePath + `"`,
		`data-source-pin-set-endpoint="` + notePinSetAPIRoutePath + `"`,
		`data-source-pin-remove-endpoint="` + notePinRemoveAPIRoutePath + `"`,
		`data-source-undo-endpoint="` + editorUndoRoutePath + `"`,
		`data-source-manage`,
		`data-source-manage-status`,
		`data-source-gate`,
		`data-source-gate-message`,
		`data-source-gate-save`,
		`data-source-gate-proceed`,
		`data-source-gate-cancel`,
		`data-source-rename-title`,
		`data-source-rename`,
		`data-source-move-folder`,
		`data-source-move`,
		`data-source-trash`,
		`data-source-property-name`,
		`data-source-property-value`,
		`data-source-property-set`,
		`data-source-property-unset`,
		`data-source-pin`,
		`data-source-unpin`,
		`data-source-capture-text`,
		`data-source-capture`,
		`data-source-note-create-target`,
		`data-source-note-create`,
		`data-source-undo`,
	} {
		if !namesAttribute(body, want) {
			t.Fatalf("editor page is missing %q", want)
		}
	}
}

// namesAttribute reports whether the page names this attribute, rather than
// merely containing its letters.
//
// Nearly every bare name in the list above is a strict prefix of another
// attribute the same document publishes — `data-source-rename` of
// `data-source-rename-endpoint`, `data-source-gate` of
// `data-source-gate-message`, `data-source-pin` of
// `data-source-pin-set-endpoint`. A plain substring check is therefore satisfied
// by the endpoint asserted a few lines above it, and stays green with the
// Rename, Move, Trash, Pin, Capture, Create or Undo *button* deleted outright,
// which is the whole failure this list exists to catch.
//
// An attribute name ends wherever a character that cannot be part of one
// follows it — whitespace, the tag's `>`, or its own `=`. Entries that already
// spell a value out are matched as written.
func namesAttribute(body, want string) bool {
	if strings.Contains(want, `="`) {
		return strings.Contains(body, want)
	}
	for at := 0; ; {
		found := strings.Index(body[at:], want)
		if found < 0 {
			return false
		}
		after := at + found + len(want)
		if after >= len(body) || !isAttributeNameByte(body[after]) {
			return true
		}
		at = after
	}
}

func isAttributeNameByte(c byte) bool {
	return c == '-' || c == '_' || c == ':' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// TestNamesAttributeRefusesAPrefixOfAnotherAttribute pins the check above,
// because a presence check that cannot fail is worse than no presence check: the
// list it guards is long, every entry in it looks alike, and a silently
// permissive match would be discovered only by a control going inert in
// production.
func TestNamesAttributeRefusesAPrefixOfAnotherAttribute(t *testing.T) {
	// The newline in the opening tag is not decoration: the real page spreads the
	// editor panel's attributes over lines (templates/editor.html), and the first
	// version of this helper terminated a name on a space, a `>` or an `=` only —
	// so it was `data-source-editor`, alone at the end of its line, that caught it.
	const page = `<article data-source-editor
  data-source-editor-path="/edit">` +
		`<section data-source-gate hidden><p data-source-gate-message></p>` +
		`<button data-source-rename-endpoint="/x" data-source-move>Move</button>`
	for want, expected := range map[string]bool{
		"data-source-editor":               true,  // present, followed by a newline
		"data-source-editor-path":          true,  // present, followed by `=`
		"data-source-gate":                 true,  // present, followed by a space
		"data-source-gate-message":         true,  // present, followed by `>`
		"data-source-move":                 true,  // present, followed by `>`
		"data-source-rename":               false, // only ever `-endpoint`
		"data-source-rename-endpoint":      true,  // present, followed by `=`
		`data-source-rename-endpoint="/x"`: true,
		"data-source-trash":                false, // not on this page at all
	} {
		if got := namesAttribute(page, want); got != expected {
			t.Errorf("namesAttribute(%q) = %v, want %v", want, got, expected)
		}
	}
}

// TestEditorPagePublishesServedRoutes is what keeps the four copies of each
// management route in step: the literal in editor_page.go, the literal the mux
// is registered from, the literal the handler re-checks its own path against,
// and the attribute the page publishes.
//
// A shared constant could not make this check, because a renamed route would
// rename both ends of it at once and the page would go on publishing a path
// that reaches nothing. This asks a *real server* for every route the page
// names and fails on a 404 — which is the one answer that means "no handler".
// Every other refusal (405 for the wrong method, 400 for a missing field, 403
// for a missing CSRF token) is a handler answering, which is all this is about.
func TestEditorPagePublishesServedRoutes(t *testing.T) {
	ts, _, _ := newSourceTestServer(t, Options{})

	_, body := getEditorPage(t, ts.URL, "?key=alpha")

	// Read back from the page rather than from the constants: a route published
	// as one string and registered as another is exactly the drift under test,
	// and asserting the constants would only prove they equal themselves.
	for _, attribute := range []string{
		"data-source-read-endpoint",
		"data-source-preview-endpoint",
		"data-source-save-endpoint",
		"data-source-create-endpoint",
		"data-source-search-endpoint",
		"data-source-upload-endpoint",
		"data-source-upload-cancel-endpoint",
		"data-source-editor-path",
		"data-source-home-path",
		"data-source-capture-endpoint",
		"data-source-note-create-endpoint",
		"data-source-rename-endpoint",
		"data-source-move-endpoint",
		"data-source-trash-endpoint",
		"data-source-frontmatter-set-endpoint",
		"data-source-frontmatter-unset-endpoint",
		"data-source-pin-set-endpoint",
		"data-source-pin-remove-endpoint",
		"data-source-undo-endpoint",
	} {
		route := publishedAttribute(t, body, attribute)
		if route == "" {
			t.Fatalf("%s is published empty", attribute)
		}
		resp, err := http.Get(ts.URL + route) // #nosec G107 -- test-only local listener URL
		if err != nil {
			t.Fatalf("GET %s (%s): %v", route, attribute, err)
		}
		_ = readResponseBody(t, resp)
		if resp.StatusCode == http.StatusNotFound {
			t.Fatalf("%s publishes %q, which this server does not serve", attribute, route)
		}
	}
}

// publishedAttribute reads one attribute's value out of the served HTML. The
// page is the subject here, so the value has to come from the bytes a browser
// receives rather than from the struct that rendered them.
func publishedAttribute(t *testing.T, body, attribute string) string {
	t.Helper()

	marker := attribute + `="`
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("editor page does not publish %s", attribute)
	}
	rest := body[start+len(marker):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		t.Fatalf("%s is unterminated in the served page", attribute)
	}
	return rest[:end]
}

// TestEditorPage_PublishesAVaultIdentity pins the field the browser keys its
// local drafts on.
//
// It is asserted as "non-empty and prefixed" rather than as a literal because
// the value is derived from the vault (see vaultIdentity) and a test that pinned
// one string would either duplicate that derivation or force it to be constant.
// What has to hold is that the page publishes *something* naming this vault: an
// empty attribute silently turns every draft key into note-plus-tab, which is
// the same key for `Alpha.md` in every vault this origin has ever served.
func TestEditorPage_PublishesAVaultIdentity(t *testing.T) {
	ts, srv, _ := newSourceTestServer(t, Options{})

	_, body := getEditorPage(t, ts.URL, "?key=alpha")
	identity := srv.vaultIdentity()
	if identity == "" {
		t.Fatal("vaultIdentity() is empty")
	}
	if !strings.HasPrefix(identity, enrolledVaultIdentityPrefix) &&
		!strings.HasPrefix(identity, locatedVaultIdentityPrefix) {
		t.Fatalf("vaultIdentity() = %q, want one of the two identity spaces", identity)
	}
	if !strings.Contains(body, `data-source-vault-id="`+identity+`"`) {
		t.Fatalf("editor page does not publish the vault identity %q", identity)
	}
}

// TestEditorPage_RecoveryPanelStartsHidden is the draft half of the same rule the
// conflict panel's test states: the panel is filled from local storage, so a page
// that shipped it visible would open every edit with an empty "unsaved text kept
// in this browser" claim over a note nobody has touched.
func TestEditorPage_RecoveryPanelStartsHidden(t *testing.T) {
	ts, _, _ := newSourceTestServer(t, Options{})

	_, body := getEditorPage(t, ts.URL, "?key=alpha")
	if !strings.Contains(body, `data-source-recovery hidden`) {
		t.Fatal("the recovery panel is not hidden on a freshly served editor page")
	}
	// The recreate button is the one control inside it that must also start
	// hidden: it writes a *new* note, and it is only ever the right answer for a
	// target that is gone.
	if !strings.Contains(body, `data-source-recovery-recreate hidden`) {
		t.Fatal("the recreate button is not hidden on a freshly served editor page")
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
	if !strings.Contains(body, `href="/history?path=Alpha.md" data-note-history`) {
		t.Fatal("writable note page does not link to its history")
	}

	replica, _, _ := newSourceTestServer(t, Options{ReadOnly: true, StateDir: t.TempDir()})
	_, replicaBody := getNotePage(t, replica.URL, "/n/alpha")
	if strings.Contains(replicaBody, "/edit?key=") {
		t.Fatal("read-only note page links to an editor it refuses")
	}
	if strings.Contains(replicaBody, "data-note-history") {
		t.Fatal("read-only note page links to history it refuses")
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
