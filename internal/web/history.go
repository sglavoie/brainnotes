package web

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

const (
	// historyEntryLimit is the page a client gets when it asks for no particular
	// size. It is what the single history panel has always shown, kept as the
	// default so a client that sends no query reads exactly what it read before
	// this route learned to paginate.
	historyEntryLimit = 20
	// historyEntryMaxLimit bounds what a client may ask for. Every entry in a
	// page is read from its own manifest and classified, so an unbounded limit is
	// an unbounded read of the whole journal on one request; a client that wants
	// more than this asks for the next page.
	historyEntryMaxLimit = 200
	// historyWarningLimit caps the unreadable-entry list. It is deliberately not
	// the page size: warnings are not paginated and not filtered, for the reason
	// historyResponse.WarningCount gives.
	historyWarningLimit = 20
)

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != historyListAPIRoutePath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeManagementErrorStatus(w, http.StatusMethodNotAllowed, noteops.Errorf(noteops.CodeInvalidRequest, "method not allowed"))
		return
	}
	query, err := parseHistoryQuery(r)
	if err != nil {
		writeManagementError(w, err)
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
	classifier := newHistoryPathClassifier(s.config())
	matched := filterHistoryEntries(entries, query, classifier)
	total := len(matched)
	page := pageHistoryEntries(matched, query)

	warningCount := len(warnings)
	if len(warnings) > historyWarningLimit {
		warnings = warnings[:historyWarningLimit]
	}

	out := make([]historyEntryResponse, 0, len(page))
	for _, entry := range page {
		out = append(out, historyEntryForJournalEntry(entry, classifier))
	}
	writeManagementJSON(w, http.StatusOK, historyResponse{
		OK:           true,
		Entries:      out,
		Total:        total,
		Offset:       query.Offset,
		Limit:        query.Limit,
		HasMore:      query.Offset+len(page) < total,
		Origins:      historyOriginOptions(),
		Classes:      historyPathClassOptions(),
		Warnings:     historyWarningsForJournalWarnings(warnings),
		WarningCount: warningCount,
	})
}

// historyQuery is one client's request for a window onto the journal.
type historyQuery struct {
	Limit  int
	Offset int
	// Origins and Classes are OR within themselves and AND with each other, which
	// is the reading somebody filtering expects: "imports or resolutions, that
	// touched an attachment".
	Origins []historyOrigin
	Classes []historyPathClass
	// Path is case-folded for contains searches and preserved for exact matches
	// against the recorded vault-relative filename.
	Path      string
	PathMatch string
}

// parseHistoryQuery reads the window and the filters, refusing anything it does
// not understand.
//
// An unparseable limit, a negative offset, and an unknown origin are all errors
// rather than defaults. The alternative — quietly dropping a filter — answers a
// narrower question with the whole journal, and the reply looks identical to a
// filter that matched everything. That is the one failure this surface must not
// have: somebody looking for the version they want must never be shown a list
// that hides it, or a list they believe is narrower than it is.
func parseHistoryQuery(r *http.Request) (historyQuery, error) {
	values := r.URL.Query()
	query, err := parseHistoryWindow(values)
	if err != nil {
		return historyQuery{}, err
	}
	if query.Origins, err = parseHistoryFilter(values["origin"], parseHistoryOrigin, "origin"); err != nil {
		return historyQuery{}, err
	}
	if query.Classes, err = parseHistoryFilter(values["class"], parseHistoryPathClass, "file class"); err != nil {
		return historyQuery{}, err
	}
	query.PathMatch = values.Get("path_match")
	switch query.PathMatch {
	case "", "contains":
		query.Path = strings.ToLower(strings.TrimSpace(values.Get("path")))
	case "exact":
		query.Path = values.Get("path")
	default:
		return historyQuery{}, noteops.Errorf(noteops.CodeInvalidRequest, "history: unknown path_match %q", query.PathMatch)
	}
	return query, nil
}

// parseHistoryWindow reads the slice of the journal being asked for. The bound
// is checked here rather than clamped, for the reason parseHistoryQuery gives:
// a client that asked for a page the route will not serve is told so, instead of
// reading a different page than it believes it read.
func parseHistoryWindow(values url.Values) (historyQuery, error) {
	limit, err := parseHistoryInt(values.Get("limit"), historyEntryLimit)
	if err != nil {
		return historyQuery{}, err
	}
	if limit < 1 || limit > historyEntryMaxLimit {
		return historyQuery{}, noteops.Errorf(noteops.CodeInvalidRequest,
			"history: limit must be between 1 and %d", historyEntryMaxLimit)
	}
	offset, err := parseHistoryInt(values.Get("offset"), 0)
	if err != nil {
		return historyQuery{}, err
	}
	if offset < 0 {
		return historyQuery{}, noteops.Errorf(noteops.CodeInvalidRequest, "history: offset must not be negative")
	}
	return historyQuery{Limit: limit, Offset: offset}, nil
}

// parseHistoryFilter reads one repeated filter parameter into its vocabulary,
// de-duplicated and in the order it was sent. Origins and classes are two
// vocabularies with one rule — an unknown value is refused by name and never
// dropped — so they are read by one function rather than by two that could come
// to disagree about what an unrecognized filter means.
func parseHistoryFilter[T comparable](raws []string, parse func(string) (T, bool), kind string) ([]T, error) {
	var out []T
	for _, raw := range raws {
		value, ok := parse(raw)
		if !ok {
			return nil, noteops.Errorf(noteops.CodeInvalidRequest, "history: unknown %s %q", kind, raw)
		}
		if !slices.Contains(out, value) {
			out = append(out, value)
		}
	}
	return out, nil
}

func parseHistoryInt(raw string, fallback int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, noteops.WrapError(noteops.CodeInvalidRequest, "history: invalid number", err)
	}
	return value, nil
}

// filterHistoryEntries keeps the entries a query asks for, in the order
// ListEntries returned them — newest first.
func filterHistoryEntries(entries []journal.Entry, query historyQuery, classifier historyPathClassifier) []journal.Entry {
	if len(query.Origins) == 0 && len(query.Classes) == 0 && query.Path == "" {
		return entries
	}
	matched := make([]journal.Entry, 0, len(entries))
	for _, entry := range entries {
		if historyEntryMatches(entry, query, classifier) {
			matched = append(matched, entry)
		}
	}
	return matched
}

func historyEntryMatches(entry journal.Entry, query historyQuery, classifier historyPathClassifier) bool {
	if len(query.Origins) > 0 && !slices.Contains(query.Origins, historyOriginForCommand(entry.Command)) {
		return false
	}
	if len(query.Classes) > 0 && !historyEntryHasClass(entry, query.Classes, classifier) {
		return false
	}
	if query.Path != "" && !historyEntryHasPath(entry, query.Path, query.PathMatch) {
		return false
	}
	return true
}

func historyEntryHasClass(entry journal.Entry, classes []historyPathClass, classifier historyPathClassifier) bool {
	for _, file := range entry.Files {
		if slices.Contains(classes, classifier.classify(file.Path)) {
			return true
		}
	}
	return false
}

func historyEntryHasPath(entry journal.Entry, needle, mode string) bool {
	for _, file := range entry.Files {
		if mode == "exact" {
			if file.Path == needle {
				return true
			}
		} else if strings.Contains(strings.ToLower(file.Path), needle) {
			return true
		}
	}
	return false
}

// pageHistoryEntries takes the requested window. An offset past the end is an
// empty page rather than an error, because a list that shrank between two
// requests — an entry pruned, a filter narrowed — is ordinary, and refusing the
// request would turn it into a failure somebody has to understand.
func pageHistoryEntries(entries []journal.Entry, query historyQuery) []journal.Entry {
	if query.Offset >= len(entries) {
		return nil
	}
	end := min(query.Offset+query.Limit, len(entries))
	return entries[query.Offset:end]
}

func (s *Server) handleUndo(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != historyUndoAPIRoutePath {
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

	// Re-read before the receipt is judged, not after the restore. The preview
	// pinned the configuration generation it classified the paths under, and a
	// generation this process has not refreshed would compare a stale value to
	// itself and pass over a configuration somebody changed on disk in between.
	if err := s.reloadConfig(); err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "config: reload failed", err))
		return
	}

	req, err := s.parseUndoRequest(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}

	plan, err := noteops.Undo(s.vault.Root, req.EntryID, undoOptionsFor(req))
	if err != nil {
		// Refreshed on the failure path too, and the reload's own error is dropped
		// here rather than reported: a restore error is not proof that nothing
		// landed — a snapshot failure means the bytes and the undo manifest are both
		// durable and only the Git commit is missing, and a transaction cleanup
		// failure means the change is durable as well — so a cache built before the
		// restore would keep serving the version that is no longer on disk. What the
		// person needs to read is the restore's own outcome, not a second failure
		// that replaced it. A transaction cleanup failure does not arrive here at
		// all — it comes back on the plan, as a success carrying a notice.
		_ = s.finishUndo()
		writeUndoError(w, err)
		return
	}

	writeManagementJSON(w, http.StatusOK, undoResponseWithReloadNotice(undoResponseForPlan(plan), s.finishUndo()))
}

// undoResponseWithReloadNotice folds a cache-refresh failure into a restore that
// already happened, instead of replacing it.
//
// Reporting the reload error as the answer would tell somebody whose content is
// back on disk that their undo failed, and would discard the plan's cleanup
// notice along with it — the only mention a leftover temp file ever gets. What
// has actually gone wrong is narrower than "the undo failed" and narrower than
// "this server is stale": finishUndo drops the note snapshot first and that
// cannot fail, so the restored bytes will be re-read either way. It is the
// configuration pair that is now the version from before the restore — which is
// what classifies a path, names the templates directory and supplies property
// semantics — so that is what the notice says. The failure path above drops the
// identical error for the identical reason.
//
// Defensive today, and deliberately kept rather than collapsed into `_ =`:
// configManager.Refresh turns an unreadable or undecodable configuration into a
// published ReloadInvalid *status* and answers nil, so no caller in this package
// can currently reach a non-nil reloadConfig error — which is also why this is
// tested as a composition rather than through the route. It is the one branch
// where a future error would otherwise convert a landed restore into a 500, so
// the answer is stated here instead of being rediscovered then.
func undoResponseWithReloadNotice(response undoResponse, err error) undoResponse {
	if err == nil {
		return response
	}
	response.Message += " The server could not re-read the configuration afterwards," +
		" so settings on this page may be the ones from before the restore: " + err.Error()
	return response
}

// finishUndo refreshes what the server caches about the vault: the note state,
// and the configuration pair a restore may itself have rewritten.
func (s *Server) finishUndo() error {
	s.state.invalidate()
	if err := s.reloadConfig(); err != nil {
		return noteops.WrapError(noteops.CodeInternal, "config: reload failed", err)
	}
	return nil
}

// writeUndoError picks the *shape* of the answer; undoRestoreError picks its
// class. Only a preflight conflict has a shape of its own — the list of newer
// work standing in the way, which is the whole content of the refusal.
func writeUndoError(w http.ResponseWriter, err error) {
	var conflictErr *journal.RestoreConflictError
	if errors.As(err, &conflictErr) {
		writeManagementJSON(w, http.StatusConflict, undoConflictResponseForError(conflictErr))
		return
	}
	writeManagementError(w, undoRestoreError(err))
}

// undoRequest is a restoration somebody has asked for, after every
// precondition that can be checked without writing has been checked.
type undoRequest struct {
	EntryID string
	Force   bool
	// Require is the state each recorded path was in when somebody was shown
	// what the restore would do, carried down so the transaction enforces the
	// consent rather than this layer merely having checked it. Nil when no
	// preview was supplied.
	Require map[string]vault.FileExpectation
}

// parseUndoRequest reads the request and decides whether it may proceed.
//
// The rule it adds is that force needs a preview. Without force, a restore
// cannot replace newer work at all: every target is pinned to the state on disk
// at plan time, so one drifted path aborts the whole restore before anything is
// written — that precondition is the journal's, and it is why an unpreviewed
// undo stays allowed and the two callers that post here without one keep
// working. Force removes that protection, and a request that removes it has to
// carry a receipt proving somebody was shown what it would overwrite.
// confirm_force is kept alongside: the receipt proves the overwrite was
// *shown*, the confirmation flag says it was *accepted*, and neither implies
// the other.
//
// A preview is verified whenever one is supplied, force or not, so a client
// that previews a clean restore and finds it dirty by the time it confirms is
// refused rather than quietly falling through to the weaker check.
func (s *Server) parseUndoRequest(r *http.Request) (undoRequest, error) {
	force := managementBoolFormValue(r, "force")
	confirmForce := managementBoolFormValue(r, "confirm_force", "force_confirmed") ||
		strings.EqualFold(strings.TrimSpace(r.Form.Get("confirm")), "force")
	if force && !confirmForce {
		return undoRequest{}, noteops.Errorf(noteops.CodeInvalidRequest, "force undo requires confirmation")
	}

	entryID, err := s.resolveHistoryEntryID(r)
	if err != nil {
		return undoRequest{}, err
	}

	previewID, _ := managementFormValue(r, "preview_id")
	previewID = strings.TrimSpace(previewID)
	if force && previewID == "" {
		return undoRequest{}, noteops.Errorf(noteops.CodePreviewRequired,
			"history: forcing a restore requires a preview of the newer work it would replace")
	}
	req := undoRequest{EntryID: entryID, Force: force}
	if previewID != "" {
		require, err := s.checkHistoryRestorePreview(previewID, entryID)
		if err != nil {
			return undoRequest{}, err
		}
		req.Require = require
	}
	return req, nil
}

// undoRestoreError says what state the restore left the vault in, which for a
// recovery operation is the only thing a person can act on. "undo failed" is the
// last resort, not the answer.
//
// The order is internal/vaultimport's classifyCommit and this package's own
// syncWriteCode, for the reasons both state: these errors nest each other, so
// asking the general question first gives the specific answer away. A snapshot
// failure unwraps to a busy Git lock; a failed rollback and an un-put-back file
// unwrap to the mutation that failed, which is usually the very conflict this
// would otherwise report as "try again" — burying the one sentence naming the
// file somebody's content is now sitting in. A cleanup failure is the inverse
// trap: it reads as a commit error and means the commit succeeded.
func undoRestoreError(err error) error {
	var opErr *noteops.Error
	switch {
	// Already classified by the layer that knew best: BeginOperation's location
	// guard and Git preflight, and the snapshot failure that says the files are
	// restored and further writes are blocked.
	case errors.As(err, &opErr), noteops.IsSnapshotFailure(err), vault.IsForbiddenMutation(err):
		return err
	case vault.IsTxnCleanupFailure(err):
		// Defensive, and deliberately not reassuring. journal.Restore carries a
		// cleanup failure out on RestorePlan.Cleanup precisely so the operation
		// still reaches Finalize, so this arm should be unreachable from a restore
		// — and arriving here would mean it did not: the bytes are on disk with no
		// undo manifest authorizing them and no snapshot recording them, which is
		// the state vaultgit refuses the next mutation over.
		return noteops.WrapError(noteops.CodeInternal,
			"history: the restore was applied but not recorded, and further writes may be refused until the vault is checked", err)
	case vault.IsTxnRollbackFailure(err), vault.IsTxnRecoveryNeeded(err):
		// The one state where content is provably somewhere nobody asked for it.
		// CodeInternal rather than CodeSafety, which is documented as the engine
		// refusing *before* anything changed; the error's own text names the file.
		return noteops.WrapError(noteops.CodeInternal,
			"history: the restore failed partway and could not put everything back", err)
	case vault.IsTxnConflict(err), vault.IsTxnLockBusy(err):
		// A path that moved between the plan and the commit, or away from the state
		// a preview pinned. It is the same refusal a preflight conflict is, made
		// later, so it is answered on the same channel rather than as a 500.
		return noteops.WrapError(noteops.CodeConflict,
			"history: the vault changed while the restore was being applied", err)
	case errors.Is(err, os.ErrNotExist):
		// Asked last of the specific cases: this is LoadEntry failing before any
		// transaction exists, and every state above it can unwrap to a missing file
		// of its own.
		return noteops.Errorf(noteops.CodeNotFound, "undo entry not found")
	}
	return noteops.WrapError(noteops.CodeJournal, "undo failed", err)
}

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
		Force:   req.Force,
		Command: "bn web undo",
		Require: req.Require,
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
