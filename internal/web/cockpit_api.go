package web

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/sglavoie/brainnotes/internal/cockpit"
	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// cockpitFixCommand labels the undo-journal entries cockpit fixes create when
// applied through the web APIs.
const cockpitFixCommand = "bn cockpit fix"

// cockpitDeadEndExemptionCommand labels the undo-journal entry created by the
// one-click dead-end exemption action in the Daily Fixes panel.
const cockpitDeadEndExemptionCommand = "bn serve exempt dead end"

// cockpitLongTailCleanupCommand labels the single undo-journal entry covering
// removal of a flagged long-tail property from every affected note.
const cockpitLongTailCleanupCommand = "bn serve clean long tail"

// cockpitStateWarning is surfaced (as a queue warning) when the workflow-state
// file cannot be loaded, so the client still sees every generated task.
const cockpitStateWarning = "Cockpit state could not be loaded; showing all generated tasks."

// cockpitStateCleanupWarning distinguishes a usable loaded state from a failed
// best-effort stale-record cleanup. Filtering remains correct for this response.
const cockpitStateCleanupWarning = "Cockpit state was loaded, but stale task state could not be saved; filtering is current for this page."

// Item-endpoint triage actions. mark_done and snooze mirror the ActionKind
// vocabulary the task advertises; clear is the reverse of both (it returns a
// suppressed task to the active queue). "done" is accepted as an alias for
// mark_done so callers can reuse the review vocabulary.
const (
	cockpitItemActionDone          = "mark_done"
	cockpitItemActionSnooze        = "snooze"
	cockpitItemActionClear         = "clear"
	cockpitItemActionExemptDeadEnd = "exempt_dead_end"
	cockpitItemActionCleanLongTail = "clean_long_tail"
)

// cockpitAPIResponse is the GET /api/cockpit and item-mutation payload. It
// embeds cockpit.Queue so the tasks/counts/warnings schema is identical to
// `bn doctor --json`, and adds the ok/message envelope the web APIs share.
type cockpitAPIResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	cockpit.Queue
}

// dailyReviewContextResponse is the GET /api/daily-review/context payload. It
// embeds the deterministic, redacted cockpit.ReviewContext so the JSON schema is
// identical to `bn doctor --review-context`, and adds the shared ok/message
// envelope. It is read-only and never invokes any AI command.
type dailyReviewContextResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	cockpit.ReviewContext
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
	cfg := s.config()
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
	// view=daily caps the workflow-filtered queue to the daily cockpit slice;
	// any other view (including none) keeps the full queue unchanged.
	if r.URL.Query().Get("view") == "daily" {
		queue = cockpit.LimitDaily(queue, cfg.Cockpit.DailyLimit)
	}
	writeManagementJSON(w, http.StatusOK, cockpitAPIResponse{OK: true, Queue: queue})
}

// handleDailyReviewContext serves the read-only daily-review context: the
// deterministic, redacted snapshot of the workflow-filtered, daily-limited
// queue. It is safe to call without side effects — no note is mutated and no AI
// command is invoked. Session 9 layers the optional agent execution on top of
// this substrate.
func (s *Server) handleDailyReviewContext(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	if r.URL.Path != "/api/daily-review/context" {
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
	// The review context is inherently a daily surface, so it always caps to the
	// daily slice after workflow-state filtering.
	queue = cockpit.LimitDaily(queue, cfg.Cockpit.DailyLimit)
	context := cockpit.BuildReviewContext(queue)
	writeManagementJSON(w, http.StatusOK, dailyReviewContextResponse{OK: true, ReviewContext: context})
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
	id, action, err := parseCockpitItemRequest(r, s.csrfToken)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	outcome, err := s.applyCockpitItemAction(r, id, action)
	if err != nil {
		writeManagementError(w, err)
		return
	}

	queue, err := s.cockpitQueueFiltered()
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "build cockpit queue", err))
		return
	}
	// view=daily caps the workflow-filtered response to the daily cockpit slice,
	// mirroring handleCockpitQueue so the daily panel cannot inflate past
	// cockpit.daily_limit after a triage action; any other view (including none)
	// keeps the full filtered queue unchanged.
	if r.URL.Query().Get("view") == "daily" {
		queue = cockpit.LimitDaily(queue, s.config().Cockpit.DailyLimit)
	}
	if outcome.warning != "" {
		queue.Warnings = append(queue.Warnings, cockpit.Warning{Message: outcome.warning})
	}
	writeManagementJSON(w, http.StatusOK, cockpitAPIResponse{OK: true, Message: outcome.message, Queue: queue})
}

// cockpitItemOutcome is what a triage action reports back: the sentence the
// panel shows, and a warning folded into the refreshed queue.
type cockpitItemOutcome struct {
	message string
	warning string
}

// cockpitNoteWrite carries what a note-writing triage action is planned
// against: the configuration and its pin, and the snapshot the task was
// generated from, whose note revisions the write must still find on disk.
type cockpitNoteWrite struct {
	cfg      *config.Config
	pin      configPin
	snapshot vaultSnapshot
}

func parseCockpitItemRequest(r *http.Request, csrfToken string) (string, string, error) {
	if err := r.ParseForm(); err != nil {
		return "", "", noteops.WrapError(noteops.CodeInvalidRequest, "invalid request form", err)
	}
	if !csrfTokenMatches(csrfToken, requestCSRFToken(r)) {
		return "", "", noteops.Errorf(noteops.CodeForbidden, "csrf token required")
	}
	id := strings.TrimSpace(r.Form.Get("id"))
	if id == "" {
		return "", "", noteops.Errorf(noteops.CodeInvalidRequest, "cockpit task id is required")
	}
	action := strings.TrimSpace(r.Form.Get("action"))
	if action == "" {
		return "", "", noteops.Errorf(noteops.CodeInvalidRequest, "cockpit task action is required")
	}
	return id, action, nil
}

// cockpitItemWritesNotes reports whether an action edits note frontmatter, and
// so must hold the same configuration guarantees every other property write
// does, rather than only the cockpit's own workflow-state file.
func cockpitItemWritesNotes(action string) bool {
	return action == cockpitItemActionExemptDeadEnd || action == cockpitItemActionCleanLongTail
}

// applyCockpitItemAction resolves the task and carries out one triage action.
//
// A note-writing action reloads and checks the configuration first, the way
// manage.go and pins_api.go do, and plans with the pinned configuration, so a
// definition edited in between is refused at the commit boundary. The task is
// looked up in a queue built from the same snapshot whose revisions those
// writes check: the queue decided which notes to change, so a note edited
// since must not be changed on that decision.
func (s *Server) applyCockpitItemAction(r *http.Request, id, action string) (cockpitItemOutcome, error) {
	if cockpitItemWritesNotes(action) {
		if err := s.ensureConfigWritable(); err != nil {
			return cockpitItemOutcome{}, err
		}
	}
	cfg, _, pin := s.pinnedConfig()
	// Look up the task from the raw generated queue: a done/snoozed task is still
	// generated (only FilterQueue hides it), so it must be resolvable here to be
	// cleared or re-suppressed.
	snapshot, err := s.state.refreshSnapshot()
	if err != nil {
		return cockpitItemOutcome{}, noteops.WrapError(noteops.CodeInternal, "build cockpit queue", err)
	}
	task, ok := cockpitTaskByID(s.cockpitQueueFrom(diagnosticsDataFromSnapshot(snapshot, s.vault.Root)), id)
	if !ok {
		return cockpitItemOutcome{}, noteops.Errorf(noteops.CodeNotFound, "cockpit task not found or stale")
	}

	write := cockpitNoteWrite{cfg: cfg, pin: pin, snapshot: snapshot}
	switch action {
	case cockpitItemActionDone, "done":
		return s.cockpitStateChange(s.cockpitStore().MarkDone(task), "Marked task done.")
	case cockpitItemActionSnooze:
		return s.snoozeCockpitTask(r, task)
	case cockpitItemActionClear:
		return s.cockpitStateChange(s.cockpitStore().Clear(task), "Cleared task state.")
	case cockpitItemActionExemptDeadEnd:
		return s.exemptCockpitDeadEnd(write, task)
	case cockpitItemActionCleanLongTail:
		return s.cleanCockpitLongTail(write, task)
	}
	return cockpitItemOutcome{}, noteops.Errorf(noteops.CodeInvalidRequest, "unsupported cockpit action %q", action)
}

func (s *Server) cockpitStateChange(err error, message string) (cockpitItemOutcome, error) {
	if err != nil {
		return cockpitItemOutcome{}, noteops.WrapError(noteops.CodeInternal, "update cockpit state", err)
	}
	return cockpitItemOutcome{message: message}, nil
}

func (s *Server) snoozeCockpitTask(r *http.Request, task cockpit.Task) (cockpitItemOutcome, error) {
	option := strings.TrimSpace(r.Form.Get("snooze"))
	if option == "" {
		option = cockpit.SnoozeTomorrow
	}
	if option != cockpit.SnoozeTomorrow && option != cockpit.SnoozeNextWeek {
		return cockpitItemOutcome{}, noteops.Errorf(noteops.CodeInvalidRequest, "unsupported snooze %q", option)
	}
	return s.cockpitStateChange(s.cockpitStore().Snooze(task, option), cockpitSnoozeMessage(option))
}

func (s *Server) exemptCockpitDeadEnd(write cockpitNoteWrite, task cockpit.Task) (cockpitItemOutcome, error) {
	if task.Kind != cockpit.RuleDeadEndNote || len(task.Affected) != 1 {
		return cockpitItemOutcome{}, noteops.Errorf(noteops.CodeInvalidRequest, "dead-end exemption is unavailable for this task")
	}
	relPath := task.Affected[0].RelPath
	if err := verifyCockpitAffected(s.vault.Root, write.snapshot, []string{relPath}); err != nil {
		return cockpitItemOutcome{}, err
	}
	result, err := noteops.SetFrontmatter(
		s.vault.Root,
		relPath,
		[]noteops.FrontmatterMutation{{Key: "dead_end", Op: noteops.FrontmatterSet, Value: false}},
		&write.cfg.Frontmatter,
		append(frontmatterConfigPinOptions(write.pin), noteops.WithFrontmatterCommand(cockpitDeadEndExemptionCommand))...,
	)
	if err != nil {
		return cockpitItemOutcome{}, err
	}
	if !result.Changed {
		return cockpitItemOutcome{}, noteops.Errorf(noteops.CodeConflict, "dead-end exemption did not change the note")
	}
	s.state.invalidate()
	return cockpitItemOutcome{message: "Marked " + task.Affected[0].Title + " as an intentional dead end."}, nil
}

func (s *Server) cleanCockpitLongTail(write cockpitNoteWrite, task cockpit.Task) (cockpitItemOutcome, error) {
	property, ok := cockpitLongTailProperty(task)
	if !ok {
		return cockpitItemOutcome{}, noteops.Errorf(noteops.CodeInvalidRequest, "long-tail cleanup is unavailable for this task")
	}
	sources := cockpitAffectedSources(task)
	relPaths := make([]string, 0, len(sources))
	for _, source := range sources {
		relPaths = append(relPaths, source.RelPath)
	}
	if err := verifyCockpitAffected(s.vault.Root, write.snapshot, relPaths); err != nil {
		return cockpitItemOutcome{}, err
	}
	result, err := noteops.BulkUnsetFrontmatter(s.vault.Root, sources, []string{property}, &write.cfg.Frontmatter,
		append(bulkConfigPinOptions(write.pin), noteops.WithBulkCommand(cockpitLongTailCleanupCommand))...)
	if err != nil {
		return cockpitItemOutcome{}, err
	}
	if result.Changed == 0 {
		return cockpitItemOutcome{}, noteops.Errorf(noteops.CodeConflict, "long-tail cleanup did not change any affected note")
	}
	s.state.invalidate()
	outcome := cockpitItemOutcome{
		message: fmt.Sprintf("Removed property %q from %d affected %s.", property, result.Changed, pluralizeNote(result.Changed)),
	}
	if len(result.Failures) > 0 {
		outcome.warning = fmt.Sprintf("Long-tail cleanup failed for %d affected %s; the successful removals remain undoable as one journal entry.", len(result.Failures), pluralizeNote(len(result.Failures)))
	}
	return outcome, nil
}

func cockpitAffectedSources(task cockpit.Task) []noteops.BulkSource {
	sources := make([]noteops.BulkSource, 0, len(task.Affected))
	seen := make(map[string]bool, len(task.Affected))
	for _, affected := range task.Affected {
		relPath := strings.TrimSpace(affected.RelPath)
		if relPath == "" || seen[relPath] {
			continue
		}
		seen[relPath] = true
		sources = append(sources, noteops.BulkSource{
			Key:     managementRouteKeyForRelPath(relPath),
			RelPath: relPath,
		})
	}
	return sources
}

// verifyCockpitAffected refuses a triage write when an affected note no longer
// holds the revision the task was generated from, the way /api/properties/value
// refuses a stale echoed hash. These notes are the ones being written, so the
// revision cannot be a FileTxn requirement (Require refuses a planned path);
// each write's own compare-and-swap then covers the window after this check.
// A note the snapshot did not load, or loaded without a hash, is left to that
// compare-and-swap alone, as pinnedListRequirements leaves it.
func verifyCockpitAffected(root string, snapshot vaultSnapshot, relPaths []string) error {
	wanted := make(map[string]bool, len(relPaths))
	for _, relPath := range relPaths {
		wanted[relPath] = true
	}
	for _, note := range snapshot.notes {
		hash := note.SourceHash()
		if !wanted[noteVaultPath(root, note)] || hash == "" {
			continue
		}
		if err := vault.VerifyState(note.Path, vault.ExpectHash(hash)); err != nil {
			return noteops.WrapError(noteops.CodeConflict,
				"cockpit: an affected note changed since the task was generated; refresh and retry", err)
		}
	}
	return nil
}

func cockpitLongTailProperty(task cockpit.Task) (string, bool) {
	if task.Kind != cockpit.RuleLongTailValue || len(task.Affected) == 0 {
		return "", false
	}
	property := ""
	for _, affected := range task.Affected {
		location := strings.TrimSpace(affected.Location)
		candidate := strings.TrimSpace(strings.TrimPrefix(location, "frontmatter."))
		if candidate == "" || candidate == location {
			return "", false
		}
		if property == "" {
			property = candidate
			continue
		}
		if candidate != property {
			return "", false
		}
	}
	return property, property != ""
}

func pluralizeNote(count int) string {
	if count == 1 {
		return "note"
	}
	return "notes"
}
