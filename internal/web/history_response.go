package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

type historyResponse struct {
	OK      bool                   `json:"ok"`
	Entries []historyEntryResponse `json:"entries"`
	// Total is how many entries matched the filters, before the page was taken,
	// so a client can say which slice of what it is showing.
	Total   int  `json:"total"`
	Offset  int  `json:"offset"`
	Limit   int  `json:"limit"`
	HasMore bool `json:"has_more"`
	// Origins and Classes are the filter vocabulary with its labels. They are
	// answered by the server rather than compiled into the browser so the two can
	// never disagree about what an origin is called or which ones exist.
	Origins []historyOptionResponse `json:"origins,omitempty"`
	Classes []historyOptionResponse `json:"classes,omitempty"`
	// Warnings names the entries that could not be read, capped at
	// historyWarningLimit.
	//
	// They are neither filtered nor paginated, and that is the only honest
	// treatment available: an entry that could not be read has no command and no
	// file list, so there is no way to tell whether it would have matched. Showing
	// it in every page — rather than only on the page its position falls in, or
	// only when the filters happen to be empty — is what stops a narrowed view
	// from looking complete when part of the journal is unreadable.
	Warnings []historyWarningResponse `json:"warnings,omitempty"`
	// WarningCount is the total before that cap, so a client is never told the
	// damage is smaller than it is.
	WarningCount int `json:"warning_count,omitempty"`
}

// historyOptionResponse is one filter value and the name to show for it.
type historyOptionResponse struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

func historyOriginOptions() []historyOptionResponse {
	out := make([]historyOptionResponse, 0, len(historyOrigins))
	for _, origin := range historyOrigins {
		out = append(out, historyOptionResponse{Value: string(origin), Label: origin.label()})
	}
	return out
}

func historyPathClassOptions() []historyOptionResponse {
	out := make([]historyOptionResponse, 0, len(historyPathClasses))
	for _, class := range historyPathClasses {
		out = append(out, historyOptionResponse{Value: string(class), Label: class.label()})
	}
	return out
}

type historyWarningResponse struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

type historyEntryResponse struct {
	Summary   string `json:"summary"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Command   string `json:"command"`
	// Origin and OriginLabel are where the change came from, read off Command by
	// historyOriginForCommand. The raw command stays alongside them: the origin is
	// a classification, and a reader who does not believe it can check.
	Origin      string `json:"origin"`
	OriginLabel string `json:"origin_label"`
	FileCount   int    `json:"file_count"`
	// Classes counts the recorded paths by kind, so a row says "3 notes, 2
	// attachments" without the reader opening it.
	Classes []historyClassCountResponse `json:"classes,omitempty"`
	Files   []historyFileResponse       `json:"files,omitempty"`
}

type historyClassCountResponse struct {
	Class string `json:"class"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

type historyFileResponse struct {
	Path   string `json:"path"`
	Before string `json:"before"`
	After  string `json:"after"`
	Class  string `json:"class"`
	// Recorded says this path enters the content history, so restoring it becomes
	// a commit. Travels says the path is portable, so that commit reaches the
	// other replica. See historyFileFacts for why both are reported.
	Recorded bool `json:"recorded"`
	Travels  bool `json:"travels"`
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

func historyEntryForJournalEntry(entry journal.Entry, classifier historyPathClassifier) historyEntryResponse {
	files := make([]historyFileResponse, 0, len(entry.Files))
	for _, file := range entry.Files {
		facts := classifier.facts(file.Path)
		files = append(files, historyFileResponse{
			Path:     file.Path,
			Before:   file.Before.Label(),
			After:    file.After.Label(),
			Class:    string(facts.Class),
			Recorded: facts.Recorded,
			Travels:  facts.Travels,
		})
	}
	facts := classifier.entryFacts(entry)
	return historyEntryResponse{
		Summary:     historyActionSummary(entry, classifier),
		ID:          entry.ID,
		Timestamp:   entry.Timestamp.UTC().Format(time.RFC3339Nano),
		Command:     entry.Command,
		Origin:      string(facts.Origin),
		OriginLabel: facts.Origin.label(),
		FileCount:   len(entry.Files),
		Classes:     historyClassCounts(facts.Classes),
		Files:       files,
	}
}

// historyClassCounts renders the per-kind tally in the fixed order
// historyPathClasses gives, so two entries with the same mix describe it the
// same way and a diff of two responses is readable.
func historyClassCounts(counts map[historyPathClass]int) []historyClassCountResponse {
	out := make([]historyClassCountResponse, 0, len(counts))
	for _, class := range historyPathClasses {
		count := counts[class]
		if count == 0 {
			continue
		}
		out = append(out, historyClassCountResponse{
			Class: string(class),
			Label: class.label(),
			Count: count,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
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

// undoOptionsFor turns a parsed request into the restore the engine performs.
//
// A separate function because Require is the whole point of the receipt and its
// journey is otherwise untestable: checkHistoryRestorePreview's own comparisons
// refuse every drift a test can arrange *before* the restore starts, so nothing
// observable changes if the pins stop being handed on — the window they close is
// between this call and the transaction's locks. Naming the translation makes it
// something a test can assert about directly.
func undoOptionsFor(req undoRequest) noteops.UndoOptions {
	return noteops.UndoOptions{
		Force:          req.Force,
		Command:        "bn web undo",
		Require:        req.Require,
		JournalOptions: journalOptionsForOperation(req.OperationID),
	}
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
		Message:   undoSuccessMessage(plan),
		EntryID:   plan.EntryID,
		Command:   plan.Command,
		Timestamp: plan.Timestamp.UTC().Format(time.RFC3339Nano),
		Changes:   changes,
	}
}

// undoSuccessMessage says what the restore did, and says so on the same line
// when the transaction left a temp file behind.
//
// A leftover temp file is not a failure — the paths are restored, journaled and
// snapshotted — so it must not be rendered as one. It is also the only mention
// that file will ever get: nothing revisits it, and the person reading this is
// the only one who knows the restore happened at all.
func undoSuccessMessage(plan *journal.RestorePlan) string {
	message := "Undo restored " + pathCountLabel(len(plan.Changes)) + "."
	if plan.Cleanup != nil {
		message += " A temporary file was left behind and can be removed: " + plan.Cleanup.Error()
	}
	return message
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
