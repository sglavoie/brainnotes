package web

import (
	"net/http"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
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
	data := struct{ CSRFToken, CreatePath, EditorPath, SuggestedTitle, VaultID string }{
		s.csrfToken, newNoteAPIRoutePath, editorRoutePath, r.URL.Query().Get("title"), s.vaultIdentity(),
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
	if req.SaveID == "" {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "new note: save_id is required"))
		return
	}
	s.applySourceSave(w, req, nil)
}
