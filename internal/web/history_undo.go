package web

import (
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

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
		// refusing *before* anything changed. The error's own text names the file,
		// so it is the message: a fixed sentence would hide the recovery path.
		return noteops.WrapError(noteops.CodeInternal,
			"history: the restore failed partway and could not put everything back: "+err.Error(), err)
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
