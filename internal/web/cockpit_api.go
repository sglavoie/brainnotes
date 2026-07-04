package web

import (
	"net/http"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

// cockpitFixCommand labels the undo-journal entries cockpit fixes create when
// applied through the web APIs.
const cockpitFixCommand = "bn cockpit fix"

// cockpitStateWarning is surfaced (as a queue warning) when the workflow-state
// file cannot be loaded, so the client still sees every generated task.
const cockpitStateWarning = "Cockpit state could not be loaded; showing all generated tasks."

// Item-endpoint triage actions. mark_done and snooze mirror the ActionKind
// vocabulary the task advertises; clear is the reverse of both (it returns a
// suppressed task to the active queue). "done" is accepted as an alias for
// mark_done so callers can reuse the review vocabulary.
const (
	cockpitItemActionDone   = "mark_done"
	cockpitItemActionSnooze = "snooze"
	cockpitItemActionClear  = "clear"
)

// cockpitAPIResponse is the GET /api/cockpit and item-mutation payload. It
// embeds cockpit.Queue so the tasks/counts/warnings schema is identical to
// `bn doctor --json`, and adds the ok/message envelope the web APIs share.
type cockpitAPIResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	cockpit.Queue
}

// fixPreviewResponse is the POST /api/fixes/preview payload. It embeds the
// planner's FixPlan (action/task_id/previews/skipped) and adds the one-use
// receipt the client must echo back to apply.
type fixPreviewResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	cockpit.FixPlan
	PreviewID string `json:"preview_id"`
	ExpiresAt string `json:"expires_at"`
}

// fixApplyResponse is the POST /api/fixes/apply payload. It embeds the applied
// FixOutcome (action/task_id/changed/created/skipped) and surfaces the undo
// journal id separately since FixOutcome.Journal is not serialized.
type fixApplyResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	cockpit.FixOutcome
	JournalID string `json:"journal_id,omitempty"`
}

func (s *Server) handleCockpitQueue(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/cockpit" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed, noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return
	}

	queue, err := s.cockpitQueueFiltered()
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "build cockpit queue", err))
		return
	}
	writeManagementJSON(w, http.StatusOK, cockpitAPIResponse{OK: true, Queue: queue})
}

func (s *Server) handleCockpitItem(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/cockpit/item" {
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

	id := strings.TrimSpace(r.Form.Get("id"))
	if id == "" {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "cockpit task id is required"))
		return
	}
	action := strings.TrimSpace(r.Form.Get("action"))
	if action == "" {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "cockpit task action is required"))
		return
	}

	// Look up the task from the raw generated queue: a done/snoozed task is still
	// generated (only FilterQueue hides it), so it must be resolvable here to be
	// cleared or re-suppressed.
	rawQueue, err := s.cockpitQueue()
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "build cockpit queue", err))
		return
	}
	task, ok := cockpitTaskByID(rawQueue, id)
	if !ok {
		writeManagementError(w, noteops.Errorf(noteops.CodeNotFound, "cockpit task not found or stale"))
		return
	}

	store := s.cockpitStore()
	message := ""
	switch action {
	case cockpitItemActionDone, "done":
		if err := store.MarkDone(task); err != nil {
			writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "update cockpit state", err))
			return
		}
		message = "Marked task done."
	case cockpitItemActionSnooze:
		option := strings.TrimSpace(r.Form.Get("snooze"))
		if option == "" {
			option = cockpit.SnoozeTomorrow
		}
		if option != cockpit.SnoozeTomorrow && option != cockpit.SnoozeNextWeek {
			writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "unsupported snooze %q", option))
			return
		}
		if err := store.Snooze(task, option); err != nil {
			writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "update cockpit state", err))
			return
		}
		message = cockpitSnoozeMessage(option)
	case cockpitItemActionClear:
		if err := store.Clear(task); err != nil {
			writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "update cockpit state", err))
			return
		}
		message = "Cleared task state."
	default:
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "unsupported cockpit action %q", action))
		return
	}

	queue, err := s.cockpitQueueFiltered()
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "build cockpit queue", err))
		return
	}
	writeManagementJSON(w, http.StatusOK, cockpitAPIResponse{OK: true, Message: message, Queue: queue})
}

func (s *Server) handleFixesPreview(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/fixes/preview" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed, noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return
	}

	id, action, err := s.parseFixRequest(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	queue, err := s.cockpitQueue()
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "build cockpit queue", err))
		return
	}
	task, ok := cockpitTaskByID(queue, id)
	if !ok {
		writeManagementError(w, noteops.Errorf(noteops.CodeNotFound, "cockpit task not found or stale"))
		return
	}

	fixer := cockpit.NewFixer(s.vault, s.cfg, cockpitFixCommand)
	plan, err := fixer.Plan(task, action)
	if err != nil {
		writeManagementError(w, cockpitFixError(err))
		return
	}
	receipt, err := s.fixPreviews.store(task, action)
	if err != nil {
		writeManagementError(w, err)
		return
	}

	writeManagementJSON(w, http.StatusOK, fixPreviewResponse{
		OK:        true,
		Message:   fixPreviewMessage(plan),
		FixPlan:   plan,
		PreviewID: receipt.ID,
		ExpiresAt: receipt.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleFixesApply(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/fixes/apply" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed, noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return
	}

	id, action, err := s.parseFixRequest(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}

	// Redeem the one-use receipt before touching the vault so an expired or
	// replayed apply fails closed with preview_required.
	record, err := s.fixPreviews.consume(r.Form.Get("preview_id"))
	if err != nil {
		writeManagementError(w, err)
		return
	}
	if record.TaskID != id || record.Action != action {
		writeManagementError(w, noteops.Errorf(noteops.CodePreviewRequired, "fix preview does not match the request"))
		return
	}

	// Resolve the current task by the previewed id. Because a task id embeds the
	// on-disk hash of every affected note, any change to the underlying files
	// yields a different id: the previewed task simply vanishes. A valid receipt
	// whose task is gone means the world moved on since the preview — stale.
	queue, err := s.cockpitQueue()
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "build cockpit queue", err))
		return
	}
	task, ok := cockpitTaskByID(queue, id)
	if !ok {
		writeManagementError(w, noteops.Errorf(noteops.CodePreviewStale, "fix preview is stale"))
		return
	}
	if record.Signature != task.Signature {
		writeManagementError(w, noteops.Errorf(noteops.CodePreviewStale, "fix preview is stale"))
		return
	}

	fixer := cockpit.NewFixer(s.vault, s.cfg, cockpitFixCommand)
	outcome, err := fixer.Apply(task, action)
	if err != nil {
		writeManagementError(w, cockpitFixError(err))
		return
	}
	if len(outcome.Changed) > 0 || len(outcome.Created) > 0 {
		s.state.invalidate()
	}

	writeManagementJSON(w, http.StatusOK, fixApplyResponse{
		OK:         true,
		Message:    fixApplyMessage(outcome),
		FixOutcome: outcome,
		JournalID:  managementJournalID(outcome.Journal),
	})
}

// parseFixRequest parses and authenticates a fix preview/apply request, then
// returns the requested task id and fix action. Preview and apply share it so
// they agree on CSRF handling and required fields; each does its own task
// lookup (apply first redeems a receipt) so the two flows can classify a missing
// task differently (not_found for preview, preview_stale for apply).
func (s *Server) parseFixRequest(r *http.Request) (string, cockpit.ActionKind, error) {
	if err := r.ParseForm(); err != nil {
		return "", "", noteops.WrapError(noteops.CodeInvalidRequest, "invalid request form", err)
	}
	if !csrfTokenMatches(s.csrfToken, requestCSRFToken(r)) {
		return "", "", noteops.Errorf(noteops.CodeForbidden, "csrf token required")
	}

	id := strings.TrimSpace(r.Form.Get("id"))
	if id == "" {
		return "", "", noteops.Errorf(noteops.CodeInvalidRequest, "cockpit task id is required")
	}
	action := cockpit.ActionKind(strings.TrimSpace(r.Form.Get("action")))
	if action == "" {
		return "", "", noteops.Errorf(noteops.CodeInvalidRequest, "fix action is required")
	}
	return id, action, nil
}

// cockpitQueue builds the raw, unfiltered generated queue.
func (s *Server) cockpitQueue() (cockpit.Queue, error) {
	return cockpit.BuildQueue(s.vault, s.cfg)
}

// cockpitQueueFiltered builds the generated queue and hides tasks the workflow
// state suppresses. If the state file cannot be loaded it degrades gracefully:
// the unfiltered queue is returned with a warning so triage never fails hard.
func (s *Server) cockpitQueueFiltered() (cockpit.Queue, error) {
	queue, err := cockpit.BuildQueue(s.vault, s.cfg)
	if err != nil {
		return cockpit.Queue{}, err
	}
	return s.filterCockpitQueue(queue), nil
}

// cockpitQueueFilteredFrom builds the filtered queue from an already-loaded
// diagnostics snapshot, so a single vault walk feeds both the homepage
// diagnostics summary and its fixes panel. The result matches
// cockpitQueueFiltered for the same load; only handleIndex has a snapshot in
// hand, so the other cockpit endpoints keep walking via cockpitQueueFiltered.
func (s *Server) cockpitQueueFilteredFrom(data diagnosticsData) cockpit.Queue {
	snapshot := cockpit.Snapshot{
		Notes:             cockpitLoadedNotes(data.loaded),
		Graph:             data.graph,
		FrontmatterErrors: cockpitParseErrors(data.frontmatterErrors),
	}
	queue := cockpit.BuildQueueFromSnapshot(snapshot, s.cfg, s.vault.Root)
	return s.filterCockpitQueue(queue)
}

// filterCockpitQueue hides workflow-suppressed tasks, degrading to the
// unfiltered queue plus a warning when the state file cannot be loaded.
func (s *Server) filterCockpitQueue(queue cockpit.Queue) cockpit.Queue {
	filtered, ferr := s.cockpitStore().FilterQueue(queue)
	if ferr == nil {
		return filtered
	}
	queue.Warnings = append(queue.Warnings, cockpit.Warning{Message: cockpitStateWarning})
	return queue
}

// cockpitLoadedNotes bridges the web diagnostics loaded-note view onto the
// exported cockpit snapshot type so the cockpit package need not import web's
// unexported loader types.
func cockpitLoadedNotes(loaded []diagnosticLoadedNote) []cockpit.LoadedNote {
	out := make([]cockpit.LoadedNote, 0, len(loaded))
	for _, item := range loaded {
		out = append(out, cockpit.LoadedNote{Note: item.note, RelPath: item.rel})
	}
	return out
}

// cockpitParseErrors bridges diagnostics frontmatter parse errors onto the
// exported cockpit parse-error type.
func cockpitParseErrors(errs []diagnosticFileError) []cockpit.ParseError {
	out := make([]cockpit.ParseError, 0, len(errs))
	for _, item := range errs {
		out = append(out, cockpit.ParseError{RelPath: item.Path, Err: item.Error})
	}
	return out
}

func (s *Server) cockpitStore() *cockpit.StateStore {
	if s.cockpitState == nil {
		s.cockpitState = cockpit.NewStateStore(s.vault.Root)
	}
	return s.cockpitState
}

func cockpitTaskByID(queue cockpit.Queue, id string) (cockpit.Task, bool) {
	for _, task := range queue.Tasks {
		if task.ID == id {
			return task, true
		}
	}
	return cockpit.Task{}, false
}

func cockpitSnoozeMessage(option string) string {
	if option == cockpit.SnoozeNextWeek {
		return "Snoozed task for a week."
	}
	return "Snoozed task until tomorrow."
}

func fixPreviewMessage(plan cockpit.FixPlan) string {
	if plan.Changed() {
		return "Preview: fix ready to apply."
	}
	return "Preview: fix would make no changes."
}

func fixApplyMessage(outcome cockpit.FixOutcome) string {
	changed := len(outcome.Changed)
	created := len(outcome.Created)
	switch {
	case changed == 0 && created == 0:
		return "Fix made no changes."
	case created > 0 && changed == 0:
		return "Created " + noteCountLabel(created) + "."
	case created == 0:
		return "Updated " + noteCountLabel(changed) + "."
	default:
		return "Updated " + noteCountLabel(changed) + " and created " + noteCountLabel(created) + "."
	}
}

// cockpitFixError maps a planner/apply error onto a management error. A task
// that does not offer the requested action is a client mistake (bad request);
// everything else is surfaced verbatim if it already carries a noteops code, or
// wrapped as internal otherwise.
func cockpitFixError(err error) error {
	if err == nil {
		return nil
	}
	if noteops.CodeOf(err) != noteops.CodeInternal {
		return err
	}
	msg := err.Error()
	if strings.Contains(msg, "does not offer action") || strings.Contains(msg, "no fix planner") {
		return noteops.Errorf(noteops.CodeInvalidRequest, "%s", msg)
	}
	return noteops.WrapError(noteops.CodeInternal, "apply fix", err)
}
