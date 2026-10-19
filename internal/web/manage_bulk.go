package web

import (
	"net/http"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

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

	req, err := s.parseBulkManagementRequest(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	record, err := s.previewStore.consume(r.Form.Get("preview_id"))
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if err := s.validateBulkPreviewForApply(req, record); err != nil {
		writeManagementError(w, err)
		return
	}

	if len(req.Sources) == 0 {
		writeManagementJSON(w, bulkFailureStatus(req.KeyFailures), bulkManagementResponse{
			OK:       false,
			Code:     string(bulkFailureCode(req.KeyFailures)),
			Message:  "No selected notes could be resolved.",
			Failed:   len(req.KeyFailures),
			Failures: req.KeyFailures,
		})
		return
	}

	result, err := s.runBulkActionForRequest(r, req, record)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	result.Failures = append(req.KeyFailures, result.Failures...)
	if result.Changed > 0 {
		s.state.invalidate()
	}

	writeManagementJSON(w, http.StatusOK, bulkManagementResponseForResult(req.Action, result))
}

func (s *Server) runBulkActionForRequest(r *http.Request, req bulkManagementRequest, record bulkPreviewRecord) (*noteops.BulkResult, error) {
	operationID, err := operationIDFromForm(r)
	if err != nil {
		return nil, err
	}
	options := bulkPreviewApplyOptions(record)
	if operationID != "" {
		options = append(options, noteops.WithBulkJournalOptions(journalOptionsForOperation(operationID)...))
	}
	return s.runBulkAction(req, options...)
}

func (s *Server) handleNotesBulkPreview(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/notes/bulk/preview" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed, noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return
	}

	req, err := s.parseBulkManagementRequest(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if len(req.Sources) == 0 {
		writeManagementJSON(w, bulkFailureStatus(req.KeyFailures), bulkPreviewManagementResponse{
			OK:        false,
			Code:      string(bulkFailureCode(req.KeyFailures)),
			Action:    req.Action,
			Message:   "No selected notes could be resolved.",
			Failed:    len(req.KeyFailures),
			PreviewID: "",
			ExpiresAt: "",
			Failures:  req.KeyFailures,
		})
		return
	}

	result, err := s.runBulkPreview(req)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	result.Failures = append(req.KeyFailures, result.Failures...)

	receipt, err := s.previewStore.store(req, result)
	if err != nil {
		writeManagementError(w, err)
		return
	}

	writeManagementJSON(w, http.StatusOK, bulkPreviewManagementResponseForResult(req.Action, result, receipt))
}

func (s *Server) resolveBulkSources(keys []string) ([]noteops.BulkSource, []noteops.BulkFailure, error) {
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

// isFrontmatterBulkAction reports whether an action's outcome depends on the
// property definitions in brainnotes.toml.
func isFrontmatterBulkAction(action string) bool {
	return action == "frontmatter_set" || action == "frontmatter_unset"
}

func (s *Server) runBulkAction(req bulkManagementRequest, opts ...noteops.BulkOption) (*noteops.BulkResult, error) {
	if isFrontmatterBulkAction(req.Action) {
		if err := s.ensureConfigWritable(); err != nil {
			return nil, err
		}
	}
	cfg, _, pin := s.pinnedConfig()
	switch req.Action {
	case "trash":
		return noteops.BulkTrashNotes(s.vault.Root, req.Sources, opts...)
	case "move":
		return noteops.BulkMoveNotes(s.vault, cfg, req.Sources, req.MoveFolder, opts...)
	case "frontmatter_set":
		return noteops.BulkSetFrontmatter(s.vault.Root, req.Sources, req.FrontmatterMutations, &cfg.Frontmatter,
			append(opts, bulkConfigPinOptions(pin)...)...)
	case "frontmatter_unset":
		return noteops.BulkUnsetFrontmatter(s.vault.Root, req.Sources, req.FrontmatterUnsetProperties, &cfg.Frontmatter,
			append(opts, bulkConfigPinOptions(pin)...)...)
	default:
		return nil, noteops.Errorf(noteops.CodeInvalidRequest, "bulk: unsupported action %q", req.Action)
	}
}

func (s *Server) runBulkPreview(req bulkManagementRequest, opts ...noteops.BulkOption) (*noteops.BulkPreviewResult, error) {
	if isFrontmatterBulkAction(req.Action) {
		if err := s.ensureConfigWritable(); err != nil {
			return nil, err
		}
	}
	cfg := s.config()
	switch req.Action {
	case "trash":
		return noteops.BulkPreviewTrashNotes(s.vault.Root, req.Sources, opts...)
	case "move":
		return noteops.BulkPreviewMoveNotes(s.vault, cfg, req.Sources, req.MoveFolder, opts...)
	case "frontmatter_set":
		return noteops.BulkPreviewSetFrontmatter(s.vault.Root, req.Sources, req.FrontmatterMutations, &cfg.Frontmatter)
	case "frontmatter_unset":
		return noteops.BulkPreviewUnsetFrontmatter(s.vault.Root, req.Sources, req.FrontmatterUnsetProperties, &cfg.Frontmatter)
	default:
		return nil, noteops.Errorf(noteops.CodeInvalidRequest, "bulk: unsupported action %q", req.Action)
	}
}

func (s *Server) validateBulkPreviewForApply(req bulkManagementRequest, record bulkPreviewRecord) error {
	currentStates, err := bulkPreviewCurrentAffectedStates(record.Affected)
	if err != nil {
		return err
	}
	if !bulkPreviewStatesMatch(record.Affected, currentStates) {
		return noteops.Errorf(noteops.CodePreviewStale, "bulk preview is stale")
	}
	if err := bulkPreviewRecordMatchesRequest(record, req); err != nil {
		return err
	}

	preview, err := s.runBulkPreview(req, bulkPreviewApplyOptions(record)...)
	if err != nil {
		return noteops.WrapError(noteops.CodePreviewStale, "bulk preview is stale", err)
	}
	recomputedStates, err := bulkPreviewAffectedStates(preview)
	if err != nil {
		return err
	}
	if !bulkPreviewStatesMatch(record.Affected, recomputedStates) {
		return noteops.Errorf(noteops.CodePreviewStale, "bulk preview is stale")
	}
	return nil
}

func bulkPreviewApplyOptions(record bulkPreviewRecord) []noteops.BulkOption {
	if len(record.TrashPlans) == 0 {
		return nil
	}
	return []noteops.BulkOption{noteops.WithBulkTrashDestinationPlans(record.TrashPlans)}
}
