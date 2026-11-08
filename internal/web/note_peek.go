package web

import (
	"net/http"
	"strings"

	"github.com/sglavoie/brainnotes/internal/brief"
	"github.com/sglavoie/brainnotes/internal/recall"
)

// A peek reads only the served snapshot. It neither records an open nor loads
// attachments, runs agents, or writes recall state.
func (s *Server) handleNotePeek(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		http.Error(w, "Notes are temporarily unavailable.", http.StatusServiceUnavailable)
		return
	}
	note, key, ok := noteForRoute(snapshot, strings.TrimSpace(r.URL.Query().Get("key")))
	if !ok {
		http.NotFound(w, r)
		return
	}
	document, err := recall.ExtractDocument(s.vault.Root, note, recall.ExtractOptions{})
	if err != nil {
		http.Error(w, "This note could not be previewed.", http.StatusServiceUnavailable)
		return
	}
	path, err := notePathForKey(key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	writeManagementJSON(w, http.StatusOK, struct {
		Title   string `json:"title"`
		Excerpt string `json:"excerpt"`
		Path    string `json:"path"`
	}{document.Title, brief.RepresentativeExcerpt(document), path})
}
