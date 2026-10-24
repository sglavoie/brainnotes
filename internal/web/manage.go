package web

import (
	"net/http"
	"strings"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/noteops"
)

func (s *Server) handleNoteTrash(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/notes/trash" {
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
	token := requestCSRFToken(r)
	if !csrfTokenMatches(s.csrfToken, token) {
		writeManagementError(w, noteops.Errorf(noteops.CodeForbidden, "csrf token required"))
		return
	}

	key := strings.TrimSpace(r.Form.Get("key"))
	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "snapshot error", err))
		return
	}
	_, normalizedKey, source, err := noteops.ResolveExistingRouteNote(s.vault.Root, snapshot.notes, snapshot.graph, key)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	routePath, err := notePathForKey(normalizedKey)
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInvalidKey, "render note route", err))
		return
	}

	result, err := s.trashNoteForRequest(r, source.RelPath)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	s.state.invalidate()

	journalID := ""
	if result.Journal != nil {
		journalID = result.Journal.ID
	}
	writeManagementJSON(w, http.StatusOK, noteops.ResultForPath(
		"Moved note to Trash.",
		normalizedKey,
		routePath,
		result.Destination.RelPath,
		journalID,
	))
}

func (s *Server) trashNoteForRequest(r *http.Request, relPath string) (*noteops.TrashResult, error) {
	operationID, err := operationIDFromForm(r)
	if err != nil {
		return nil, err
	}
	return noteops.TrashNote(s.vault.Root, relPath,
		noteops.WithTrashJournalOptions(journalOptionsForOperation(operationID)...))
}

func (s *Server) handleNoteCreate(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/notes/create" {
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

	source, err := s.resolveManagementSource(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	target, ok := managementFormValue(r, "target", "broken_target")
	if !ok {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "create: missing target"))
		return
	}
	result, err := noteops.CreateStubFromBrokenTarget(s.vault.Root, source.RelPath, target)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	s.state.invalidate()

	newKey := managementRouteKeyForRelPath(result.Destination.RelPath)
	routePath, err := notePathForKey(newKey)
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInvalidKey, "render note route", err))
		return
	}
	writeManagementJSON(w, http.StatusOK, noteops.ResultForPath(
		"Created note.",
		newKey,
		routePath,
		result.Destination.RelPath,
		managementJournalID(result.Journal),
	))
}

func (s *Server) handleNoteRename(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	if r.URL.Path != "/api/notes/rename" {
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

	source, err := s.resolveManagementSource(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	newTitle, ok := managementFormValue(r, "title", "new_title")
	if !ok {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "rename: missing new title"))
		return
	}
	result, err := noteops.RenameNote(s.vault, cfg, source.RelPath, newTitle)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	s.state.invalidate()

	newKey := managementRouteKeyForRelPath(result.Destination.RelPath)
	routePath, err := notePathForKey(newKey)
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInvalidKey, "render note route", err))
		return
	}
	writeManagementJSON(w, http.StatusOK, noteops.ResultForPath(
		"Renamed note.",
		newKey,
		routePath,
		result.Destination.RelPath,
		managementJournalID(result.Journal),
	))
}

func (s *Server) handleNoteMove(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	if r.URL.Path != "/api/notes/move" {
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

	source, err := s.resolveManagementSource(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	newFolder, ok := managementFormValue(r, "folder", "new_folder")
	if !ok {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "move: missing new folder"))
		return
	}
	result, err := noteops.MoveNote(s.vault, cfg, source.RelPath, newFolder)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	s.state.invalidate()

	newKey := managementRouteKeyForRelPath(result.Destination.RelPath)
	routePath, err := notePathForKey(newKey)
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInvalidKey, "render note route", err))
		return
	}
	writeManagementJSON(w, http.StatusOK, noteops.ResultForPath(
		"Moved note.",
		newKey,
		routePath,
		result.Destination.RelPath,
		managementJournalID(result.Journal),
	))
}

func (s *Server) handleNoteFrontmatterSet(w http.ResponseWriter, r *http.Request) {
	if !s.requireManagementPost(w, r, "/api/notes/frontmatter/set", 0) {
		return
	}
	if err := s.ensureConfigWritable(); err != nil {
		writeManagementError(w, err)
		return
	}
	cfg, _, pin := s.pinnedConfig()

	source, err := s.resolveManagementSource(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	mutation, err := frontmatterMutationFromForm(r, &cfg.Frontmatter)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	result, err := noteops.SetFrontmatter(s.vault.Root, source.RelPath, []noteops.FrontmatterMutation{mutation}, &cfg.Frontmatter,
		frontmatterConfigPinOptions(pin)...)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if result.Changed {
		s.state.invalidate()
	}

	message := "Updated property."
	if !result.Changed {
		message = "Property unchanged."
	}
	writeManagementJSON(w, http.StatusOK, noteops.ResultForPath(
		message,
		managementRouteKeyForRelPath(result.Source.RelPath),
		mustManagementNotePath(result.Source.RelPath),
		result.Source.RelPath,
		managementJournalID(result.Journal),
	))
}

func frontmatterMutationFromForm(r *http.Request, fm *config.FrontmatterConfig) (noteops.FrontmatterMutation, error) {
	property, ok := managementFormValue(r, "property", "prop", "frontmatter_key")
	if !ok {
		return noteops.FrontmatterMutation{}, noteops.Errorf(noteops.CodeInvalidRequest, "frontmatter: missing property")
	}
	op := strings.TrimSpace(r.Form.Get("op"))
	if op == "" {
		op = "="
	}
	value, ok := managementFormValue(r, "value")
	if !ok {
		return noteops.FrontmatterMutation{}, noteops.Errorf(noteops.CodeInvalidRequest, "frontmatter: missing value")
	}
	return noteops.NewFrontmatterMutation(property, op, value, strings.TrimSpace(r.Form.Get("type")), fm)
}

func (s *Server) handleNoteFrontmatterUnset(w http.ResponseWriter, r *http.Request) {
	if !s.requireManagementPost(w, r, "/api/notes/frontmatter/unset", 0) {
		return
	}
	if err := s.ensureConfigWritable(); err != nil {
		writeManagementError(w, err)
		return
	}
	cfg, _, pin := s.pinnedConfig()

	source, err := s.resolveManagementSource(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	property, ok := managementFormValue(r, "property", "prop", "frontmatter_key")
	if !ok {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "frontmatter: missing property"))
		return
	}
	result, err := noteops.UnsetFrontmatter(s.vault.Root, source.RelPath, []string{property}, &cfg.Frontmatter,
		frontmatterConfigPinOptions(pin)...)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if result.Changed {
		s.state.invalidate()
	}

	message := "Removed property."
	if !result.Changed {
		message = "Property unchanged."
	}
	writeManagementJSON(w, http.StatusOK, noteops.ResultForPath(
		message,
		managementRouteKeyForRelPath(result.Source.RelPath),
		mustManagementNotePath(result.Source.RelPath),
		result.Source.RelPath,
		managementJournalID(result.Journal),
	))
}

func (s *Server) resolveManagementSource(r *http.Request) (noteops.PathResolution, error) {
	key := strings.TrimSpace(r.Form.Get("key"))
	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		return noteops.PathResolution{}, noteops.WrapError(noteops.CodeInternal, "snapshot error", err)
	}
	_, _, source, err := noteops.ResolveExistingRouteNote(s.vault.Root, snapshot.notes, snapshot.graph, key)
	if err != nil {
		return noteops.PathResolution{}, err
	}
	return source, nil
}
