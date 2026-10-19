package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

func managementFormValue(r *http.Request, names ...string) (string, bool) {
	for _, name := range names {
		values, ok := r.Form[name]
		if !ok || len(values) == 0 {
			continue
		}
		return values[0], true
	}
	return "", false
}

func managementRouteKeyForRelPath(relPath string) string {
	relPath = strings.TrimSpace(filepath.ToSlash(relPath))
	ext := path.Ext(relPath)
	if strings.EqualFold(ext, ".md") {
		relPath = strings.TrimSuffix(relPath, ext)
	}
	relPath = strings.TrimPrefix(path.Clean("/"+relPath), "/")
	return strings.ToLower(relPath)
}

func mustManagementNotePath(relPath string) string {
	routePath, err := notePathForKey(managementRouteKeyForRelPath(relPath))
	if err != nil {
		return ""
	}
	return routePath
}

func managementJournalID(entry *journal.Entry) string {
	if entry == nil {
		return ""
	}
	return entry.ID
}

func requestCSRFToken(r *http.Request) string {
	token := strings.TrimSpace(r.Header.Get(csrfHeaderName))
	if token == "" {
		token = strings.TrimSpace(r.Form.Get("token"))
	}
	return token
}

// requireManagementPost is the preamble a management POST shares with every
// other one: the exact route, POST only, a bounded and parsed form, and a CSRF
// token that matches. It answers in the management envelope and reports whether
// the handler may continue.
//
// A bodyLimit of zero leaves the profile-wide bound requestGuard already applied
// in place, which is what a route with no reason to be stricter than its
// deployment wants. A route that is stricter passes its own.
func (s *Server) requireManagementPost(w http.ResponseWriter, r *http.Request, route string, bodyLimit int64) bool {
	if r.URL.Path != route {
		http.NotFound(w, r)
		return false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed, noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return false
	}
	if bodyLimit > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
	}
	if err := r.ParseForm(); err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInvalidRequest, "invalid request form", err))
		return false
	}
	if !csrfTokenMatches(s.csrfToken, requestCSRFToken(r)) {
		writeManagementError(w, noteops.Errorf(noteops.CodeForbidden, "csrf token required"))
		return false
	}
	return true
}

func writeManagementError(w http.ResponseWriter, err error) {
	if errors.Is(err, errRequestBodyTooLarge) {
		writeManagementErrorStatus(w, http.StatusRequestEntityTooLarge, err)
		return
	}
	writeManagementErrorStatus(w, noteops.HTTPStatusForError(err), err)
}

func writeManagementErrorStatus(w http.ResponseWriter, status int, err error) {
	if err == nil {
		err = noteops.Errorf(noteops.CodeInternal, "internal error")
	}
	response := noteops.ErrorResponse(err)
	response.Message = redactForReader(w, response.Message)
	writeManagementJSON(w, status, response)
}

func writeManagementJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
