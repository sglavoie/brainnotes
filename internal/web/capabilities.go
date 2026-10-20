package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

// Mode names the serving profile a vault is published under. It is derived from
// Options once, in NewServer, and is the only thing the rest of the package asks
// about a deployment: a handler never re-derives "am I hosted" from a flag.
type Mode string

const (
	// ModeLocal is `bn serve` on a developer's own machine: it may write the
	// vault and it may start the configured editor and agent commands.
	ModeLocal Mode = "local"
	// ModeReadOnly is the published replica: a route allowlist, no writes, and
	// no process launches.
	ModeReadOnly Mode = "read-only"
	// ModeHosted is the vault served over the network (behind Cloudflare Access
	// or a loopback tunnel): document actions are available, process launches
	// are not.
	ModeHosted Mode = "hosted"
)

// Body bounds are a property of the profile, not of the route. A profile that
// can be reached from another machine is bounded tightly; the local profile is
// bounded generously, because a bulk action over a very large vault posts a real
// number of note keys and refusing it would be a regression, not a defence.
const (
	localMaxRequestBody  = 32 << 20
	remoteMaxRequestBody = 1 << 20
)

// requestBodyLimit is the bound a request is actually held to: the profile's,
// widened for the routes whose legitimate request is larger than it.
//
// Without the widening the profile bound would silently cap a feature rather
// than defend it — a synthesis draft may be a megabyte on its own, so
// form-encoding it inside a preview request exceeds the remote bound every time
// and a hosted deployment could never preview a full-size draft at all. The
// route still applies its own narrower bound afterwards in requireManagementPost,
// so widening here does not make the request unbounded; it makes the bound the
// route's instead of the profile's.
func (c capabilities) requestBodyLimit(path string) int64 {
	if widened := routeBodyLimit(path); widened > c.maxRequestBody {
		return widened
	}
	return c.maxRequestBody
}

// routeBodyLimit is the transport allowance a route needs when its legitimate
// request is larger than a remote profile's bound. Zero means the profile's
// bound stands, which is the answer for every route that has no reason to be
// treated specially.
//
// Every route named here applies the same number itself in
// requireManagementPost, so this widening moves the bound from the profile to
// the route rather than removing it.
func routeBodyLimit(path string) int64 {
	switch path {
	case synthesisPreviewAPIRoutePath:
		return synthesisPreviewBodyLimit
	case sourcePreviewAPIRoutePath, sourceSaveAPIRoutePath, sourceCreateAPIRoutePath:
		return sourceRequestBodyLimit
	case attachmentUploadAPIRoutePath:
		return attachmentUploadBodyLimit
	}
	return 0
}

// capabilities is the one table stating what a mode may do. Everything that used
// to branch on `readOnly` asks this instead, so "hosted" is not "read-only with
// writes turned back on": the two dimensions are independent and the table is
// what keeps them from being confused.
//
//   - documentWrites: note, property, configuration and sidecar mutations.
//   - processLaunch: starting the configured editor or agent command.
//   - browserOpen: an Open action answers with a route to navigate to instead of
//     starting an editor. Every mode without processLaunch sets it, which is what
//     makes "Open" mean something in a mode that cannot start a process.
//   - absolutePaths: an error message may name the vault's absolute location.
//     Only local sets it; every profile reachable from another machine has the
//     root taken out (vaultRootRedactor), since the reader is not on that disk.
type capabilities struct {
	mode           Mode
	documentWrites bool
	processLaunch  bool
	browserOpen    bool
	absolutePaths  bool
	maxRequestBody int64
}

func capabilitiesFor(mode Mode) capabilities {
	switch mode {
	case ModeReadOnly:
		return capabilities{
			mode:           ModeReadOnly,
			browserOpen:    true,
			maxRequestBody: remoteMaxRequestBody,
		}
	case ModeHosted:
		return capabilities{
			mode:           ModeHosted,
			documentWrites: true,
			browserOpen:    true,
			maxRequestBody: remoteMaxRequestBody,
		}
	default:
		return capabilities{
			mode:           ModeLocal,
			documentWrites: true,
			processLaunch:  true,
			absolutePaths:  true,
			maxRequestBody: localMaxRequestBody,
		}
	}
}

// modeFor derives the profile from the two independent option flags. A hosted
// replica is refused rather than resolved: --read-only publishes a replica of a
// vault this process does not own, --hosted publishes the vault itself, and
// silently picking one of them would serve a deployment nobody asked for.
func modeFor(opts Options) (Mode, error) {
	switch {
	case opts.ReadOnly && opts.Hosted:
		return "", errors.New("serve: --read-only and --hosted are different serving profiles; choose one")
	case opts.ReadOnly:
		return ModeReadOnly, nil
	case opts.Hosted:
		return ModeHosted, nil
	default:
		return ModeLocal, nil
	}
}

// processLaunchRoutes are the routes whose success starts a process on the
// serving machine, and which have no browser equivalent. They are refused at the
// guard — before the handler, before ParseForm, before a CSRF comparison — in
// every mode without processLaunch, so a crafted request cannot reach an
// exec.Cmd through any of them.
//
// Three further routes can start a process and are deliberately *not* listed
// here, because they mean something without one: /api/open, /api/brief/item and
// /api/related/item answer with a destination to navigate to when browserOpen is
// set. Their boundary is in the handler rather than the guard, and each has a
// direct test proving the navigating branch never reaches openPath. Read-only
// still refuses two of the three — its POST allowlist is exactly /api/brief/view
// and /api/brief/item — so "not listed here" means "decided by the handler",
// not "reachable in every mode".
func processLaunchRoute(path string) bool {
	switch path {
	case configOpenAPIRoutePath,
		briefInsightAPIRoutePath,
		noteInsightAPIRoutePath,
		dailyReviewAgentAPIRoutePath,
		synthesisGenerateAPIRoutePath:
		return true
	}
	return false
}

// denialReason reports why the request is refused, or "" when it is allowed.
// Read-only is an allowlist (a route not named there is unavailable until it is
// reviewed); every other refusal is a capability the mode does not have.
func (c capabilities) denialReason(r *http.Request) string {
	if !c.documentWrites && !replicaRequestAllowed(r) {
		return "read-only replica"
	}
	if !c.processLaunch && processLaunchRoute(r.URL.Path) {
		return "process launch is unavailable in " + string(c.mode) + " mode"
	}
	return ""
}

// agentAvailability is the one answer to "may this page offer an AI action",
// together with what to say when it may not. A configured [agent].command is not
// enough on its own: running it is a process launch, so a mode without that
// capability reports the feature unavailable rather than rendering a control
// whose route the guard would refuse.
//
// The two answers come back together because they are one decision. A mode that
// cannot launch a process must not tell a person to configure [agent].command —
// configuring it would change nothing, and the advice would send them editing a
// file on the wrong machine — so the caller passes the unconfigured wording and
// this decides whether it is the true explanation.
func (s *Server) agentAvailability(cfg *config.Config, unconfigured string) (bool, string) {
	if !s.caps.processLaunch {
		return false, "AI actions are unavailable in " + string(s.caps.mode) + " mode: this server does not launch processes."
	}
	if len(cfg.Agent.Command) == 0 {
		return false, unconfigured
	}
	return true, ""
}

// errAgentLaunchDisabled is the refusal every agent route shares. Like
// errProcessLaunchDisabled in open.go it is raised at the single place the
// process is started, so the guarantee does not depend on the route list in
// processLaunchRoute staying complete.
var errAgentLaunchDisabled = errors.New("web: agent commands are unavailable in this serving mode")

// launchAgent is the one door to the configured agent command. Every /api route
// that runs an agent goes through it, so "this deployment does not launch
// processes" is enforced once rather than per handler.
func (s *Server) launchAgent(ctx context.Context, argv []string, stdin []byte) ([]byte, error) {
	if !s.caps.processLaunch {
		return nil, errAgentLaunchDisabled
	}
	return s.runAgent(ctx, argv, stdin)
}

// requestGuard is the one middleware every request passes through. It publishes
// the response headers that are true of a vault in every mode, refuses what the
// mode cannot do, refuses a state-changing request that did not come from this
// origin, and bounds the body before any handler reads it.
func (s *Server) requestGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A vault response is private in every mode. no-store is what keeps a
		// hosted deployment's shared cache — Cloudflare, a corporate proxy —
		// from retaining a note it served to one authenticated person; nosniff
		// keeps a declared Content-Type from being second-guessed; the referrer
		// policy keeps the /n/<key> a person is reading out of the Referer of
		// every external link in that note.
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		// Framing is refused everywhere. The CSRF token is no defence against a
		// click in a same-origin iframe, so a hosted deployment's write controls
		// would otherwise be clickjackable from an attacker's page. This is
		// X-Frame-Options rather than a CSP frame-ancestors directive because
		// applyMediaSafetyHeaders Sets its own Content-Security-Policy on an
		// active attachment, and Set replaces, so a guard-set CSP would not
		// survive on exactly the responses that most need a framing rule.
		w.Header().Set("X-Frame-Options", "DENY")

		if reason := s.caps.denialReason(r); reason != "" {
			writeManagementError(w, noteops.Errorf(noteops.CodeForbidden, "%s", reason))
			return
		}
		if !sameOriginRequest(r) {
			writeManagementError(w, noteops.Errorf(noteops.CodeForbidden, "cross-origin request refused"))
			return
		}
		// A declared length over the bound is refused here so the answer is an
		// honest 413 rather than whatever a handler makes of a truncated form.
		// MaxBytesReader still wraps the body afterwards, because a chunked
		// request declares no length and one that declares a small length can
		// still send more.
		// The status is set explicitly rather than derived from the code: this
		// is a transport bound, not a note-operation failure, so it borrows the
		// management envelope without claiming a noteops error class of its own.
		limit := s.caps.requestBodyLimit(r.URL.Path)
		if r.ContentLength > limit {
			writeManagementErrorStatus(w, http.StatusRequestEntityTooLarge,
				noteops.Errorf(noteops.CodeInvalidRequest, "request body exceeds %d bytes", limit))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		// Inside the redacting writer, which redactForReader finds by type.
		w, settle := s.refreshAfterFailedWrite(w, r)
		defer settle()
		if s.rootRedactor.active() {
			w = &redactingResponseWriter{ResponseWriter: w, redactor: s.rootRedactor}
		}
		next.ServeHTTP(w, r)
	})
}

// sameOriginRequest reports whether a state-changing request came from a page
// this server served. It is defence in depth beside the per-route CSRF token,
// and it matters most in hosted mode, where the server is no longer reachable
// only from loopback.
//
// Sec-Fetch-Site is preferred when the browser sends it: it is computed by the
// browser and survives a reverse proxy that rewrites Host, which an Origin/Host
// comparison does not. Absent it, a present Origin must name this host. A
// request with neither header is not a browser request — curl, the CLI, a test —
// and is left to the CSRF token, which a cross-site page still cannot read.
func sameOriginRequest(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	switch strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")) {
	case "same-origin", "none":
		return true
	case "":
		// Fall through to the Origin comparison below.
	default:
		// "cross-site" and "same-site" are both another origin.
		return false
	}

	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	// A parse failure, an opaque "null" origin (a sandboxed frame, a file:// or
	// data: document) and a scheme with no authority all land here with an empty
	// Host and are refused.
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}
