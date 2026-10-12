package web

import (
	"net/http"
	"strings"
	"testing"
)

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
