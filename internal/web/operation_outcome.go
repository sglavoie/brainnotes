package web

import (
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

const operationOutcomeRoutePath = "/api/operations/outcome"

// validOperationID accepts only the random 128-bit token sent with one browser
// write. It is never a path or a search expression.
func validOperationID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil && id == strings.ToLower(id)
}

func operationIDFromForm(r *http.Request) (string, error) {
	id := strings.TrimSpace(r.Form.Get("operation_id"))
	if id != "" && !validOperationID(id) {
		return "", noteops.Errorf(noteops.CodeInvalidRequest, "invalid operation ID")
	}
	return id, nil
}

func journalOptionsForOperation(id string) []journal.RecorderOption {
	if id == "" {
		return nil
	}
	return []journal.RecorderOption{journal.WithOperationID(id)}
}

// handleOperationOutcome answers whether the exact browser attempt has a
// finalized journal entry. A missing entry is deliberately called unconfirmed:
// an in-flight write or a recovery problem cannot be proved absent by a read.
func (s *Server) handleOperationOutcome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != operationOutcomeRoutePath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed, noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return
	}
	id := r.URL.Query().Get("id")
	if !validOperationID(id) {
		writeManagementError(w, noteops.Errorf(noteops.CodeInvalidRequest, "invalid operation ID"))
		return
	}
	entries, warnings, err := journal.ListEntries(noteops.JournalRoot(s.vault.Root))
	if err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeJournal, "check operation outcome", err))
		return
	}
	for _, entry := range entries {
		if entry.OperationID == id {
			writeManagementJSON(w, http.StatusOK, struct {
				OK        bool   `json:"ok"`
				Status    string `json:"status"`
				JournalID string `json:"journal_id"`
			}{true, "confirmed", entry.ID})
			return
		}
	}
	writeManagementJSON(w, http.StatusOK, struct {
		OK       bool   `json:"ok"`
		Status   string `json:"status"`
		Warnings int    `json:"warnings"`
	}{true, "unconfirmed", len(warnings)})
}
