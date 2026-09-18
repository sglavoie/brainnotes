package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

const sourceTestToken = "source-csrf-token"

// sourceTestNote is stored the way somebody typed it: keys out of alphabetical
// order, a link into the vault, and no trailing newline. A route that repaired
// any of that would be editing a person's file while serving it.
const sourceTestNote = "---\ntitle: Alpha\nstatus: draft\n---\n\nAlpha body, see [[Beta]]."

type sourceReadPayload struct {
	OK       bool   `json:"ok"`
	Key      string `json:"key"`
	RelPath  string `json:"rel_path"`
	Revision string `json:"revision"`
	Content  string `json:"content"`
}

type sourceSavePayload struct {
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
	RelPath   string `json:"rel_path"`
	Revision  string `json:"revision"`
	Changed   bool   `json:"changed"`
	Created   bool   `json:"created"`
	Replayed  bool   `json:"replayed"`
	JournalID string `json:"journal_id"`
}

type sourceErrorPayload struct {
	OK       bool   `json:"ok"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Revision string `json:"revision"`
	Content  string `json:"content"`
	Changed  bool   `json:"changed"`
	// Attachments is on the error payload for one arm only: a snapshot failure
	// committed its uploads as surely as a success did, and the browser stops
	// owing an upload id on the strength of this field.
	Attachments []noteops.SavedAttachment `json:"attachments"`
}

// newSourceTestServer builds a vault and a server over it.
//
// customize runs on the configuration after the fixture's own values and before
// the vault is opened, which is how a test asks for a narrower attachment limit
// or a different attachments directory without a second fixture.
func newSourceTestServer(t *testing.T, opts Options, customize ...func(*config.Config)) (*httptest.Server, *Server, string) {
	t.Helper()

	root := t.TempDir()
	writeTestFile(t, root, "Alpha.md", sourceTestNote)
	writeTestFile(t, root, "Beta.md", "---\ntitle: Beta\n---\n\nBeta body.\n")

	cfg := sourceTestConfig(root)
	for _, apply := range customize {
		apply(cfg)
	}
	v, err := vault.Open(cfg)
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	if opts.CSRFToken == "" {
		opts.CSRFToken = sourceTestToken
	}
	srv, err := NewServer(cfg, v, opts)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, srv, root
}

// sourceTestConfig is the vault configuration the source and attachment routes
// are exercised against.
//
// The ignore list and the templates directory are declared because the source
// routes honour them: they are two of the locations vault.Walk never visits, and
// a fixture that left them unset would exercise a laxer rule than a real vault's.
// The attachment settings are declared for the same reason — a loaded
// configuration always carries both, so a fixture that left them zero would have
// the upload routes fail on a store no real vault could produce.
func sourceTestConfig(root string) *config.Config {
	disabled := false
	return &config.Config{
		Vault:     root,
		Ignore:    []string{"Archive"},
		Templates: config.TemplatesConfig{Dir: "Templates"},
		Serve:     config.ServeConfig{Addr: "127.0.0.1:0", OpenBrowser: &disabled},
		Attachments: config.AttachmentsConfig{
			Dir:          config.DefaultAttachmentsDir,
			MaxFileBytes: config.MaxAttachmentFileBytes,
		},
	}
}

// restartTestServer builds a second server over a vault that already exists,
// which is how a test observes what a process does on startup with an earlier
// session's leftovers still on disk.
func restartTestServer(t *testing.T, root string, customize ...func(*Options)) *Server {
	t.Helper()

	cfg := sourceTestConfig(root)
	v, err := vault.Open(cfg)
	if err != nil {
		t.Fatalf("reopen vault: %v", err)
	}
	opts := Options{CSRFToken: sourceTestToken}
	for _, apply := range customize {
		apply(&opts)
	}
	srv, err := NewServer(cfg, v, opts)
	if err != nil {
		t.Fatalf("restart NewServer: %v", err)
	}
	return srv
}

func getSourceDocument(t *testing.T, ts *httptest.Server, key string) *http.Response {
	t.Helper()
	resp, err := ts.Client().Get(ts.URL + sourceAPIRoutePath + "?key=" + url.QueryEscape(key))
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	return resp
}

func postSourceRoute(t *testing.T, ts *httptest.Server, route string, form url.Values, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+route, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if token != "" {
		req.Header.Set(csrfHeaderName, token)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", route, err)
	}
	return resp
}

func decodeSourceSave(t *testing.T, resp *http.Response) sourceSavePayload {
	t.Helper()
	defer resp.Body.Close()

	var payload sourceSavePayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode save payload: %v", err)
	}
	if resp.StatusCode != http.StatusOK || !payload.OK {
		t.Fatalf("status = %d, payload = %+v, want a 200 success", resp.StatusCode, payload)
	}
	return payload
}

func decodeSourceError(t *testing.T, resp *http.Response, wantStatus int, wantCode noteops.ErrorCode) sourceErrorPayload {
	t.Helper()
	defer resp.Body.Close()

	var payload sourceErrorPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode error payload: %v", err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d (payload %+v)", resp.StatusCode, wantStatus, payload)
	}
	if payload.OK {
		t.Fatalf("an error payload reported ok: %+v", payload)
	}
	if noteops.ErrorCode(payload.Code) != wantCode {
		t.Fatalf("code = %q, want %q (payload %+v)", payload.Code, wantCode, payload)
	}
	return payload
}

func readSourceNote(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "Alpha.md"))
	if err != nil {
		t.Fatalf("read Alpha.md: %v", err)
	}
	return string(data)
}

func TestSourceReadServesTheStoredBytesAndTheirRevision(t *testing.T) {
	ts, _, _ := newSourceTestServer(t, Options{})

	resp := getSourceDocument(t, ts, "alpha")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var payload sourceReadPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Content != sourceTestNote {
		t.Fatalf("content was not served verbatim:\n%q", payload.Content)
	}
	if want := vault.HashBytes([]byte(sourceTestNote)); payload.Revision != want {
		t.Fatalf("revision = %q, want %q", payload.Revision, want)
	}
	if payload.RelPath != "Alpha.md" || payload.Key != "alpha" {
		t.Fatalf("payload identifies the note as %+v", payload)
	}
}

func TestSourceReadRefusesAMissingKeyAndTheWrongMethod(t *testing.T) {
	ts, _, _ := newSourceTestServer(t, Options{})

	decodeSourceError(t, getSourceDocument(t, ts, ""), http.StatusBadRequest, noteops.CodeInvalidRequest)
	decodeSourceError(t, getSourceDocument(t, ts, "nothing-here"), http.StatusNotFound, noteops.CodeNotFound)

	resp := postSourceRoute(t, ts, sourceAPIRoutePath, url.Values{}, sourceTestToken)
	decodeSourceError(t, resp, http.StatusMethodNotAllowed, noteops.CodeInvalidRequest)
	if allow := resp.Header.Get("Allow"); allow != http.MethodGet {
		t.Fatalf("Allow = %q, want GET", allow)
	}
}

func TestSourceSaveWritesTheSubmittedBytesAndReportsTheNewRevision(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})
	edited := "---\ntitle: Alpha\nstatus: done\n---\n\nAlpha body, edited, still [[Beta]]."

	form := url.Values{}
	form.Set("key", "alpha")
	form.Set("content", edited)
	form.Set("revision", vault.HashBytes([]byte(sourceTestNote)))
	payload := decodeSourceSave(t, postSourceRoute(t, ts, sourceSaveAPIRoutePath, form, sourceTestToken))

	if got := readSourceNote(t, root); got != edited {
		t.Fatalf("the note on disk is not what was submitted:\n%q", got)
	}
	if !payload.Changed || payload.Created || payload.Replayed {
		t.Fatalf("payload = %+v, want a plain change", payload)
	}
	if want := vault.HashBytes([]byte(edited)); payload.Revision != want {
		t.Fatalf("revision = %q, want %q", payload.Revision, want)
	}
	if payload.JournalID == "" {
		t.Fatal("the response names no undo entry")
	}
	if !strings.Contains(payload.Message, "Saved") {
		t.Fatalf("message = %q", payload.Message)
	}
}

// TestSourceSaveRefusalCarriesTheCurrentSource is the stale case at the HTTP
// boundary. A 409 with a message alone would leave an editor with nothing to do
// but tell somebody to copy their draft somewhere and reload, so the refusal
// carries the note's current bytes and revision and a merge becomes possible.
func TestSourceSaveRefusalCarriesTheCurrentSource(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})

	form := url.Values{}
	form.Set("key", "alpha")
	form.Set("content", "---\ntitle: Alpha\n---\n\nmy draft.\n")
	form.Set("revision", vault.HashBytes([]byte("a version this vault never held")))
	payload := decodeSourceError(t,
		postSourceRoute(t, ts, sourceSaveAPIRoutePath, form, sourceTestToken),
		http.StatusConflict, noteops.CodeConflict)

	if payload.Content != sourceTestNote {
		t.Fatalf("the refusal did not carry the current source:\n%q", payload.Content)
	}
	if want := vault.HashBytes([]byte(sourceTestNote)); payload.Revision != want {
		t.Fatalf("revision = %q, want the current %q", payload.Revision, want)
	}
	if payload.Changed {
		t.Fatal("a refused save reported the bytes as changed")
	}
	if got := readSourceNote(t, root); got != sourceTestNote {
		t.Fatalf("a refused save wrote anyway:\n%q", got)
	}
}

// TestSourceSaveReportsWhatItDidNotDo covers the successes that are not writes:
// a resubmission the note already agrees with, a replayed save id, and a
// replayed create. Wording any of them "Saved" — or "Created" — is the sentence
// that hides a duplicate.
func TestSourceSaveReportsWhatItDidNotDo(t *testing.T) {
	ts, _, _ := newSourceTestServer(t, Options{})
	edited := "---\ntitle: Alpha\nstatus: done\n---\n\nedited once.\n"
	base := vault.HashBytes([]byte(sourceTestNote))

	form := url.Values{}
	form.Set("key", "alpha")
	form.Set("content", edited)
	form.Set("revision", base)
	form.Set("save_id", "editor-1")
	first := decodeSourceSave(t, postSourceRoute(t, ts, sourceSaveAPIRoutePath, form, sourceTestToken))

	// The same submission again, with the base revision the first one replaced.
	second := decodeSourceSave(t, postSourceRoute(t, ts, sourceSaveAPIRoutePath, form, sourceTestToken))
	if second.Changed {
		t.Fatalf("the resubmission claimed to write: %+v", second)
	}
	if second.Revision != first.Revision {
		t.Fatalf("revision = %q, want %q", second.Revision, first.Revision)
	}
	if strings.Contains(second.Message, "Saved") {
		t.Fatalf("message = %q, want it to say the content was already there", second.Message)
	}

	// A different edit under the same save id is the one duplicate that must be
	// refused rather than answered.
	form.Set("content", "---\ntitle: Alpha\n---\n\na different edit.\n")
	form.Set("revision", first.Revision)
	decodeSourceError(t,
		postSourceRoute(t, ts, sourceSaveAPIRoutePath, form, sourceTestToken),
		http.StatusConflict, noteops.CodeConflict)

	// A replayed create carries both facts at once: the record says the note was
	// created, and this call is the resubmission that did not create it. The
	// answer has to be the second one, or the message is the same lie as "Saved".
	create := url.Values{}
	create.Set("rel_path", "Drafts/Keyed.md")
	create.Set("content", "---\ntitle: Keyed\n---\n\nKeyed body.\n")
	create.Set("save_id", "editor-create-1")
	created := decodeSourceSave(t, postSourceRoute(t, ts, sourceCreateAPIRoutePath, create, sourceTestToken))
	if !created.Created || !created.Changed {
		t.Fatalf("the first create = %+v, want a created change", created)
	}
	replayed := decodeSourceSave(t, postSourceRoute(t, ts, sourceCreateAPIRoutePath, create, sourceTestToken))
	if !replayed.Replayed || replayed.Changed {
		t.Fatalf("the resubmitted create = %+v, want replayed and unchanged", replayed)
	}
	if strings.Contains(replayed.Message, "Created") {
		t.Fatalf("message = %q, want it to say the save id was already used", replayed.Message)
	}
}

func TestSourceRoutesRefuseInvalidFrontmatter(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})
	broken := "---\ntitle: [unterminated\n---\n\nbody\n"

	save := url.Values{}
	save.Set("key", "alpha")
	save.Set("content", broken)
	save.Set("revision", vault.HashBytes([]byte(sourceTestNote)))
	decodeSourceError(t,
		postSourceRoute(t, ts, sourceSaveAPIRoutePath, save, sourceTestToken),
		http.StatusBadRequest, noteops.CodeInvalidRequest)

	preview := url.Values{}
	preview.Set("content", broken)
	decodeSourceError(t,
		postSourceRoute(t, ts, sourcePreviewAPIRoutePath, preview, sourceTestToken),
		http.StatusBadRequest, noteops.CodeInvalidRequest)

	if got := readSourceNote(t, root); got != sourceTestNote {
		t.Fatalf("a refused save wrote anyway:\n%q", got)
	}
}

// TestSourcePreviewRendersWithoutWriting proves the preview is a read. It also
// checks the wikilink resolved against the real graph, because a preview that
// renders links differently from the saved note is a preview of another note.
func TestSourcePreviewRendersWithoutWriting(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})

	form := url.Values{}
	form.Set("content", "---\ntitle: Draft\n---\n\n## Heading\n\nA link to [[Beta]] and one to [[Nowhere]].\n")
	resp := postSourceRoute(t, ts, sourcePreviewAPIRoutePath, form, sourceTestToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var payload struct {
		OK           bool   `json:"ok"`
		RenderedHTML string `json:"rendered_html"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !payload.OK || !strings.Contains(payload.RenderedHTML, "Heading") {
		t.Fatalf("rendered_html = %q", payload.RenderedHTML)
	}
	// The frontmatter is parsed off rather than rendered as a horizontal rule.
	if strings.Contains(payload.RenderedHTML, "title: Draft") {
		t.Fatalf("the preview rendered the properties block as prose: %q", payload.RenderedHTML)
	}
	if got := readSourceNote(t, root); got != sourceTestNote {
		t.Fatalf("a preview wrote to the vault:\n%q", got)
	}
}

func TestSourceCreateWritesANewNoteAndRefusesAnExistingPath(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})
	content := "---\ntitle: Delta\n---\n\nDelta body.\n"

	form := url.Values{}
	form.Set("rel_path", "Drafts/Delta.md")
	form.Set("content", content)
	payload := decodeSourceSave(t, postSourceRoute(t, ts, sourceCreateAPIRoutePath, form, sourceTestToken))
	if !payload.Created || !payload.Changed {
		t.Fatalf("payload = %+v, want a created change", payload)
	}
	stored, err := os.ReadFile(filepath.Join(root, "Drafts", "Delta.md"))
	if err != nil {
		t.Fatalf("read the created note: %v", err)
	}
	if string(stored) != content {
		t.Fatalf("the created note is not what was submitted:\n%q", stored)
	}

	// The same create again converges: the destination holds exactly these bytes,
	// so this is the create that already happened rather than a second one.
	again := decodeSourceSave(t, postSourceRoute(t, ts, sourceCreateAPIRoutePath, form, sourceTestToken))
	if again.Changed {
		t.Fatalf("the repeated create claimed to write: %+v", again)
	}
	if again.Revision != payload.Revision {
		t.Fatalf("revision = %q, want %q", again.Revision, payload.Revision)
	}

	// A create whose destination holds *different* bytes is the one that must be
	// refused, because succeeding would replace a note nobody asked about.
	form.Set("content", "---\ntitle: Delta\n---\n\nsomebody else's Delta.\n")
	decodeSourceError(t,
		postSourceRoute(t, ts, sourceCreateAPIRoutePath, form, sourceTestToken),
		http.StatusConflict, noteops.CodeConflict)
	if stored, err := os.ReadFile(filepath.Join(root, "Drafts", "Delta.md")); err != nil {
		t.Fatalf("read the created note: %v", err)
	} else if string(stored) != content {
		t.Fatalf("a refused create overwrote the note:\n%q", stored)
	}
}

// TestSourceCreateRefusesAPathThatIsNotAContainedNote covers both halves of the
// location rule over the route: a path that is not a contained Markdown note,
// and one that is but names somewhere vault.Walk never visits. The second half
// matters more here than it reads — a create is the only source request that
// names a brand-new path, so it is the only one that could put a note where no
// query would ever find it and, under `.bnotes/`, where the save's own snapshot
// would commit nothing while the answer reported success.
func TestSourceCreateRefusesAPathThatIsNotAContainedNote(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})

	for _, relPath := range []string{
		"../escape.md", "/etc/passwd", "Notes/plain.txt", "",
		".bnotes/Sneaky.md", ".git/Sneaky.md", "Templates/Daily.md", "Archive/Old.md",
	} {
		form := url.Values{}
		form.Set("rel_path", relPath)
		form.Set("content", "---\ntitle: X\n---\n\nbody\n")
		resp := postSourceRoute(t, ts, sourceCreateAPIRoutePath, form, sourceTestToken)
		payload := sourceErrorPayload{}
		func() {
			defer resp.Body.Close()
			if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
				t.Fatalf("decode: %v", err)
			}
		}()
		code := noteops.ErrorCode(payload.Code)
		if code != noteops.CodeForbidden && code != noteops.CodeInvalidRequest {
			t.Fatalf("rel_path %q answered %d %q, want a refusal", relPath, resp.StatusCode, payload.Code)
		}
		if want := noteops.HTTPStatusForError(&noteops.Error{Code: code}); resp.StatusCode != want {
			t.Fatalf("rel_path %q answered %d for code %q, want %d", relPath, resp.StatusCode, code, want)
		}
	}

	// A refusal has to have written nothing, which is the half a status code
	// cannot show. Only the in-vault paths are checked: the escaping ones name
	// somewhere this assertion has no business reading, and the empty one names
	// the vault root, which of course exists.
	for _, relPath := range []string{
		"Notes/plain.txt", ".bnotes/Sneaky.md", ".git/Sneaky.md", "Templates/Daily.md", "Archive/Old.md",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relPath))); !os.IsNotExist(err) {
			t.Fatalf("the refused create left something at %s: %v", relPath, err)
		}
	}
}

// TestSourceSaveRequiresTheCSRFToken is the management envelope applied to these
// routes. It is asserted here rather than assumed because all three writes are
// new doors into the vault.
func TestSourceSaveRequiresTheCSRFToken(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})

	form := url.Values{}
	form.Set("key", "alpha")
	form.Set("content", "---\ntitle: Alpha\n---\n\nno token.\n")
	form.Set("revision", vault.HashBytes([]byte(sourceTestNote)))
	for _, route := range []string{sourcePreviewAPIRoutePath, sourceSaveAPIRoutePath, sourceCreateAPIRoutePath} {
		decodeSourceError(t, postSourceRoute(t, ts, route, form, ""), http.StatusForbidden, noteops.CodeForbidden)
	}
	if got := readSourceNote(t, root); got != sourceTestNote {
		t.Fatalf("a save without a token wrote anyway:\n%q", got)
	}
}

// TestSourceRoutesAreUnavailableOnAReadOnlyReplica pins the deliberate choice
// recorded on registerSourceRoutes: none of the four is on the replica's
// allowlist, the read included. A replica cannot accept a save, and a route that
// hands out the revision one would need is an editing affordance on a published
// copy.
func TestSourceRoutesAreUnavailableOnAReadOnlyReplica(t *testing.T) {
	// A replica keeps its own state outside the vault it publishes, which
	// validateReplicaOptions requires before the mode resolves at all.
	ts, _, root := newSourceTestServer(t, Options{ReadOnly: true, StateDir: t.TempDir()})

	resp := getSourceDocument(t, ts, "alpha")
	payload := decodeSourceError(t, resp, http.StatusForbidden, noteops.CodeForbidden)
	if !strings.Contains(payload.Message, "read-only replica") {
		t.Fatalf("message = %q, want the allowlist refusal", payload.Message)
	}

	form := url.Values{}
	form.Set("key", "alpha")
	form.Set("content", "---\ntitle: Alpha\n---\n\nreplica write.\n")
	form.Set("revision", vault.HashBytes([]byte(sourceTestNote)))
	form.Set("rel_path", "Drafts/Delta.md")
	for _, route := range []string{sourcePreviewAPIRoutePath, sourceSaveAPIRoutePath, sourceCreateAPIRoutePath} {
		decodeSourceError(t, postSourceRoute(t, ts, route, form, sourceTestToken),
			http.StatusForbidden, noteops.CodeForbidden)
	}
	if got := readSourceNote(t, root); got != sourceTestNote {
		t.Fatalf("a replica wrote to the vault:\n%q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "Drafts")); err == nil {
		t.Fatal("a refused create left a folder behind")
	}
}

// TestSourceSaveWorksOnAHostedVaultWithinItsOwnBound covers the other half of
// the profile decision. Hosted has documentWrites, so the save must work — and
// the remote body bound is a megabyte, which form-encoding a note exceeds long
// before noteops' own content limit, so the route's widening has to be in force
// there rather than only locally.
func TestSourceSaveWorksOnAHostedVaultWithinItsOwnBound(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{Hosted: true})
	edited := "---\ntitle: Alpha\n---\n\n" + strings.Repeat("hosted body. ", 200_000)

	form := url.Values{}
	form.Set("key", "alpha")
	form.Set("content", edited)
	form.Set("revision", vault.HashBytes([]byte(sourceTestNote)))
	if len(form.Encode()) <= remoteMaxRequestBody {
		t.Fatalf("the request is %d bytes, which does not exceed the remote profile bound", len(form.Encode()))
	}
	decodeSourceSave(t, postSourceRoute(t, ts, sourceSaveAPIRoutePath, form, sourceTestToken))
	if got := readSourceNote(t, root); got != edited {
		t.Fatalf("the hosted save did not store the submitted bytes (%d bytes on disk)", len(got))
	}
}

// TestSourceRoutesStayBoundedByTheirOwnLimit is the other side of that
// widening: it moves the bound from the profile to the route rather than
// removing it, so a request over the route's own limit is still refused.
func TestSourceRoutesStayBoundedByTheirOwnLimit(t *testing.T) {
	for _, mode := range []Mode{ModeLocal, ModeHosted} {
		caps := capabilitiesFor(mode)
		for _, route := range []string{sourcePreviewAPIRoutePath, sourceSaveAPIRoutePath, sourceCreateAPIRoutePath} {
			if got := caps.requestBodyLimit(route); got < sourceRequestBodyLimit {
				t.Fatalf("%s mode bounds %s at %d, want at least the route's own %d",
					mode, route, got, sourceRequestBodyLimit)
			}
		}
		if got := caps.requestBodyLimit(sourceAPIRoutePath); got != caps.maxRequestBody {
			t.Fatalf("%s mode widened the read route to %d", mode, got)
		}
	}

	// Hosted is where the widening is the operative bound — its profile allowance
	// is a megabyte, so the route's own number is what a request is held to and
	// the guard answers an over-length one with an honest 413.
	ts, _, root := newSourceTestServer(t, Options{Hosted: true, CSRFToken: sourceTestToken})
	form := url.Values{}
	form.Set("key", "alpha")
	form.Set("content", strings.Repeat("x", sourceRequestBodyLimit+1))
	resp := postSourceRoute(t, ts, sourceSaveAPIRoutePath, form, sourceTestToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
	if got := readSourceNote(t, root); got != sourceTestNote {
		t.Fatalf("an over-limit save wrote anyway:\n%q", got)
	}
}

// TestSourceSaveRefusesABodyOverTheRouteBoundOnALocalVault is the local half of
// the same bound. The local profile allows more than these routes do, so the
// number that refuses is the one requireManagementPost applies, and its refusal
// is a malformed-request 400 rather than the guard's 413. Either way the save
// does not happen, which is the property worth pinning: the widening moved the
// bound to the route, it did not remove it.
func TestSourceSaveRefusesABodyOverTheRouteBoundOnALocalVault(t *testing.T) {
	ts, _, root := newSourceTestServer(t, Options{})

	form := url.Values{}
	form.Set("key", "alpha")
	form.Set("content", strings.Repeat("x", sourceRequestBodyLimit+1))
	decodeSourceError(t, postSourceRoute(t, ts, sourceSaveAPIRoutePath, form, sourceTestToken),
		http.StatusBadRequest, noteops.CodeInvalidRequest)
	if got := readSourceNote(t, root); got != sourceTestNote {
		t.Fatalf("an over-limit save wrote anyway:\n%q", got)
	}
}

// TestSourceSaveErrorTellsALandedSaveFromARefusedOne is the response shape for
// the snapshot-failure case.
//
// internal/mutationmatrix proves the real failure arrives here coded
// snapshot_failed with the bytes on disk; what this pins is what the body then
// says. A 500 with no revision would invite a resubmission planned against a
// revision this save already replaced, which is how an honest snapshot failure
// turns into a lost edit — so the landed revision and changed: true are part of
// the contract, and a refusal before the write must carry neither.
func TestSourceSaveErrorTellsALandedSaveFromARefusedOne(t *testing.T) {
	_, srv, _ := newSourceTestServer(t, Options{})
	req := noteops.SaveSourceRequest{RelPath: "Alpha.md", Content: sourceTestNote}
	landed := &noteops.SaveSourceResult{
		Destination: noteops.PathResolution{RelPath: "Alpha.md"},
		Revision:    vault.HashBytes([]byte(sourceTestNote)),
		Changed:     true,
	}

	recorder := httptest.NewRecorder()
	srv.writeSourceSaveError(recorder, req, landed,
		noteops.Errorf(noteops.CodeSnapshotFailed, "the save completed and is on disk"))
	payload := decodeSourceError(t, recorder.Result(), http.StatusInternalServerError, noteops.CodeSnapshotFailed)
	if payload.Revision != landed.Revision || !payload.Changed {
		t.Fatalf("payload = %+v, want the landed revision and changed: true", payload)
	}

	recorder = httptest.NewRecorder()
	srv.writeSourceSaveError(recorder, req, nil,
		noteops.Errorf(noteops.CodeSafety, "this vault refuses further mutations"))
	payload = decodeSourceError(t, recorder.Result(), http.StatusInternalServerError, noteops.CodeSafety)
	if payload.Changed || payload.Revision != "" {
		t.Fatalf("payload = %+v, want no claim that anything landed", payload)
	}
}

// TestSourceSaveErrorCarriesTheAttachmentsALandedSaveCommitted is the other half
// of the snapshot-failure envelope, and the one the browser's staging area
// depends on.
//
// The uploads are in the vault — applySourceSave calls forgetCommittedAttachments
// whenever the result is non-nil, which a snapshot failure is — and the note on
// disk links them. A body that named none of them would leave the page still
// owing those ids, and the page gives an id it still owes back on the way out:
// the files the note references would be deleted from under it. A refusal that
// wrote nothing must carry none, for the mirror reason — the ids are still the
// page's to send with the attempt that follows.
func TestSourceSaveErrorCarriesTheAttachmentsALandedSaveCommitted(t *testing.T) {
	_, srv, _ := newSourceTestServer(t, Options{})
	req := noteops.SaveSourceRequest{RelPath: "Alpha.md", Content: sourceTestNote}
	committed := []noteops.SavedAttachment{
		{UploadID: "u-1", RelPath: "attachments/one.png"},
		{UploadID: "u-2", RelPath: "attachments/two.png"},
	}

	recorder := httptest.NewRecorder()
	srv.writeSourceSaveError(recorder, req, &noteops.SaveSourceResult{
		Destination: noteops.PathResolution{RelPath: "Alpha.md"},
		Revision:    vault.HashBytes([]byte(sourceTestNote)),
		Changed:     true,
		Attachments: committed,
	}, noteops.Errorf(noteops.CodeSnapshotFailed, "the save completed and is on disk"))
	payload := decodeSourceError(t, recorder.Result(), http.StatusInternalServerError, noteops.CodeSnapshotFailed)
	if !reflect.DeepEqual(payload.Attachments, committed) {
		t.Fatalf("attachments = %+v, want the two the save committed", payload.Attachments)
	}

	// A stale-revision refusal planned nothing and wrote nothing. It is the arm
	// that fills the payload from a *second* read of the note, so it is also the
	// one that could plausibly carry a field it has no business carrying.
	recorder = httptest.NewRecorder()
	srv.writeSourceSaveError(recorder, req, nil, &noteops.SourceRevisionError{
		RelPath:  "Alpha.md",
		Expected: "r-stale",
		Actual:   vault.HashBytes([]byte(sourceTestNote)),
		Err:      noteops.Errorf(noteops.CodeConflict, "source: Alpha.md changed since it was loaded"),
	})
	payload = decodeSourceError(t, recorder.Result(), http.StatusConflict, noteops.CodeConflict)
	if len(payload.Attachments) != 0 {
		t.Fatalf("attachments = %+v, want none on a save that wrote nothing", payload.Attachments)
	}
	if payload.Content != sourceTestNote {
		t.Fatalf("the conflict carried no bytes to merge against: %+v", payload)
	}
}
