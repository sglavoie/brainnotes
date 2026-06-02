package web

import (
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

const historyEntryLimit = 20

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/history" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed, noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return
	}

	entries, err := journal.ListEntries(noteops.JournalRoot(s.vault.Root))
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeJournal, "list history", err))
		return
	}
	if len(entries) > historyEntryLimit {
		entries = entries[:historyEntryLimit]
	}

	out := make([]historyEntryResponse, 0, len(entries))
	for _, entry := range entries {
		out = append(out, historyEntryForJournalEntry(entry))
	}
	writeManagementJSON(w, http.StatusOK, historyResponse{
		OK:      true,
		Entries: out,
	})
}

func (s *Server) handleUndo(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/undo" {
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

	entryID, _ := managementFormValue(r, "id", "entry_id")
	entryID = strings.TrimSpace(entryID)
	force := managementBoolFormValue(r, "force")
	confirmForce := managementBoolFormValue(r, "confirm_force", "force_confirmed") ||
		strings.EqualFold(strings.TrimSpace(r.Form.Get("confirm")), "force")
	if force && !confirmForce {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "force undo requires confirmation"))
		return
	}

	journalRoot := noteops.JournalRoot(s.vault.Root)
	if entryID == "" {
		id, err := latestHistoryEntryID(journalRoot)
		if err != nil {
			writeManagementError(w, err)
			return
		}
		entryID = id
	}

	plan, err := journal.Restore(s.vault.Root, journalRoot, entryID, journal.RestoreOptions{
		Force: force,
	})
	if err != nil {
		var conflictErr *journal.RestoreConflictError
		if errors.As(err, &conflictErr) {
			writeManagementJSON(w, http.StatusConflict, undoConflictResponseForError(conflictErr))
			return
		}
		if errors.Is(err, os.ErrNotExist) {
			writeManagementError(w, noteops.Errorf(noteops.CodeNotFound, "undo entry not found"))
			return
		}
		writeManagementError(w, noteops.WrapError(noteops.CodeJournal, "undo failed", err))
		return
	}

	s.state.invalidate()
	writeManagementJSON(w, http.StatusOK, undoResponseForPlan(plan))
}

type historyResponse struct {
	OK      bool                   `json:"ok"`
	Entries []historyEntryResponse `json:"entries"`
}

type historyEntryResponse struct {
	ID        string                `json:"id"`
	Timestamp string                `json:"timestamp"`
	Command   string                `json:"command"`
	FileCount int                   `json:"file_count"`
	Files     []historyFileResponse `json:"files,omitempty"`
}

type historyFileResponse struct {
	Path   string `json:"path"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type undoResponse struct {
	OK        bool                 `json:"ok"`
	Code      string               `json:"code,omitempty"`
	Message   string               `json:"message"`
	EntryID   string               `json:"entry_id,omitempty"`
	Command   string               `json:"command,omitempty"`
	Timestamp string               `json:"timestamp,omitempty"`
	Changes   []undoChangeResponse `json:"changes,omitempty"`
}

type undoChangeResponse struct {
	Path          string `json:"path"`
	CurrentState  string `json:"current_state"`
	RestoredState string `json:"restored_state"`
}

type undoConflictResultResponse struct {
	OK            bool                       `json:"ok"`
	Code          string                     `json:"code"`
	Message       string                     `json:"message"`
	EntryID       string                     `json:"entry_id"`
	RequiresForce bool                       `json:"requires_force"`
	Conflicts     []undoConflictItemResponse `json:"conflicts"`
}

type undoConflictItemResponse struct {
	Path          string `json:"path"`
	ExpectedState string `json:"expected_state"`
	CurrentState  string `json:"current_state"`
}

func historyEntryForJournalEntry(entry journal.Entry) historyEntryResponse {
	files := make([]historyFileResponse, 0, len(entry.Files))
	for _, file := range entry.Files {
		files = append(files, historyFileResponse{
			Path:   file.Path,
			Before: file.Before.Label(),
			After:  file.After.Label(),
		})
	}
	return historyEntryResponse{
		ID:        entry.ID,
		Timestamp: entry.Timestamp.UTC().Format(time.RFC3339Nano),
		Command:   entry.Command,
		FileCount: len(entry.Files),
		Files:     files,
	}
}

func latestHistoryEntryID(journalRoot string) (string, error) {
	entries, err := journal.ListEntries(journalRoot)
	if err != nil {
		return "", noteops.WrapError(noteops.CodeJournal, "list history", err)
	}
	if len(entries) == 0 {
		return "", noteops.Errorf(noteops.CodeNotFound, "no journal entries found")
	}
	return entries[0].ID, nil
}

func undoResponseForPlan(plan *journal.RestorePlan) undoResponse {
	if plan == nil {
		return undoResponse{
			OK:      false,
			Code:    string(noteops.CodeInternal),
			Message: "Undo failed.",
		}
	}
	changes := make([]undoChangeResponse, 0, len(plan.Changes))
	for _, change := range plan.Changes {
		changes = append(changes, undoChangeResponse{
			Path:          change.Path,
			CurrentState:  change.CurrentState,
			RestoredState: change.RestoredState,
		})
	}
	return undoResponse{
		OK:        true,
		Message:   "Undo restored " + pathCountLabel(len(plan.Changes)) + ".",
		EntryID:   plan.EntryID,
		Command:   plan.Command,
		Timestamp: plan.Timestamp.UTC().Format(time.RFC3339Nano),
		Changes:   changes,
	}
}

func pathCountLabel(count int) string {
	if count == 1 {
		return "1 path"
	}
	return strconv.Itoa(count) + " paths"
}

func undoConflictResponseForError(err *journal.RestoreConflictError) undoConflictResultResponse {
	entryID := ""
	conflicts := []undoConflictItemResponse{}
	if err != nil {
		entryID = err.EntryID
		conflicts = make([]undoConflictItemResponse, 0, len(err.Conflicts))
		for _, conflict := range err.Conflicts {
			conflicts = append(conflicts, undoConflictItemResponse{
				Path:          conflict.Path,
				ExpectedState: conflict.Expected,
				CurrentState:  conflict.Actual,
			})
		}
	}
	return undoConflictResultResponse{
		OK:            false,
		Code:          string(noteops.CodeConflict),
		Message:       "Undo has conflicts.",
		EntryID:       entryID,
		RequiresForce: true,
		Conflicts:     conflicts,
	}
}

func managementBoolFormValue(r *http.Request, names ...string) bool {
	for _, name := range names {
		for _, value := range r.Form[name] {
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "1", "true", "yes", "on":
				return true
			}
		}
	}
	return false
}
