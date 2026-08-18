package web

import (
	"net/http"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

// handleCapture appends one quick-capture line to the configured inbox note.
// It is the second documented narrow unpreviewed write (after
// /api/properties/value): one note, strictly additive, and safe because the
// append transaction is pinned to the revision it planned against rather than
// to a hash the client echoed back — the quick-capture box never loaded the
// note and has no hash to send.
//
// The server owns the clock. The rendered bullet comes back in Detail so the
// browser can show exactly what landed instead of guessing the timestamp.
func (s *Server) handleCapture(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/capture" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed, noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return
	}
	if err := r.ParseForm(); err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInvalidRequest, "invalid request form", err))
		return
	}
	if !csrfTokenMatches(s.csrfToken, requestCSRFToken(r)) {
		writeManagementError(w, noteops.Errorf(noteops.CodeForbidden, "csrf token required"))
		return
	}

	cfg := s.config()
	relPath, heading, configured := captureDestination(cfg.Capture.InboxNote, cfg.Capture.Heading)
	if !configured {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "capture: quick capture is not configured"))
		return
	}
	text := r.Form.Get("text")
	if strings.TrimSpace(text) == "" {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "capture: text is required"))
		return
	}

	result, err := noteops.ApplyCapture(
		s.vault.Root,
		relPath,
		noteops.CaptureRequest{Heading: heading, Text: text, At: time.Now()},
		&cfg.Frontmatter,
		noteops.WithCaptureCommand("bn web capture"),
	)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	s.state.invalidate()

	key := managementRouteKeyForRelPath(result.Destination.RelPath)
	routePath, err := notePathForKey(key)
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInvalidKey, "render note route", err))
		return
	}

	message := "Captured to " + result.Destination.RelPath + "."
	if result.CreatedNote {
		message = "Created " + result.Destination.RelPath + " and captured."
	}
	payload := noteops.ResultForPath(
		message,
		key,
		routePath,
		result.Destination.RelPath,
		managementJournalID(result.Journal),
	)
	payload.Detail = result.Line
	writeManagementJSON(w, http.StatusOK, payload)
}

// captureDestination reports the configured inbox note and heading. Both are
// required: a hand-built configuration (every web test does exactly that) leaves
// them zero rather than defaulted, and an empty heading is unparseable by the
// capture scanner.
func captureDestination(inboxNote, heading string) (string, string, bool) {
	relPath := strings.TrimSpace(inboxNote)
	trimmedHeading := strings.TrimSpace(heading)
	return relPath, trimmedHeading, relPath != "" && trimmedHeading != ""
}
