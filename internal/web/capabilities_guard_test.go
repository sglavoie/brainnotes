package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

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
