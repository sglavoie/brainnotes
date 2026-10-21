package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/vault"
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
		absolutePaths  bool
		maxRequestBody int64
	}{
		{ModeLocal, true, true, false, true, localMaxRequestBody},
		{ModeReadOnly, false, false, true, false, remoteMaxRequestBody},
		{ModeHosted, true, false, true, false, remoteMaxRequestBody},
	} {
		got := capabilitiesFor(tc.mode)
		want := capabilities{
			mode:           tc.mode,
			documentWrites: tc.documentWrites,
			processLaunch:  tc.processLaunch,
			browserOpen:    tc.browserOpen,
			absolutePaths:  tc.absolutePaths,
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
