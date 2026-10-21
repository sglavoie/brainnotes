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

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/noteops"
)

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
