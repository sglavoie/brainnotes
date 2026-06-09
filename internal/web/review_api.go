package web

import (
	"net/http"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

const reviewStateWarningCode = "review_state_unavailable"

type reviewAPIResponse struct {
	OK       bool               `json:"ok"`
	Message  string             `json:"message,omitempty"`
	Items    []reviewItem       `json:"items"`
	Counts   reviewCounts       `json:"counts"`
	Warnings []reviewAPIWarning `json:"warnings,omitempty"`
}

type reviewAPIWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (s *Server) handleReviewQueue(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/review" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed, noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return
	}

	queue, warnings, err := s.reviewQueueWithStateWarnings()
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "build review queue", err))
		return
	}
	writeManagementJSON(w, http.StatusOK, reviewAPIResponse{
		OK:       true,
		Items:    queue.Items,
		Counts:   queue.Counts,
		Warnings: warnings,
	})
}

func (s *Server) handleReviewItem(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/review/item" {
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
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "review item id is required"))
		return
	}
	action := reviewStateAction(strings.TrimSpace(r.Form.Get("action")))
	if action == "" {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "review item action is required"))
		return
	}

	rawQueue, err := s.unfilteredReviewQueue()
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "build review queue", err))
		return
	}
	item, ok := reviewItemByID(rawQueue, id)
	if !ok {
		writeManagementError(w, noteops.Errorf(noteops.CodeNotFound, "review item not found or stale"))
		return
	}

	store := s.reviewStore()
	message := ""
	switch action {
	case reviewStateActionDone:
		if err := store.markDone(item); err != nil {
			writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "update review state", err))
			return
		}
		message = "Marked review item done."
	case reviewStateActionSnooze:
		snooze := strings.TrimSpace(r.Form.Get("snooze"))
		if snooze == "" {
			snooze = reviewSnoozeTomorrow
		}
		if snooze != reviewSnoozeTomorrow && snooze != reviewSnoozeNextWeek {
			writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "unsupported review snooze %q", snooze))
			return
		}
		if err := store.snooze(item, snooze); err != nil {
			writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "update review state", err))
			return
		}
		message = reviewSnoozeMessage(snooze)
	case reviewStateActionClear:
		if err := store.clear(item); err != nil {
			writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "update review state", err))
			return
		}
		message = "Cleared review item state."
	default:
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "unsupported review action %q", action))
		return
	}

	queue, err := buildReviewQueueWithState(s.vault, s.cfg, store)
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "build review queue", err))
		return
	}
	writeManagementJSON(w, http.StatusOK, reviewAPIResponse{
		OK:      true,
		Message: message,
		Items:   queue.Items,
		Counts:  queue.Counts,
	})
}

func (s *Server) reviewQueueWithStateWarnings() (reviewQueue, []reviewAPIWarning, error) {
	queue, err := s.unfilteredReviewQueue()
	if err != nil {
		return reviewQueue{}, nil, err
	}
	filtered, err := s.reviewStore().filterQueue(queue)
	if err == nil {
		return filtered, nil, nil
	}
	return queue, []reviewAPIWarning{{
		Code:    reviewStateWarningCode,
		Message: "Review state could not be loaded; showing all generated review items.",
	}}, nil
}

func (s *Server) unfilteredReviewQueue() (reviewQueue, error) {
	return buildReviewQueueWithState(s.vault, s.cfg, nil)
}

func (s *Server) reviewStore() *reviewStateStore {
	if s.reviewState == nil {
		s.reviewState = newReviewStateStore(s.vault.Root)
	}
	return s.reviewState
}

func reviewItemByID(queue reviewQueue, id string) (reviewItem, bool) {
	for _, item := range queue.Items {
		if item.ID == id {
			return item, true
		}
	}
	return reviewItem{}, false
}

func reviewSnoozeMessage(snooze string) string {
	switch snooze {
	case reviewSnoozeNextWeek:
		return "Snoozed review item for a week."
	default:
		return "Snoozed review item until tomorrow."
	}
}
