package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
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

	result, err := noteops.TrashNote(s.vault.Root, source.RelPath)
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
	result, err := noteops.RenameNote(s.vault, s.cfg, source.RelPath, newTitle)
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
	result, err := noteops.MoveNote(s.vault, s.cfg, source.RelPath, newFolder)
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
	if r.URL.Path != "/api/notes/frontmatter/set" {
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
	property, ok := managementFormValue(r, "property", "prop", "frontmatter_key")
	if !ok {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "frontmatter: missing property"))
		return
	}
	op := strings.TrimSpace(r.Form.Get("op"))
	if op == "" {
		op = "="
	}
	value, ok := managementFormValue(r, "value")
	if !ok {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "frontmatter: missing value"))
		return
	}
	typeFlag := strings.TrimSpace(r.Form.Get("type"))
	if typeFlag == "" {
		typeFlag = frontmatterSchemaTypeFlag(s.cfg.Frontmatter.Schema[property].Type)
	}

	mutation, err := noteops.NewFrontmatterMutation(property, op, value, typeFlag, &s.cfg.Frontmatter)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	result, err := noteops.SetFrontmatter(s.vault.Root, source.RelPath, []noteops.FrontmatterMutation{mutation}, &s.cfg.Frontmatter)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if result.Changed {
		s.state.invalidate()
	}

	message := "Updated frontmatter."
	if !result.Changed {
		message = "Frontmatter unchanged."
	}
	writeManagementJSON(w, http.StatusOK, noteops.ResultForPath(
		message,
		managementRouteKeyForRelPath(result.Source.RelPath),
		mustManagementNotePath(result.Source.RelPath),
		result.Source.RelPath,
		managementJournalID(result.Journal),
	))
}

func (s *Server) handleNoteFrontmatterUnset(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/notes/frontmatter/unset" {
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
	property, ok := managementFormValue(r, "property", "prop", "frontmatter_key")
	if !ok {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "frontmatter: missing property"))
		return
	}
	result, err := noteops.UnsetFrontmatter(s.vault.Root, source.RelPath, []string{property}, &s.cfg.Frontmatter)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if result.Changed {
		s.state.invalidate()
	}

	message := "Unset frontmatter."
	if !result.Changed {
		message = "Frontmatter unchanged."
	}
	writeManagementJSON(w, http.StatusOK, noteops.ResultForPath(
		message,
		managementRouteKeyForRelPath(result.Source.RelPath),
		mustManagementNotePath(result.Source.RelPath),
		result.Source.RelPath,
		managementJournalID(result.Journal),
	))
}

func (s *Server) handleNotesBulk(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/notes/bulk" {
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

	action := strings.TrimSpace(r.Form.Get("action"))
	if action == "" {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "bulk: missing action"))
		return
	}

	sources, keyFailures, err := s.resolveBulkSources(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if len(sources) == 0 {
		writeManagementJSON(w, bulkFailureStatus(keyFailures), bulkManagementResponse{
			OK:       false,
			Code:     string(bulkFailureCode(keyFailures)),
			Message:  "No selected notes could be resolved.",
			Failed:   len(keyFailures),
			Failures: keyFailures,
		})
		return
	}

	result, err := s.runBulkAction(action, r, sources)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	result.Failures = append(keyFailures, result.Failures...)
	if result.Changed > 0 {
		s.state.invalidate()
	}

	writeManagementJSON(w, http.StatusOK, bulkManagementResponseForResult(action, result))
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

func (s *Server) resolveBulkSources(r *http.Request) ([]noteops.BulkSource, []noteops.BulkFailure, error) {
	keys := readBulkKeys(r.Form)
	if len(keys) == 0 {
		return nil, nil, noteops.Errorf(noteops.CodeInvalidRequest, "bulk: no notes selected")
	}

	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		return nil, nil, noteops.WrapError(noteops.CodeInternal, "snapshot error", err)
	}

	seen := make(map[string]struct{}, len(keys))
	sources := make([]noteops.BulkSource, 0, len(keys))
	failures := make([]noteops.BulkFailure, 0)
	for _, key := range keys {
		_, normalizedKey, source, err := noteops.ResolveExistingRouteNote(s.vault.Root, snapshot.notes, snapshot.graph, key)
		if err != nil {
			failures = append(failures, noteops.BulkFailureForError(key, "", err))
			continue
		}
		if _, ok := seen[normalizedKey]; ok {
			continue
		}
		seen[normalizedKey] = struct{}{}
		sources = append(sources, noteops.BulkSource{
			Key:     normalizedKey,
			RelPath: source.RelPath,
		})
	}
	return sources, failures, nil
}

func (s *Server) runBulkAction(action string, r *http.Request, sources []noteops.BulkSource) (*noteops.BulkResult, error) {
	switch action {
	case "trash":
		return noteops.BulkTrashNotes(s.vault.Root, sources)
	case "move":
		newFolder, ok := managementFormValue(r, "folder", "new_folder")
		if !ok {
			return nil, noteops.Errorf(noteops.CodeInvalidRequest, "move: missing new folder")
		}
		return noteops.BulkMoveNotes(s.vault, s.cfg, sources, newFolder)
	case "frontmatter_set", "frontmatter-set", "set_frontmatter":
		property, ok := managementFormValue(r, "property", "prop", "frontmatter_key")
		if !ok {
			return nil, noteops.Errorf(noteops.CodeInvalidRequest, "frontmatter: missing property")
		}
		op := strings.TrimSpace(r.Form.Get("op"))
		if op == "" {
			op = "="
		}
		value, ok := managementFormValue(r, "value")
		if !ok {
			return nil, noteops.Errorf(noteops.CodeInvalidRequest, "frontmatter: missing value")
		}
		typeFlag := strings.TrimSpace(r.Form.Get("type"))
		if typeFlag == "" {
			typeFlag = frontmatterSchemaTypeFlag(s.cfg.Frontmatter.Schema[property].Type)
		}
		mutation, err := noteops.NewFrontmatterMutation(property, op, value, typeFlag, &s.cfg.Frontmatter)
		if err != nil {
			return nil, err
		}
		return noteops.BulkSetFrontmatter(s.vault.Root, sources, []noteops.FrontmatterMutation{mutation}, &s.cfg.Frontmatter)
	case "frontmatter_unset", "frontmatter-unset", "unset_frontmatter":
		property, ok := managementFormValue(r, "property", "prop", "frontmatter_key")
		if !ok {
			return nil, noteops.Errorf(noteops.CodeInvalidRequest, "frontmatter: missing property")
		}
		return noteops.BulkUnsetFrontmatter(s.vault.Root, sources, []string{property}, &s.cfg.Frontmatter)
	default:
		return nil, noteops.Errorf(noteops.CodeInvalidRequest, "bulk: unsupported action %q", action)
	}
}

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

func readBulkKeys(form map[string][]string) []string {
	values := make([]string, 0)
	for _, name := range []string{"keys[]", "keys", "key"} {
		values = append(values, form[name]...)
	}

	keys := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		key := strings.TrimSpace(value)
		if key == "" {
			continue
		}
		folded := strings.ToLower(key)
		if _, ok := seen[folded]; ok {
			continue
		}
		seen[folded] = struct{}{}
		keys = append(keys, key)
	}
	return keys
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

type bulkManagementResponse struct {
	OK        bool                  `json:"ok"`
	Code      string                `json:"code,omitempty"`
	Message   string                `json:"message"`
	Changed   int                   `json:"changed"`
	Unchanged int                   `json:"unchanged,omitempty"`
	Failed    int                   `json:"failed"`
	JournalID string                `json:"journal_id,omitempty"`
	Results   []bulkManagementItem  `json:"results,omitempty"`
	Failures  []noteops.BulkFailure `json:"failures,omitempty"`
}

type bulkManagementItem struct {
	Key     string `json:"key"`
	Path    string `json:"path"`
	RelPath string `json:"rel_path"`
	Changed bool   `json:"changed"`
}

func bulkManagementResponseForResult(action string, result *noteops.BulkResult) bulkManagementResponse {
	if result == nil {
		return bulkManagementResponse{
			OK:      false,
			Code:    string(noteops.CodeInternal),
			Message: "Bulk action failed.",
		}
	}
	return bulkManagementResponse{
		OK:        true,
		Message:   bulkManagementMessage(action, result),
		Changed:   result.Changed,
		Unchanged: result.Unchanged,
		Failed:    len(result.Failures),
		JournalID: managementJournalID(result.Journal),
		Results:   bulkManagementItems(result),
		Failures:  result.Failures,
	}
}

func bulkManagementItems(result *noteops.BulkResult) []bulkManagementItem {
	items := make([]bulkManagementItem, 0, len(result.Results))
	for _, item := range result.Results {
		relPath := item.Source.RelPath
		if item.Destination.RelPath != "" {
			relPath = item.Destination.RelPath
		}
		key := managementRouteKeyForRelPath(relPath)
		items = append(items, bulkManagementItem{
			Key:     key,
			Path:    mustManagementNotePath(relPath),
			RelPath: relPath,
			Changed: item.Changed,
		})
	}
	return items
}

func bulkManagementMessage(action string, result *noteops.BulkResult) string {
	changed := result.Changed
	failures := len(result.Failures)
	var message string
	switch action {
	case "trash":
		message = fmt.Sprintf("Moved %s to Trash.", noteCountLabel(changed))
	case "move":
		message = fmt.Sprintf("Moved %s.", noteCountLabel(changed))
	case "frontmatter_set", "frontmatter-set", "set_frontmatter":
		if changed == 0 {
			message = "Frontmatter unchanged on selected notes."
		} else {
			message = fmt.Sprintf("Updated frontmatter on %s.", noteCountLabel(changed))
		}
	case "frontmatter_unset", "frontmatter-unset", "unset_frontmatter":
		if changed == 0 {
			message = "Frontmatter unchanged on selected notes."
		} else {
			message = fmt.Sprintf("Unset frontmatter on %s.", noteCountLabel(changed))
		}
	default:
		message = fmt.Sprintf("Updated %s.", noteCountLabel(changed))
	}
	if failures > 0 {
		message += fmt.Sprintf(" %d failed.", failures)
	}
	return message
}

func noteCountLabel(count int) string {
	if count == 1 {
		return "1 note"
	}
	return fmt.Sprintf("%d notes", count)
}

func bulkFailureCode(failures []noteops.BulkFailure) noteops.ErrorCode {
	if len(failures) == 0 || failures[0].Code == "" {
		return noteops.CodeInvalidRequest
	}
	return failures[0].Code
}

func bulkFailureStatus(failures []noteops.BulkFailure) int {
	switch bulkFailureCode(failures) {
	case noteops.CodeInvalidRequest, noteops.CodeInvalidKey:
		return http.StatusBadRequest
	case noteops.CodeForbidden:
		return http.StatusForbidden
	case noteops.CodeNotFound:
		return http.StatusNotFound
	case noteops.CodeConflict:
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func mustManagementNotePath(relPath string) string {
	routePath, err := notePathForKey(managementRouteKeyForRelPath(relPath))
	if err != nil {
		return ""
	}
	return routePath
}

func frontmatterSchemaTypeFlag(schemaType string) string {
	switch strings.TrimSpace(schemaType) {
	case "string":
		return "raw"
	case "date", "int", "bool", "float":
		return schemaType
	case "list":
		return "raw"
	default:
		return ""
	}
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

func writeManagementError(w http.ResponseWriter, err error) {
	writeManagementErrorStatus(w, noteops.HTTPStatusForError(err), err)
}

func writeManagementErrorStatus(w http.ResponseWriter, status int, err error) {
	if err == nil {
		err = noteops.Errorf(noteops.CodeInternal, "internal error")
	}
	writeManagementJSON(w, status, noteops.ErrorResponse(err))
}

func writeManagementJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
