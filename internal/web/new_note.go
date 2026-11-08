package web

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/sglavoie/brainnotes/internal/noteops"
)

const newNoteRoutePath = "/new"
const newNoteAPIRoutePath = "/api/notes/new"

func (s *Server) handleNewNotePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != newNoteRoutePath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := s.connectionDraft(r.URL.Query()["source"])
	if err != nil {
		writeManagementError(w, err)
		return
	}
	data := struct{ CSRFToken, CreatePath, EditorPath, SuggestedTitle, VaultID, SuggestedBody string }{
		s.csrfToken, newNoteAPIRoutePath, editorRoutePath, r.URL.Query().Get("title"), s.vaultIdentity(), body,
	}
	if err := s.templates.ExecuteTemplate(w, "new-note", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (s *Server) handleNewNote(w http.ResponseWriter, r *http.Request) {
	if !s.requireManagementPost(w, r, newNoteAPIRoutePath, remoteMaxRequestBody) {
		return
	}
	req, err := noteops.NewNoteSource(r.Form.Get("title"), r.Form.Get("folder"))
	if err != nil {
		writeManagementError(w, err)
		return
	}
	req.SaveID = strings.TrimSpace(r.Form.Get("save_id"))
	body := r.Form.Get("body")
	if utf8.RuneCountInString(body) > 20000 {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "new note: initial writing is limited to 20,000 characters"))
		return
	}
	if body != "" {
		req.Content += "\n" + body
	}
	if req.SaveID == "" {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "new note: save_id is required"))
		return
	}
	s.applySourceSave(w, req, nil)
}

func (s *Server) connectionDraft(keys []string) (string, error) {
	if len(keys) == 0 {
		return "", nil
	}
	if len(keys) != 2 {
		return "", noteops.Errorf(noteops.CodeInvalidRequest, "a connection needs two source notes")
	}
	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		return "", err
	}
	links := make([]string, 0, 2)
	for _, key := range keys {
		note, _, ok := noteForRoute(snapshot, key)
		if !ok {
			return "", noteops.Errorf(noteops.CodeNotFound, "a source note is no longer available")
		}
		links = append(links, "[["+wikilinkTargetForNote(s.vault.Root, note)+"]]")
	}
	return "## Connection\n\n" + strings.Join(links, " ↔ ") + "\n\n", nil
}
