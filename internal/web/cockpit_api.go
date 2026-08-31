package web

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
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
	cfg := s.config()
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
	mutationWarning := ""
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
	case cockpitItemActionExemptDeadEnd:
		if task.Kind != cockpit.RuleDeadEndNote || len(task.Affected) != 1 {
			writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "dead-end exemption is unavailable for this task"))
			return
		}
		result, err := noteops.SetFrontmatter(
			s.vault.Root,
			task.Affected[0].RelPath,
			[]noteops.FrontmatterMutation{{Key: "dead_end", Op: noteops.FrontmatterSet, Value: false}},
			&cfg.Frontmatter,
			noteops.WithFrontmatterCommand(cockpitDeadEndExemptionCommand),
		)
		if err != nil {
			writeManagementError(w, err)
			return
		}
		if !result.Changed {
			writeManagementError(w, noteops.Errorf(noteops.CodeConflict, "dead-end exemption did not change the note"))
			return
		}
		s.state.invalidate()
		message = "Marked " + task.Affected[0].Title + " as an intentional dead end."
	case cockpitItemActionCleanLongTail:
		property, ok := cockpitLongTailProperty(task)
		if !ok {
			writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "long-tail cleanup is unavailable for this task"))
			return
		}
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
		result, err := noteops.BulkUnsetFrontmatter(
			s.vault.Root,
			sources,
			[]string{property},
			&cfg.Frontmatter,
			noteops.WithBulkCommand(cockpitLongTailCleanupCommand),
		)
		if err != nil {
			writeManagementError(w, err)
			return
		}
		if result.Changed == 0 {
			writeManagementError(w, noteops.Errorf(noteops.CodeConflict, "long-tail cleanup did not change any affected note"))
			return
		}
		s.state.invalidate()
		message = fmt.Sprintf("Removed property %q from %d affected %s.", property, result.Changed, pluralizeNote(result.Changed))
		if len(result.Failures) > 0 {
			mutationWarning = fmt.Sprintf("Long-tail cleanup failed for %d affected %s; the successful removals remain undoable as one journal entry.", len(result.Failures), pluralizeNote(len(result.Failures)))
		}
	default:
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "unsupported cockpit action %q", action))
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
		queue = cockpit.LimitDaily(queue, cfg.Cockpit.DailyLimit)
	}
	if mutationWarning != "" {
		queue.Warnings = append(queue.Warnings, cockpit.Warning{Message: mutationWarning})
	}
	writeManagementJSON(w, http.StatusOK, cockpitAPIResponse{OK: true, Message: message, Queue: queue})
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
