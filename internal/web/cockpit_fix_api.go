package web

import (
	"net/http"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
)

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
	queue, fixer, err := s.cockpitFixContext()
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "build cockpit queue", err))
		return
	}
	task, ok := cockpitTaskByID(queue, id)
	if !ok {
		writeManagementError(w, noteops.Errorf(noteops.CodeNotFound, "cockpit task not found or stale"))
		return
	}

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
	operationID, err := operationIDFromForm(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	task, fixer, err := s.redeemFixPreview(r.Form.Get("preview_id"), id, action)
	if err != nil {
		writeManagementError(w, err)
		return
	}

	outcome, err := fixer.WithJournalOptions(journalOptionsForOperation(operationID)...).Apply(task, action)
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

// redeemFixPreview redeems the one-use receipt before anything touches the
// vault, so an expired or replayed apply fails closed with preview_required,
// then finds the previewed task again in a freshly refreshed snapshot.
//
// Because a task id embeds the on-disk hash of every affected note, any change
// to the underlying files yields a different id: the previewed task simply
// vanishes. A valid receipt whose task is gone means the world moved on since
// the preview — stale. The Fixer is bound to that same snapshot so the apply
// diffs against exactly the load the id was validated against.
func (s *Server) redeemFixPreview(previewID, id string, action cockpit.ActionKind) (cockpit.Task, *cockpit.Fixer, error) {
	record, err := s.fixPreviews.consume(previewID)
	if err != nil {
		return cockpit.Task{}, nil, err
	}
	if record.TaskID != id || record.Action != action {
		return cockpit.Task{}, nil, noteops.Errorf(noteops.CodePreviewRequired, "fix preview does not match the request")
	}
	queue, fixer, err := s.cockpitFixContext()
	if err != nil {
		return cockpit.Task{}, nil, noteops.WrapError(noteops.CodeInternal, "build cockpit queue", err)
	}
	task, ok := cockpitTaskByID(queue, id)
	if !ok || record.Signature != task.Signature {
		return cockpit.Task{}, nil, noteops.Errorf(noteops.CodePreviewStale, "fix preview is stale")
	}
	return task, fixer, nil
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

// cockpitQueue builds the raw, unfiltered generated queue from a freshly loaded
// bounded snapshot. The content-addressed fix preview/apply and item triage
// flows call it, so it forces a refresh (refreshSnapshot) to reflect a
// concurrent on-disk edit immediately — a task id embeds the hash of every note
// it touches, and receipt/stale validation depends on that id tracking the
// current bytes. The refresh is bounded and honors the process-lifetime
// quarantine, so a stalled note is never re-read; it just replaces the older
// unbounded cockpit.BuildQueue walk.
func (s *Server) cockpitQueue() (cockpit.Queue, error) {
	snapshot, err := s.state.refreshSnapshot()
	if err != nil {
		return cockpit.Queue{}, err
	}
	return s.cockpitQueueFrom(diagnosticsDataFromSnapshot(snapshot, s.vault.Root)), nil
}

// cockpitQueueFiltered builds the generated queue from the shared resilient
// snapshot and hides tasks the workflow state suppresses. It reads the cached
// currentSnapshot rather than forcing a refresh: the read-only cockpit surfaces
// (GET /api/cockpit, daily-review context/agent, the item-mutation response)
// tolerate up-to-one-TTL latency, and reusing the cache means a stalled note is
// only ever waited on once per server process. If the state file cannot be
// loaded it degrades gracefully: the unfiltered queue is returned with a
// warning so triage never fails hard.
func (s *Server) cockpitQueueFiltered() (cockpit.Queue, error) {
	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		return cockpit.Queue{}, err
	}
	return s.cockpitQueueFilteredFrom(diagnosticsDataFromSnapshot(snapshot, s.vault.Root)), nil
}

// cockpitQueueFrom builds the raw generated queue from an already-loaded
// diagnostics snapshot, so the shared vault walk feeds the cockpit without a
// second walk. The snapshot's loaded notes, graph, and parse errors describe
// one coherent load, exactly as BuildQueueFromSnapshot requires.
func (s *Server) cockpitQueueFrom(data diagnosticsData) cockpit.Queue {
	cfg := s.config()
	return cockpit.BuildQueueFromSnapshot(cockpitSnapshotFromData(data), cfg, s.vault.Root)
}

// cockpitSnapshotFromData bridges the web diagnostics snapshot onto the exported
// cockpit.Snapshot the queue builder and snapshot-backed Fixer both consume.
func cockpitSnapshotFromData(data diagnosticsData) cockpit.Snapshot {
	return cockpit.Snapshot{
		Notes:             cockpitLoadedNotes(data.loaded),
		Graph:             data.graph,
		FrontmatterErrors: cockpitParseErrors(data.frontmatterErrors),
	}
}

// cockpitFixContext forces one fresh bounded load and derives from it both the
// raw generated queue (for the task lookup plus receipt/stale validation) and a
// Fixer bound to that same snapshot. Sharing one snapshot keeps the previewed
// task and the fix that mutates it perfectly consistent, and — crucially — the
// snapshot-backed Fixer never performs the unbounded full-vault walk NewFixer's
// default reload would, so a quarantined note stays unread and the fix endpoints
// remain responsive even while one note read is stalled.
func (s *Server) cockpitFixContext() (cockpit.Queue, *cockpit.Fixer, error) {
	cfg := s.config()
	snapshot, err := s.state.refreshSnapshot()
	if err != nil {
		return cockpit.Queue{}, nil, err
	}
	cockpitSnapshot := cockpitSnapshotFromData(diagnosticsDataFromSnapshot(snapshot, s.vault.Root))
	queue := cockpit.BuildQueueFromSnapshot(cockpitSnapshot, cfg, s.vault.Root)
	fixer := cockpit.NewFixerFromSnapshot(s.vault, cfg, cockpitFixCommand, cockpitSnapshot)
	return queue, fixer, nil
}

// cockpitQueueFilteredFrom builds the filtered queue from an already-loaded
// diagnostics snapshot, so a single vault walk feeds both the homepage
// diagnostics summary and its fixes panel. It shares cockpitQueueFrom with the
// endpoint helpers so every cockpit surface derives from the same snapshot
// bridge.
func (s *Server) cockpitQueueFilteredFrom(data diagnosticsData) cockpit.Queue {
	return s.filterCockpitQueue(s.cockpitQueueFrom(data))
}

// filterCockpitQueue hides workflow-suppressed tasks. An unreadable state file
// degrades to the unfiltered queue, while a failed stale-record cleanup keeps
// the valid filtered result. Retryable contention with the Git safety watcher is
// silent because a later request or watcher pass will finish the cleanup.
func (s *Server) filterCockpitQueue(queue cockpit.Queue) cockpit.Queue {
	filtered, ferr := s.cockpitStore().FilterQueue(queue)
	if ferr == nil {
		return filtered
	}
	if cockpit.IsStateCleanupError(ferr) {
		if !vaultgit.IsBusy(ferr) {
			filtered.Warnings = append(filtered.Warnings, cockpit.Warning{Message: cockpitStateCleanupWarning})
		}
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
