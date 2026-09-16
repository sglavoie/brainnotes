package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

const capabilityTestToken = "capability-csrf-token"

// capabilityHarness is a server in one mode plus the two launch recorders. The
// recorders are the point of the fixture: every assertion below that says "no
// process was launched" is proved by these staying false, not by a status code
// that could be produced for some other reason.
type capabilityHarness struct {
	mode          Mode
	server        *Server
	root          string
	editorStarted *bool
	agentRan      *bool
}

// capabilityOption adjusts the fixture after the mode has decided its options
// and before the server is built, for the rows that need a setting the matrix
// otherwise holds constant — the startup browser is the only one so far.
type capabilityOption func(*config.Config, *Options)

// newCapabilityHarness builds one server per mode over an identical vault, so a
// difference between two rows of the matrix is the mode and nothing else.
//
// Read-only opens the vault read-only and needs external state, matching how
// `bn serve --read-only` is actually launched; local and hosted open the same
// vault for writing. The editor and agent hooks record rather than fail, because
// several rows below assert a launch *did* happen in local mode — a fixture that
// fataled on launch could only ever test the denials.
func newCapabilityHarness(t *testing.T, mode Mode, options ...capabilityOption) *capabilityHarness {
	t.Helper()

	root := t.TempDir()
	for rel, body := range map[string]string{
		"Alpha.md":              "---\ntitle: Alpha\n---\n[[Beta]]\n",
		"Beta.md":               "# Beta\n",
		"file.txt":              "attachment",
		"page.html":             "<script>alert(1)</script>",
		"drawing.svg":           "<svg xmlns=\"http://www.w3.org/2000/svg\"></svg>",
		"picture.png":           "\x89PNG\r\n\x1a\n",
		"mystery.unknownext":    "<html><body>hi</body></html>",
		"brainnotes.toml":       "vault = \".\"\n",
		"brainnotes.local.toml": "[open]\neditor = \"secret-editor\"\n",
		".secret":               "hidden",
	} {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	disabled := false
	cfg := &config.Config{
		Vault: root,
		Serve: config.ServeConfig{Addr: "127.0.0.1:0", OpenBrowser: &disabled},
		Open:  config.OpenConfig{Editor: "true"},
		Agent: config.AgentConfig{Command: []string{"true"}},
	}

	editorStarted, agentRan := false, false
	opts := Options{
		CSRFToken:   capabilityTestToken,
		StartEditor: func(string, []string) error { editorStarted = true; return nil },
		RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
			agentRan = true
			return []byte("agent output"), nil
		},
	}

	openVault := vault.Open
	switch mode {
	case ModeReadOnly:
		opts.ReadOnly = true
		opts.StateDir = t.TempDir()
		openVault = vault.OpenReadOnly
	case ModeHosted:
		opts.Hosted = true
	case ModeLocal:
	}
	for _, option := range options {
		option(cfg, &opts)
	}

	v, err := openVault(cfg)
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	srv, err := NewServer(cfg, v, opts)
	if err != nil {
		t.Fatalf("NewServer(%s): %v", mode, err)
	}
	if srv.caps.mode != mode {
		t.Fatalf("resolved mode = %q, want %q", srv.caps.mode, mode)
	}
	return &capabilityHarness{mode: mode, server: srv, root: root, editorStarted: &editorStarted, agentRan: &agentRan}
}

// post issues a same-origin, CSRF-bearing POST. Every capability assertion uses
// a request that would succeed in local mode, so a refusal elsewhere is the
// mode's doing and not a missing token.
func (h *capabilityHarness) post(route string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, route, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(csrfHeaderName, capabilityTestToken)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	h.server.Handler().ServeHTTP(rec, req)
	return rec
}

func (h *capabilityHarness) get(route string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, route, nil))
	return rec
}

func capabilityForm(extra url.Values) url.Values {
	form := url.Values{"token": {capabilityTestToken}}
	for key, values := range extra {
		form[key] = values
	}
	return form
}

// TestCapabilityTableIsThreeIndependentProfiles pins the table itself. The rest
// of this file tests behaviour through handlers; this is the one assertion that
// hosted is not "read-only with writes turned back on" — the two dimensions move
// separately, and the body bound follows reachability rather than writability.
func TestCapabilityTableIsThreeIndependentProfiles(t *testing.T) {
	for _, tc := range []struct {
		mode           Mode
		documentWrites bool
		processLaunch  bool
		browserOpen    bool
		maxRequestBody int64
	}{
		{ModeLocal, true, true, false, localMaxRequestBody},
		{ModeReadOnly, false, false, true, remoteMaxRequestBody},
		{ModeHosted, true, false, true, remoteMaxRequestBody},
	} {
		got := capabilitiesFor(tc.mode)
		want := capabilities{
			mode:           tc.mode,
			documentWrites: tc.documentWrites,
			processLaunch:  tc.processLaunch,
			browserOpen:    tc.browserOpen,
			maxRequestBody: tc.maxRequestBody,
		}
		if got != want {
			t.Errorf("capabilitiesFor(%s) = %+v, want %+v", tc.mode, got, want)
		}
	}
	// An unrecognised mode must fall back to the profile that assumes the
	// server is not reachable from elsewhere only when that is already true;
	// the zero Mode is the local default and nothing else may reach it.
	if got := capabilitiesFor(Mode("something-else")); got.mode != ModeLocal {
		t.Errorf("unknown mode resolved to %+v, want the local profile", got)
	}
}

func TestModeForRefusesTwoProfilesAtOnce(t *testing.T) {
	for _, tc := range []struct {
		opts    Options
		want    Mode
		wantErr bool
	}{
		{Options{}, ModeLocal, false},
		{Options{ReadOnly: true}, ModeReadOnly, false},
		{Options{Hosted: true}, ModeHosted, false},
		{Options{ReadOnly: true, Hosted: true}, "", true},
	} {
		got, err := modeFor(tc.opts)
		if (err != nil) != tc.wantErr {
			t.Fatalf("modeFor(%+v) error = %v, wantErr %v", tc.opts, err, tc.wantErr)
		}
		if got != tc.want {
			t.Errorf("modeFor(%+v) = %q, want %q", tc.opts, got, tc.want)
		}
	}
	// The same pair must be refused at the constructor, not only by the helper:
	// a library caller never goes through the CLI's mutually-exclusive flags.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Note.md"), []byte("# Note\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Vault: root, Serve: config.ServeConfig{Addr: "127.0.0.1:0"}}
	v, err := vault.OpenReadOnly(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewServer(cfg, v, Options{ReadOnly: true, Hosted: true, StateDir: t.TempDir()}); err == nil {
		t.Fatal("NewServer accepted --read-only and --hosted together")
	}
}

// TestCapabilityMatrixOverProcessLaunchRoutes drives every route that ends in an
// exec.Cmd, in every mode, and asserts both the status and whether a process was
// actually started. Statuses alone would not be enough: a handler could answer
// 200 after launching nothing, or refuse *after* starting the process.
//
// These requests go straight at the handlers rather than through the rendered
// page, so a control the stylesheet hides is still exercised here — the CSS is
// presentation and this is the boundary.
func TestCapabilityMatrixOverProcessLaunchRoutes(t *testing.T) {
	for _, mode := range []Mode{ModeLocal, ModeReadOnly, ModeHosted} {
		t.Run(string(mode), func(t *testing.T) {
			h := newCapabilityHarness(t, mode)
			wantLaunch := mode == ModeLocal

			assertProcessLaunchRoutesRefused(t, h, mode, wantLaunch)

			if !wantLaunch {
				if *h.editorStarted || *h.agentRan {
					t.Fatalf("%s mode launched a process: editor=%v agent=%v", mode, *h.editorStarted, *h.agentRan)
				}
				return
			}
			// The recorders must be able to observe a launch, or every "no
			// process was launched" assertion above is vacuous. /api/config/open
			// needs nothing but a token, so it is the one that reliably fires.
			if !*h.editorStarted {
				t.Fatal("local mode launched no editor; the launch recorder proves nothing")
			}
		})
	}
}

// assertProcessLaunchRoutesRefused posts to every route whose success ends in an
// exec.Cmd and asserts the status the mode owes. The form carries every field
// any of them needs, because the assertion is about the refusal arriving before
// the handler reads the form at all: a mode without processLaunch must answer
// 403 whether or not the request would otherwise have been well formed.
//
// The refusal's *reason* is asserted alongside the status. 403 on its own does
// not distinguish the two refusals the guard can make, and read-only refuses
// most of these as a replica whether or not processLaunchRoute still names
// them — so without the reason, dropping a route from that list would go
// unnoticed in the mode where it is the only thing standing in the way.
func assertProcessLaunchRoutesRefused(t *testing.T, h *capabilityHarness, mode Mode, wantLaunch bool) {
	t.Helper()

	launchReason := "process launch is unavailable in " + string(mode) + " mode"
	for _, route := range []string{
		configOpenAPIRoutePath,
		noteInsightAPIRoutePath,
		dailyReviewAgentAPIRoutePath,
		synthesisGenerateAPIRoutePath,
		briefInsightAPIRoutePath,
	} {
		rec := h.post(route, capabilityForm(url.Values{"key": {"Alpha"}, "card_id": {"missing"}}))
		if wantLaunch {
			if rec.Code == http.StatusForbidden {
				t.Errorf("%s refused in local mode: %s", route, rec.Body.String())
			}
			continue
		}
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403 in %s mode", route, rec.Code, mode)
			continue
		}
		got := managementErrorMessage(t, rec)
		switch mode {
		case ModeHosted:
			// Hosted writes, so the replica refusal cannot fire here: the
			// process-launch capability is the whole reason for the 403.
			if got != launchReason {
				t.Errorf("%s in hosted mode: message %q, want %q", route, got, launchReason)
			}
		default:
			if got != "read-only replica" && got != launchReason {
				t.Errorf("%s in %s mode: message %q, want a named refusal", route, mode, got)
			}
		}
	}
}

// managementErrorMessage reads the message out of the management envelope every
// refusal is written in, so an assertion can name the refusal rather than the
// status class it shares with every other one.
func managementErrorMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var payload struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode management error: %v (%s)", err, rec.Body.String())
	}
	return payload.Message
}

// TestOpenAnswersWithNavigationWithoutLaunching is the /api/open half of the
// matrix. The route is deliberately not in processLaunchRoute, because it means
// something without a process: hosted answers with the route to navigate to.
// Read-only refuses it instead — its POST surface is an allowlist of exactly two
// routes, and widening a hardened public surface for an action the Brief already
// offers would be the wrong direction.
func TestOpenAnswersWithNavigationWithoutLaunching(t *testing.T) {
	for _, tc := range []struct {
		mode           Mode
		wantStatus     int
		wantEditor     bool
		wantNavigation bool
	}{
		// Local answers 204: the editor is now open on the person's desktop and
		// there is nothing for the browser to do.
		{ModeLocal, http.StatusNoContent, true, false},
		{ModeHosted, http.StatusOK, false, true},
		{ModeReadOnly, http.StatusForbidden, false, false},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			h := newCapabilityHarness(t, tc.mode)
			rec := h.post(openAPIRoutePath, capabilityForm(url.Values{"key": {"Alpha"}}))
			if rec.Code != tc.wantStatus {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if *h.editorStarted != tc.wantEditor {
				t.Fatalf("editor started = %v, want %v", *h.editorStarted, tc.wantEditor)
			}
			if !tc.wantNavigation {
				return
			}
			var payload openNavigationResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode: %v (%s)", err, rec.Body.String())
			}
			if !payload.OK || !strings.HasPrefix(payload.Destination, noteRoutePrefix) {
				t.Fatalf("destination = %+v, want an %s route", payload, noteRoutePrefix)
			}
			// The destination must be a route this server actually serves, or
			// the Open button navigates to a 404.
			if got := h.get(payload.Destination); got.Code != http.StatusOK {
				t.Fatalf("navigating to %s: status %d", payload.Destination, got.Code)
			}
		})
	}
}

// TestOpenNoteTargetIsOneDoorForEveryMode covers the door the Brief's Open and
// the Related panel's Open share. Local is here as well as the two navigating
// modes: "a mode without browserOpen still launches" is half of what the door
// promises, and a branch nobody exercises is a branch that can be inverted.
func TestOpenNoteTargetIsOneDoorForEveryMode(t *testing.T) {
	for _, tc := range []struct {
		mode      Mode
		wantRoute bool
	}{
		{ModeLocal, false},
		{ModeReadOnly, true},
		{ModeHosted, true},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			h := newCapabilityHarness(t, tc.mode)
			destination, err := h.server.openNoteTarget(filepath.Join(h.root, "Alpha.md"), "Alpha.md")
			if err != nil {
				t.Fatalf("openNoteTarget: %v", err)
			}
			if tc.wantRoute {
				assertOpenNoteTargetNavigates(t, h, destination)
				return
			}
			if destination != "" {
				t.Fatalf("destination = %q, want the launching branch", destination)
			}
			if !*h.editorStarted {
				t.Fatal("local mode navigated instead of starting an editor")
			}
		})
	}
}

func assertOpenNoteTargetNavigates(t *testing.T, h *capabilityHarness, destination string) {
	t.Helper()
	if !strings.HasPrefix(destination, noteRoutePrefix) {
		t.Fatalf("destination = %q, want an %s route", destination, noteRoutePrefix)
	}
	if *h.editorStarted {
		t.Fatal("Open started an editor")
	}
	// A note that vanished is a 404 about the note. The navigating branch never
	// started an editor, so it must not report one failing to start.
	_, err := h.server.openNoteTarget(filepath.Join(h.root, "Gone.md"), "Gone.md")
	if got := noteops.CodeOf(err); got != noteops.CodeNotFound {
		t.Fatalf("missing note error = %v (code %q), want %q", err, got, noteops.CodeNotFound)
	}
}

// TestRelatedOpenAnswersWithNavigationOverHTTP drives the Related panel's Open
// through the mux rather than through the shared door directly, because the
// panel's failure mode was in the handler around it: a refusal mapped as an
// editor-start error became a 500 about a subsystem hosted mode does not have.
//
// Read-only refuses the route outright — its POST surface is an allowlist of
// two brief routes and this is not one of them — so the row asserts the refusal
// rather than a destination.
func TestRelatedOpenAnswersWithNavigationOverHTTP(t *testing.T) {
	for _, tc := range []struct {
		mode           Mode
		wantStatus     int
		wantEditor     bool
		wantNavigation bool
	}{
		{ModeLocal, http.StatusOK, true, false},
		{ModeHosted, http.StatusOK, false, true},
		{ModeReadOnly, http.StatusForbidden, false, false},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			h := newCapabilityHarness(t, tc.mode)
			rec := h.post(relatedItemAPIRoutePath, capabilityForm(url.Values{
				"action":    {"open_editor"},
				"source":    {"Alpha"},
				"candidate": {"Beta"},
			}))
			if rec.Code != tc.wantStatus {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if *h.editorStarted != tc.wantEditor {
				t.Fatalf("editor started = %v, want %v", *h.editorStarted, tc.wantEditor)
			}
			if tc.wantStatus == http.StatusOK {
				assertRelatedOpenDestination(t, h, rec, tc.wantNavigation)
			}
		})
	}
}

func assertRelatedOpenDestination(t *testing.T, h *capabilityHarness, rec *httptest.ResponseRecorder, wantNavigation bool) {
	t.Helper()
	var payload relatedItemResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if (payload.Destination != "") != wantNavigation {
		t.Fatalf("destination = %q, want navigation = %v", payload.Destination, wantNavigation)
	}
	if !wantNavigation {
		return
	}
	if !strings.HasPrefix(payload.Destination, noteRoutePrefix) {
		t.Fatalf("destination = %q, want an %s route", payload.Destination, noteRoutePrefix)
	}
	// The same check the Brief's Open gets: a route the server does not serve is
	// an Open button that navigates to a 404.
	if got := h.get(payload.Destination); got.Code != http.StatusOK {
		t.Fatalf("navigating to %s: status %d", payload.Destination, got.Code)
	}
}

// refuseRecallWrites makes the recall sidecar unwritable while leaving it
// readable, which is one way to reach applyRelatedOpen's warning branch from
// outside — the state store is built inside the request, so a test cannot
// substitute one. related_item_editor_api_test.go gets there the other way, by
// planting a symlink from its StartEditor hook; this one needs no launch hook
// and so also covers the hosted branch, where nothing is launched.
//
// Validate only reads and therefore still passes. RecordOpened has to take a
// lock and then replace a file inside this directory, and can do neither.
func refuseRecallWrites(t *testing.T, root string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so the write would succeed")
	}
	dir := filepath.Join(root, ".bnotes")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	// Restored so TempDir's own cleanup can remove the tree.
	t.Cleanup(func() { _ = os.Chmod(dir, 0o750) })
}

// TestRelatedOpenWarnsWhenTheRecallWriteFails pins the sentence the server sends
// when it has answered an Open and then failed to record it. The exact string
// matters to more than this file: the panel decides whether to navigate by
// looking at `warning` (see static_src/related-mutations.ts), and it composes its
// own sentence around this one — so a server that quietly stopped setting the
// warning, or set a different field, would turn a visible failure into a silent
// navigation away from the only report of it.
//
// Both modes are asserted because the two clients differ: local started an
// editor and has nowhere to send the reader, hosted answers with a destination
// and deliberately does not follow it.
func TestRelatedOpenWarnsWhenTheRecallWriteFails(t *testing.T) {
	for _, tc := range []struct {
		mode           Mode
		wantEditor     bool
		wantNavigation bool
	}{
		{ModeLocal, true, false},
		{ModeHosted, false, true},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			h := newCapabilityHarness(t, tc.mode)
			refuseRecallWrites(t, h.root)

			rec := h.post(relatedItemAPIRoutePath, capabilityForm(url.Values{
				"action":    {"open_editor"},
				"source":    {"Alpha"},
				"candidate": {"Beta"},
			}))
			// A failed recall write is a warning, not an error: the note is
			// reachable either way, so answering 500 here would hide an Open that
			// actually happened.
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
			}
			assertUnrecordedOpen(t, rec, tc.wantNavigation)
			if *h.editorStarted != tc.wantEditor {
				t.Errorf("editor started = %v, want %v", *h.editorStarted, tc.wantEditor)
			}
		})
	}
}

func assertUnrecordedOpen(t *testing.T, rec *httptest.ResponseRecorder, wantNavigation bool) {
	t.Helper()
	var payload relatedItemResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if payload.Warning != "Recall history could not be recorded." {
		t.Errorf("warning = %q, want the recall-history sentence", payload.Warning)
	}
	if payload.Recorded == nil || *payload.Recorded {
		t.Errorf("recorded = %v, want an explicit false", payload.Recorded)
	}
	if !payload.Opened {
		t.Error("opened = false, but the open succeeded and only the recording failed")
	}
	if (payload.Destination != "") != wantNavigation {
		t.Errorf("destination = %q, want navigation = %v", payload.Destination, wantNavigation)
	}
}

// TestProcessLaunchRefusedAtTheExecSite is the belt-and-braces half. The route
// list in processLaunchRoute is a list, and a list can fall behind the routes
// that exist; these two refusals are at the single place each process is
// actually started, so a new route that forgets the list still cannot exec.
func TestProcessLaunchRefusedAtTheExecSite(t *testing.T) {
	for _, mode := range []Mode{ModeReadOnly, ModeHosted} {
		t.Run(string(mode), func(t *testing.T) {
			h := newCapabilityHarness(t, mode)

			err := h.server.openPath(filepath.Join(h.root, "Alpha.md"))
			if !errors.Is(err, errProcessLaunchDisabled) {
				t.Fatalf("openPath error = %v, want errProcessLaunchDisabled", err)
			}
			if failureStage(err) != openFailureCapability {
				t.Fatalf("openPath stage = %v, want openFailureCapability", failureStage(err))
			}

			if _, err := h.server.launchAgent(context.Background(), []string{"true"}, nil); !errors.Is(err, errAgentLaunchDisabled) {
				t.Fatalf("launchAgent error = %v, want errAgentLaunchDisabled", err)
			}

			if *h.editorStarted || *h.agentRan {
				t.Fatal("a process was launched despite the refusal")
			}
		})
	}
}

// TestStartupBrowserAnswersToTheCapability covers the one exec site no request
// reaches. `bn serve` already forces --no-open for both remote profiles, so this
// is about the other caller: a library caller that builds Options by hand over a
// vault whose [serve] section enables the browser. Without the capability check
// in Run that deployment spawns a browser on a machine nobody is sitting at, and
// no route-level test would ever see it.
func TestStartupBrowserAnswersToTheCapability(t *testing.T) {
	for _, tc := range []struct {
		mode       Mode
		wantLaunch bool
	}{
		{ModeLocal, true},
		{ModeHosted, false},
		{ModeReadOnly, false},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			launched := make(chan string, 1)
			listening := make(chan string, 1)
			enabled := true
			h := newCapabilityHarness(t, tc.mode, func(cfg *config.Config, opts *Options) {
				cfg.Serve.OpenBrowser = &enabled
				opts.OpenBrowser = func(url string) error { launched <- url; return nil }
				opts.NotifyListening = func(url string) { listening <- url }
			})

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- h.server.Run(ctx) }()
			select {
			case <-listening:
			case <-time.After(5 * time.Second):
				cancel()
				t.Fatal("server never reported a listener")
			}
			cancel()
			if err := <-done; err != nil {
				t.Fatalf("Run: %v", err)
			}

			// The launch is a goroutine started before Serve, so waiting on the
			// channel is how both rows are judged: the negative one needs the
			// window to have been long enough for a launch to have shown up.
			select {
			case url := <-launched:
				if !tc.wantLaunch {
					t.Fatalf("%s mode launched a browser at %s", tc.mode, url)
				}
			case <-time.After(2 * time.Second):
				if tc.wantLaunch {
					t.Fatalf("%s mode never launched a browser", tc.mode)
				}
			}
		})
	}
}

// TestDocumentWritesFollowTheirOwnDimension is the point of the session: hosted
// keeps the write surface that read-only refuses. Capture is the representative
// write because it bootstraps its own note, so the assertion is about the
// capability rather than about fixture state.
func TestDocumentWritesFollowTheirOwnDimension(t *testing.T) {
	for _, tc := range []struct {
		mode       Mode
		wantWrites bool
	}{
		{ModeLocal, true},
		{ModeHosted, true},
		{ModeReadOnly, false},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			h := newCapabilityHarness(t, tc.mode)
			rec := h.post("/api/notes/frontmatter/set", capabilityForm(url.Values{
				"key": {"Alpha"}, "property": {"status"}, "value": {"draft"},
			}))
			refused := rec.Code == http.StatusForbidden
			if refused == tc.wantWrites {
				t.Fatalf("frontmatter set: status %d, want writes allowed = %v", rec.Code, tc.wantWrites)
			}
		})
	}
}

// TestHostedReportsItsCapabilitiesToTheBrowser checks the one payload the page
// configures itself from. Writable must fold in the mode: a replica of a vault
// whose config file is writable on disk is still not a vault this server writes.
func TestHostedReportsItsCapabilitiesToTheBrowser(t *testing.T) {
	for _, tc := range []struct {
		mode              Mode
		wantWritable      bool
		wantProcessLaunch bool
	}{
		{ModeLocal, true, true},
		{ModeHosted, true, false},
		{ModeReadOnly, false, false},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			h := newCapabilityHarness(t, tc.mode)
			rec := h.get("/api/config/status")
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			var payload ConfigStatus
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode: %v (%s)", err, rec.Body.String())
			}
			if payload.Mode != string(tc.mode) {
				t.Errorf("mode = %q, want %q", payload.Mode, tc.mode)
			}
			if payload.Writable != tc.wantWritable {
				t.Errorf("writable = %v, want %v", payload.Writable, tc.wantWritable)
			}
			if payload.ProcessLaunch != tc.wantProcessLaunch {
				t.Errorf("process_launch = %v, want %v", payload.ProcessLaunch, tc.wantProcessLaunch)
			}
		})
	}
}

// TestPrivateFilesAreNeverAttachmentsInAnyMode re-asserts the media boundary per
// mode rather than only for the replica. Hosted is the mode where it newly
// matters: brainnotes.local.toml names this machine's editor and agent command,
// and hosted is the first writable profile reachable from another machine.
func TestPrivateFilesAreNeverAttachmentsInAnyMode(t *testing.T) {
	for _, mode := range []Mode{ModeLocal, ModeReadOnly, ModeHosted} {
		t.Run(string(mode), func(t *testing.T) {
			h := newCapabilityHarness(t, mode)
			// The traversal rows are %2e-encoded on purpose. ServeMux resolves
			// a literal ".." or "." into a redirect before any handler runs, so
			// the literal form proves the redirect exists and nothing about the
			// media handler; the encoded form is what actually reaches it — and
			// what a URL-cleaning proxy in front of a hosted deployment would
			// not have normalized away either. Both are asserted, with the
			// status each is expected to produce, so a row cannot quietly stop
			// testing anything.
			for _, tc := range []struct {
				route      string
				wantStatus int
			}{
				{"/media/brainnotes.toml", http.StatusNotFound},
				{"/media/brainnotes.local.toml", http.StatusNotFound},
				{"/media/.secret", http.StatusNotFound},
				{"/media/%2e%2e/brainnotes.toml", http.StatusNotFound},
				{"/media/%2e/brainnotes.toml", http.StatusNotFound},
				{"/media/sub/%2e%2e/brainnotes.local.toml", http.StatusNotFound},
				{"/media/..%2fbrainnotes.toml", http.StatusNotFound},
				{"/media/../brainnotes.toml", http.StatusTemporaryRedirect},
				{"/media/./brainnotes.toml", http.StatusTemporaryRedirect},
			} {
				assertPrivateMediaRefused(t, h, tc.route, tc.wantStatus)
			}
		})
	}
}

func assertPrivateMediaRefused(t *testing.T, h *capabilityHarness, route string, wantStatus int) {
	t.Helper()
	rec := h.get(route)
	if rec.Code != wantStatus {
		t.Errorf("%s: status %d, want %d: %s", route, rec.Code, wantStatus, rec.Body.String())
	}
	// A redirect is not an exposure, but following it must not become one:
	// that is the half the literal rows can still test.
	if location := rec.Header().Get("Location"); location != "" {
		if followed := h.get(location); followed.Code == http.StatusOK {
			t.Errorf("%s redirected to %s, which is served: %s", route, location, followed.Body.String())
		}
	}
	if strings.Contains(rec.Body.String(), "secret-editor") {
		t.Errorf("%s leaked the local config", route)
	}
}

// TestMediaSafetyHeaders covers the attachment surface itself. A vault holds
// whatever a person dropped in it, /media serves it from the same origin as
// every note page, and a browser that executed an .html attachment would be
// running script with this session's full reach.
func TestMediaSafetyHeaders(t *testing.T) {
	h := newCapabilityHarness(t, ModeHosted)
	for _, tc := range []struct {
		route          string
		wantSandbox    bool
		wantAttachment bool
		wantType       string
	}{
		{"/media/file.txt", false, false, "text/plain"},
		{"/media/picture.png", false, false, "image/png"},
		{"/media/page.html", true, true, "text/html"},
		{"/media/drawing.svg", true, true, "image/svg+xml"},
		// No extension the mime table knows, and bytes that sniff as HTML: the
		// declared type is the guess, so it is downgraded rather than trusted.
		{"/media/mystery.unknownext", true, true, "application/octet-stream"},
	} {
		rec := h.get(tc.route)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", tc.route, rec.Code)
			continue
		}
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, tc.wantType) {
			t.Errorf("%s: Content-Type = %q, want %q", tc.route, got, tc.wantType)
		}
		if got := rec.Header().Get("Content-Security-Policy") == "sandbox"; got != tc.wantSandbox {
			t.Errorf("%s: sandboxed = %v, want %v", tc.route, got, tc.wantSandbox)
		}
		disposition := rec.Header().Get("Content-Disposition")
		if got := strings.HasPrefix(disposition, "attachment"); got != tc.wantAttachment {
			t.Errorf("%s: Content-Disposition = %q, want attachment = %v", tc.route, disposition, tc.wantAttachment)
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q, want nosniff", tc.route, got)
		}
	}
}

// TestAttachmentDispositionCannotForgeAHeader covers the filename, which comes
// out of the vault and is therefore attacker-influenced whenever a synchronised
// folder is.
func TestAttachmentDispositionCannotForgeAHeader(t *testing.T) {
	for _, name := range []string{
		`plain.html`,
		`we"ird.html`,
		"new\nline.html",
		"semi;colon.html",
		"héllo.html",
		"\xff\xfe.html",
	} {
		got := attachmentDisposition(name)
		if !strings.HasPrefix(got, "attachment") {
			t.Errorf("attachmentDisposition(%q) = %q, want an attachment disposition", name, got)
		}
		if strings.ContainsAny(got, "\r\n") {
			t.Errorf("attachmentDisposition(%q) = %q, contains a line break", name, got)
		}
	}
}

func TestActiveMediaTypeClassification(t *testing.T) {
	for _, tc := range []struct {
		contentType string
		want        bool
	}{
		{"text/plain; charset=utf-8", false},
		{"image/png", false},
		{"image/jpeg", false},
		{"application/pdf", false},
		{"audio/mpeg", false},
		{"text/html", true},
		{"text/html; charset=utf-8", true},
		{"TEXT/HTML", true},
		{"image/svg+xml", true},
		{"application/xhtml+xml", true},
		{"application/xml", true},
		{"text/javascript", true},
		{"application/x-shockwave-flash", true},
		// An unparsable type is classified as active: the safe answer for bytes
		// nobody can classify is the restricted one.
		{"not a media type at all", true},
	} {
		if got := activeMediaType(tc.contentType); got != tc.want {
			t.Errorf("activeMediaType(%q) = %v, want %v", tc.contentType, got, tc.want)
		}
	}
}

// TestGuardHeadersApplyInEveryMode pins the headers the guard sets for
// everything. no-store is what keeps a shared cache in front of a hosted
// deployment from retaining a note it served to one authenticated person, and
// DENY is what keeps its write controls out of an attacker's frame — the CSRF
// token is no defence against a click in a frame that carries the real cookie.
// They are asserted together because they are one decision: these are the four
// headers true of a vault response in every mode.
func TestGuardHeadersApplyInEveryMode(t *testing.T) {
	want := map[string]string{
		"Cache-Control":          "private, no-store",
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "same-origin",
		"X-Frame-Options":        "DENY",
	}
	for _, mode := range []Mode{ModeLocal, ModeReadOnly, ModeHosted} {
		t.Run(string(mode), func(t *testing.T) {
			h := newCapabilityHarness(t, mode)
			for _, route := range []string{"/", "/n/Alpha", "/api/search?q=Alpha", "/media/file.txt"} {
				rec := h.get(route)
				for header, value := range want {
					if got := rec.Header().Get(header); got != value {
						t.Errorf("%s: %s = %q, want %q", route, header, got, value)
					}
				}
			}
		})
	}
}

// TestSameOriginRequest is defence in depth beside the per-route CSRF token, and
// it is what a hosted deployment leans on once the listener is reachable from
// something other than loopback.
func TestSameOriginRequest(t *testing.T) {
	for _, tc := range []struct {
		name      string
		method    string
		host      string
		origin    string
		fetchSite string
		want      bool
	}{
		{"read methods are never state changing", http.MethodGet, "vault.example", "https://evil.example", "cross-site", true},
		{"head is a read", http.MethodHead, "vault.example", "https://evil.example", "cross-site", true},
		{"browser says same-origin", http.MethodPost, "vault.example", "", "same-origin", true},
		{"browser says none", http.MethodPost, "vault.example", "", "none", true},
		{"browser says cross-site", http.MethodPost, "vault.example", "", "cross-site", false},
		// same-site is another origin: a sibling subdomain a hosted deployment
		// does not control is exactly the threat the check exists for.
		{"browser says same-site", http.MethodPost, "vault.example", "https://other.example", "same-site", false},
		{"no browser headers at all", http.MethodPost, "vault.example", "", "", true},
		{"matching origin", http.MethodPost, "vault.example", "https://vault.example", "", true},
		{"matching origin ignoring case", http.MethodPost, "vault.example", "https://VAULT.example", "", true},
		{"foreign origin", http.MethodPost, "vault.example", "https://evil.example", "", false},
		{"opaque origin", http.MethodPost, "vault.example", "null", "", false},
		{"unparsable origin", http.MethodPost, "vault.example", "::::", "", false},
		// The userinfo trick: the string starts with the real host but names
		// somebody else.
		{"origin with userinfo", http.MethodPost, "vault.example", "https://vault.example@evil.example", "", false},
		{"port must match too", http.MethodPost, "vault.example:8080", "https://vault.example:9090", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/api/capture", nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			}
			if got := sameOriginRequest(req); got != tc.want {
				t.Errorf("sameOriginRequest = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCrossOriginPostIsRefusedBeforeTheHandler proves the check is wired into
// the guard rather than only unit-tested, and that it refuses before any write
// can happen — the request below carries a valid CSRF token.
func TestCrossOriginPostIsRefusedBeforeTheHandler(t *testing.T) {
	h := newCapabilityHarness(t, ModeHosted)
	form := capabilityForm(url.Values{"key": {"Alpha"}, "property": {"status"}, "value": {"draft"}})
	req := httptest.NewRequest(http.MethodPost, "/api/notes/frontmatter/set", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(csrfHeaderName, capabilityTestToken)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cross-origin") {
		t.Fatalf("body = %s, want a cross-origin refusal", rec.Body.String())
	}
}

// TestRequestBodyIsBoundedPerProfile covers the bound that follows reachability
// rather than writability. The local bound is generous on purpose: a bulk action
// over a very large vault posts a real number of note keys.
func TestRequestBodyIsBoundedPerProfile(t *testing.T) {
	h := newCapabilityHarness(t, ModeHosted)
	form := capabilityForm(url.Values{
		"key":      {"Alpha"},
		"property": {"status"},
		"value":    {strings.Repeat("x", int(remoteMaxRequestBody)+1024)},
	})
	rec := h.post("/api/notes/frontmatter/set", form)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: status %d, want 413: %s", rec.Code, rec.Body.String())
	}

	// The same body is within the local profile's bound and succeeds there, so
	// the refusal above is the bound and not some other rejection of a very long
	// property value.
	local := newCapabilityHarness(t, ModeLocal)
	if got := local.post("/api/notes/frontmatter/set", form); got.Code != http.StatusOK {
		t.Fatalf("local profile: status %d, want 200: %s", got.Code, got.Body.String())
	}

	// A body that declares no length at all still cannot spend more than the
	// bound: the declared-length check above is the honest status code, and
	// MaxBytesReader is the part that actually holds.
	body := strings.NewReader(strings.Repeat("x", int(remoteMaxRequestBody)+1024))
	req := httptest.NewRequest(http.MethodPost, "/api/notes/frontmatter/set", body)
	req.ContentLength = -1
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(csrfHeaderName, capabilityTestToken)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	undeclared := httptest.NewRecorder()
	h.server.Handler().ServeHTTP(undeclared, req)
	if undeclared.Code == http.StatusOK {
		t.Fatalf("undeclared oversized body accepted: %s", undeclared.Body.String())
	}
}

// TestSynthesisPreviewKeepsItsOwnBoundInEveryProfile covers the one route whose
// legitimate request is larger than the remote profile's bound. A form-encoded
// draft is several times its own size, so without the widening the guard would
// answer 413 before the route's own narrower bound ever applied: a hosted
// deployment could never preview a full-size draft, and a bound that reads as a
// defence would in fact be a feature cap nobody chose.
func TestSynthesisPreviewKeepsItsOwnBoundInEveryProfile(t *testing.T) {
	for _, tc := range []struct {
		mode Mode
		want int64
	}{
		{ModeLocal, localMaxRequestBody},
		{ModeReadOnly, synthesisPreviewBodyLimit},
		{ModeHosted, synthesisPreviewBodyLimit},
	} {
		caps := capabilitiesFor(tc.mode)
		if got := caps.requestBodyLimit(synthesisPreviewAPIRoutePath); got != tc.want {
			t.Errorf("%s: preview bound = %d, want %d", tc.mode, got, tc.want)
		}
		// The widening is for this route and no other: everything else is held
		// to the profile's bound.
		if got := caps.requestBodyLimit("/api/notes/frontmatter/set"); got != caps.maxRequestBody {
			t.Errorf("%s: other route bound = %d, want the profile's %d", tc.mode, got, caps.maxRequestBody)
		}
	}

	// The effective bound, not only the table. A hosted request between the two
	// bounds must reach the route — whatever the route then makes of it — and
	// one past the route's own bound must still be refused as too large.
	h := newCapabilityHarness(t, ModeHosted)
	between := h.post(synthesisPreviewAPIRoutePath, capabilityForm(url.Values{
		"draft": {strings.Repeat("x", int(remoteMaxRequestBody)+1024)},
	}))
	if between.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("draft within the route's bound refused by the profile's: %s", between.Body.String())
	}
	past := h.post(synthesisPreviewAPIRoutePath, capabilityForm(url.Values{
		"draft": {strings.Repeat("x", int(synthesisPreviewBodyLimit)+1024)},
	}))
	if past.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("draft past the route's bound: status %d, want 413: %s", past.Code, past.Body.String())
	}
}

// TestProcessLaunchRouteListCoversEveryAgentRoute keeps the guard's list honest.
// A route that runs an agent and is missing from the list would still be refused
// at launchAgent, but it would be refused *after* parsing and dispatching, which
// is not where a deployment that cannot launch processes should be spending its
// attention.
func TestProcessLaunchRouteListCoversEveryAgentRoute(t *testing.T) {
	for _, route := range []string{
		configOpenAPIRoutePath,
		briefInsightAPIRoutePath,
		noteInsightAPIRoutePath,
		dailyReviewAgentAPIRoutePath,
		synthesisGenerateAPIRoutePath,
	} {
		if !processLaunchRoute(route) {
			t.Errorf("processLaunchRoute(%q) = false, want true", route)
		}
	}
	// The two navigating routes must stay out of the list, or hosted mode loses
	// the Open action entirely.
	for _, route := range []string{openAPIRoutePath, briefItemAPIRoutePath, "/api/search"} {
		if processLaunchRoute(route) {
			t.Errorf("processLaunchRoute(%q) = true, want false", route)
		}
	}
}

// TestAgentUnavailableMessageDoesNotSendPeopleEditingTheWrongFile checks the
// copy, because the wrong copy here costs somebody a real half hour: telling a
// reader of a hosted vault to set [agent].command would send them editing a file
// on a machine that would still refuse to run it.
func TestAgentUnavailableMessageDoesNotSendPeopleEditingTheWrongFile(t *testing.T) {
	unconfigured := "Ask AI is unavailable until [agent].command is configured."

	local := newCapabilityHarness(t, ModeLocal)
	cfg := local.server.config()
	if available, message := local.server.agentAvailability(cfg, unconfigured); !available || message != "" {
		t.Errorf("configured local = (%v, %q), want (true, no explanation)", available, message)
	}
	// The same mode with nothing configured still gets the advice that helps:
	// on this machine, configuring the command is exactly what is missing.
	bare := *cfg
	bare.Agent.Command = nil
	if available, message := local.server.agentAvailability(&bare, unconfigured); available || message != unconfigured {
		t.Errorf("unconfigured local = (%v, %q), want (false, the unconfigured advice)", available, message)
	}

	for _, mode := range []Mode{ModeReadOnly, ModeHosted} {
		h := newCapabilityHarness(t, mode)
		available, got := h.server.agentAvailability(h.server.config(), unconfigured)
		if available {
			t.Errorf("%s reported the agent as available", mode)
		}
		if strings.Contains(got, "[agent].command") {
			t.Errorf("%s message = %q, still advises configuring the agent", mode, got)
		}
		if !strings.Contains(got, string(mode)) {
			t.Errorf("%s message = %q, does not name the mode", mode, got)
		}
	}
}
