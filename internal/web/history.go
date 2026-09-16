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
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
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

	// Either configuration file can itself be restored by undo — `bn config
	// migrate` journals both, and undoing it can remove an overlay that did not
	// exist before — so history and undo both re-read the pair before answering;
	// otherwise the UI would report a configuration generation that no longer
	// matches disk.
	if err := s.reloadConfig(); err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "config: reload failed", err))
		return
	}

	// An unreadable journal root is still an error — the history is unknown, and
	// answering "no entries" would be a lie the UI cannot see through. An
	// individual unreadable entry is reported alongside the entries that are fine,
	// so one corrupt manifest costs one undo record instead of the whole panel.
	entries, warnings, err := journal.ListEntries(noteops.JournalRoot(s.vault.Root))
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeJournal, "list history", err))
		return
	}
	if len(entries) > historyEntryLimit {
		entries = entries[:historyEntryLimit]
	}
	warningCount := len(warnings)
	if len(warnings) > historyEntryLimit {
		warnings = warnings[:historyEntryLimit]
	}

	out := make([]historyEntryResponse, 0, len(entries))
	for _, entry := range entries {
		out = append(out, historyEntryForJournalEntry(entry))
	}
	writeManagementJSON(w, http.StatusOK, historyResponse{
		OK:           true,
		Entries:      out,
		Warnings:     historyWarningsForJournalWarnings(warnings),
		WarningCount: warningCount,
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

	plan, err := noteops.Undo(s.vault.Root, entryID, noteops.UndoOptions{
		Force:   force,
		Command: "bn web undo",
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
		// Forwarded rather than reclassified: a location refusal is a 403, a
		// safety refusal a 500 that says nothing was written, and a snapshot
		// failure a 500 that says the files are already restored. Wrapping all
		// three as "undo failed" would erase exactly that distinction.
		writeManagementError(w, undoRestoreError(err))
		return
	}

	s.state.invalidate()
	if err := s.reloadConfig(); err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "config: reload failed", err))
		return
	}
	writeManagementJSON(w, http.StatusOK, undoResponseForPlan(plan))
}

// undoRestoreError keeps an already-classified failure classified and gives an
// unclassified one the journal class it used to get.
func undoRestoreError(err error) error {
	var opErr *noteops.Error
	if errors.As(err, &opErr) || noteops.IsSnapshotFailure(err) || vault.IsForbiddenMutation(err) {
		return err
	}
	return noteops.WrapError(noteops.CodeJournal, "undo failed", err)
}

type historyResponse struct {
	OK      bool                   `json:"ok"`
	Entries []historyEntryResponse `json:"entries"`
	// Warnings names the entries that could not be read, capped like Entries.
	Warnings []historyWarningResponse `json:"warnings,omitempty"`
	// WarningCount is the total before that cap, so a client is never told the
	// damage is smaller than it is.
	WarningCount int `json:"warning_count,omitempty"`
}

type historyWarningResponse struct {
	ID      string `json:"id"`
	Message string `json:"message"`
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

func historyWarningsForJournalWarnings(warnings []journal.Warning) []historyWarningResponse {
	if len(warnings) == 0 {
		return nil
	}
	out := make([]historyWarningResponse, 0, len(warnings))
	for _, warning := range warnings {
		out = append(out, historyWarningResponse{ID: warning.ID, Message: warning.Error()})
	}
	return out
}

// latestHistoryEntryID resolves the newest *readable* entry. Undo of a
// specifically named entry never comes through here, so a request to undo a
// corrupt entry stays a direct error; only the "undo the last thing" shorthand
// skips past one.
func latestHistoryEntryID(journalRoot string) (string, error) {
	entries, _, err := journal.ListEntries(journalRoot)
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
