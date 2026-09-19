package web

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

const historyPreviewAPIRoutePath = "/api/history/preview"

// A restore that is refused on drift cannot replace newer work: planRestore
// pins each target to the state on disk right now, so any path somebody else
// touched aborts the whole restore before a byte is written. Force is what
// removes that protection, and until this route existed force was reachable
// from a client that had never been shown what it was about to overwrite — a
// flag and a confirmation flag, both set in the same request.
//
// historyRestoreRecord is what a preview authorizes: not "restore this entry",
// but "restore this entry over exactly these pieces of newer work". Apply
// re-plans and compares, so a conflict that appeared between the preview and the
// confirmation is a refusal rather than a silent extra overwrite.
type historyRestoreRecord struct {
	EntryID string
	// Changes and Conflicts are sorted vault-relative paths. They are compared as
	// sets rather than counted: a conflict that vanished and a different one that
	// appeared would keep the count identical while changing entirely what the
	// person agreed to lose.
	Changes   []string
	Conflicts []string
}

// handleHistoryPreview answers what restoring one entry would do, and mints the
// one-use receipt that authorizes forcing it.
//
// It writes nothing. PreviewRestore reads no snapshot and takes no lock, so a
// vault that cannot currently be written can still be inspected for what is
// recoverable — which is the state somebody is most likely to be in when they
// come looking.
func (s *Server) handleHistoryPreview(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != historyPreviewAPIRoutePath {
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
	// The same reload history and undo do, and for the same reason: a restore can
	// name either configuration file, so the generation this preview is pinned to
	// has to be the one on disk now.
	if err := s.reloadConfig(); err != nil {
		writeManagementError(w, noteops.WrapError(noteops.CodeInternal, "config: reload failed", err))
		return
	}

	entryID, err := s.resolveHistoryEntryID(r)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	plan, conflicts, err := journal.PreviewRestore(s.vault.Root, noteops.JournalRoot(s.vault.Root), entryID)
	if err != nil {
		writeManagementError(w, historyPreviewError(err))
		return
	}

	record, pins, err := s.historyRestoreReceipt(entryID, plan, conflicts)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	receipt, err := s.propertyPreviews.store(propertyPreviewRecord{
		Kind:       propertyPreviewRestore,
		Generation: s.configState.Status().Generation,
		Restore:    record,
		Files:      pins,
	})
	if err != nil {
		writeManagementError(w, err)
		return
	}

	writeManagementJSON(w, http.StatusOK, s.historyPreviewResponse(receipt, plan, conflicts))
}

// resolveHistoryEntryID reads the requested entry, falling back to the newest
// readable one exactly as undo does, so previewing and confirming the "undo the
// last thing" shorthand cannot land on two different entries by accident — the
// preview answers with the identifier it resolved, and the confirmation sends
// that identifier back rather than the shorthand.
func (s *Server) resolveHistoryEntryID(r *http.Request) (string, error) {
	entryID, _ := managementFormValue(r, "id", "entry_id")
	entryID = strings.TrimSpace(entryID)
	if entryID != "" {
		return entryID, nil
	}
	return latestHistoryEntryID(noteops.JournalRoot(s.vault.Root))
}

// historyPreviewError keeps a missing entry a 404 rather than letting it become
// a generic journal failure, which is the difference between "that entry was
// pruned" and "the history is broken".
func historyPreviewError(err error) error {
	// errors.Is rather than os.IsNotExist: LoadEntry wraps the read failure with
	// the entry it was looking for, and os.IsNotExist does not unwrap — which
	// reported every pruned entry as a broken history.
	if errors.Is(err, os.ErrNotExist) {
		return noteops.Errorf(noteops.CodeNotFound, "undo entry not found")
	}
	return noteops.WrapError(noteops.CodeJournal, "history: preview restore", err)
}

// historyRestoreReceipt builds what the receipt pins: the path sets the person
// is being shown, and the current bytes at every path the restore would write.
func (s *Server) historyRestoreReceipt(entryID string, plan *journal.RestorePlan, conflicts []journal.RestoreConflict) (historyRestoreRecord, []propertyPreviewFileState, error) {
	record := historyRestoreRecord{
		EntryID:   entryID,
		Changes:   historyPlanPaths(plan),
		Conflicts: historyConflictPaths(conflicts),
	}
	pins := make([]propertyPreviewFileState, 0, len(record.Changes))
	for _, rel := range record.Changes {
		abs := filepath.Join(s.vault.Root, filepath.FromSlash(rel))
		digest, err := historyRestoreDigest(abs)
		if err != nil {
			return historyRestoreRecord{}, nil, err
		}
		pins = append(pins, propertyPreviewFileState{RelPath: rel, AbsPath: abs, Digest: digest})
	}
	return record, pins, nil
}

func historyPlanPaths(plan *journal.RestorePlan) []string {
	if plan == nil {
		return nil
	}
	paths := make([]string, 0, len(plan.Changes))
	for _, change := range plan.Changes {
		paths = append(paths, change.Path)
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

func historyConflictPaths(conflicts []journal.RestoreConflict) []string {
	paths := make([]string, 0, len(conflicts))
	for _, conflict := range conflicts {
		paths = append(paths, conflict.Path)
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

// historyRestoreDigest describes what is at a path now, in a form two reads can
// be compared by.
//
// It deliberately does not fail on a path that is not a regular file, which is
// where it parts company with propertyFileDigest. A configuration write is
// planned against files the server itself manages, so "this is no longer a
// regular file" is a genuine surprise worth refusing on. A restore is aimed at
// whatever the vault has become — a note replaced by a directory, a path turned
// into a symlink — and refusing to *describe* that state would make the one
// entry somebody needs impossible to preview, and so impossible to recover. The
// state is recorded as a kind instead, and a change of kind between preview and
// confirmation is caught by the same comparison a change of bytes is.
func historyRestoreDigest(absPath string) (string, error) {
	info, err := os.Lstat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", noteops.WrapError(noteops.CodePreviewStale, "history: stat restore path", err)
	}
	if !info.Mode().IsRegular() {
		return "kind:" + info.Mode().Type().String(), nil
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", noteops.WrapError(noteops.CodePreviewStale, "history: read restore path", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// historyRestoreFilesUnchanged reports whether every pinned path still holds
// what the preview was computed from.
func historyRestoreFilesUnchanged(expected []propertyPreviewFileState) error {
	for _, state := range expected {
		digest, err := historyRestoreDigest(state.AbsPath)
		if err != nil {
			return err
		}
		if digest != state.Digest {
			return noteops.Errorf(noteops.CodePreviewStale, "history: %s changed since the preview", state.RelPath)
		}
	}
	return nil
}

// checkHistoryRestorePreview redeems a receipt, re-checks everything it pinned
// against the vault as it is now, and hands back those pins in the form the
// transaction can enforce them in.
//
// The digest check and the replan are not redundant. The digests catch a path
// whose bytes moved; the replan catches a *conflict set* that moved for a reason
// the pinned paths cannot show — an entry whose manifest was rewritten, or a
// path that has newly drifted from its recorded "after" state without its
// current bytes differing from what the preview read. Both have to hold, because
// what was authorized was the pair.
//
// Neither is sufficient, which is what the returned map is for. Both run here,
// outside the vault's locks and before noteops.BeginOperation's Git preflight,
// so a writer that lands in that window — bn-vault-sync applying a peer's
// change, most realistically — changes the bytes without changing the conflict
// *path set*, and a forced restore would then overwrite content nobody was ever
// shown. Carrying the pins down to journal.RestoreOptions.Require is what makes
// the consent a precondition of the write itself rather than a check that
// happened to precede it.
func (s *Server) checkHistoryRestorePreview(previewID, entryID string) (map[string]vault.FileExpectation, error) {
	record, err := s.propertyPreviews.consume(previewID, propertyPreviewRestore)
	if err != nil {
		return nil, err
	}
	if record.Restore.EntryID != entryID {
		return nil, noteops.Errorf(noteops.CodePreviewRequired, "history: preview is for a different entry")
	}
	if err := historyRestoreFilesUnchanged(record.Files); err != nil {
		return nil, err
	}
	if record.Generation != s.configState.Status().Generation {
		return nil, noteops.Errorf(noteops.CodePreviewStale, "history: configuration changed since the preview")
	}
	plan, conflicts, err := journal.PreviewRestore(s.vault.Root, noteops.JournalRoot(s.vault.Root), entryID)
	if err != nil {
		return nil, historyPreviewError(err)
	}
	if !slices.Equal(historyPlanPaths(plan), record.Restore.Changes) {
		return nil, noteops.Errorf(noteops.CodePreviewStale, "history: the restore now covers different paths")
	}
	if !slices.Equal(historyConflictPaths(conflicts), record.Restore.Conflicts) {
		return nil, noteops.Errorf(noteops.CodePreviewStale, "history: newer work at these paths changed since the preview")
	}
	return historyRestoreRequirements(record.Files)
}

// historyRestoreRequirements turns the receipt's pins into transaction
// preconditions, keyed by the path exactly as the journal entry spells it —
// which is where historyRestoreReceipt took them from, so the map names every
// path the restore will stage and journal.RestoreOptions.Require's
// name-them-all rule holds by construction.
//
// A non-regular file is the one state that cannot be pinned. historyRestoreDigest
// deliberately describes it instead of failing, so that a vault where a note has
// become a directory can still be *previewed*; but vault.FileExpectation can say
// only "absent" or "these bytes", and calling a directory either one would pin
// the restore to something untrue. The restore is refused instead, which costs
// nothing a person could otherwise have had: the journal refuses to remove or
// overwrite a directory anyway, and this way the refusal names the reason.
func historyRestoreRequirements(pins []propertyPreviewFileState) (map[string]vault.FileExpectation, error) {
	require := make(map[string]vault.FileExpectation, len(pins))
	for _, pin := range pins {
		switch {
		case pin.Digest == "":
			require[pin.RelPath] = vault.ExpectMissing()
		case strings.HasPrefix(pin.Digest, "sha256:"):
			require[pin.RelPath] = vault.ExpectHash(strings.TrimPrefix(pin.Digest, "sha256:"))
		default:
			return nil, noteops.Errorf(noteops.CodeConflict,
				"history: %s is not a regular file, so this restore cannot be authorized against it", pin.RelPath)
		}
	}
	return require, nil
}

type historyPreviewResponse struct {
	OK        bool   `json:"ok"`
	PreviewID string `json:"preview_id"`
	ExpiresAt string `json:"expires_at"`
	Message   string `json:"message"`

	EntryID     string `json:"entry_id"`
	Command     string `json:"command"`
	Origin      string `json:"origin"`
	OriginLabel string `json:"origin_label"`
	Timestamp   string `json:"timestamp"`

	// RequiresForce says this restore cannot proceed without overriding newer
	// work. It is the same flag the conflict reply carries, answered before
	// anything is attempted rather than after a refusal.
	RequiresForce bool                       `json:"requires_force"`
	Changes       []historyPreviewChange     `json:"changes"`
	Conflicts     []undoConflictItemResponse `json:"conflicts,omitempty"`
}

// historyPreviewChange is one path the restore would write, with the same facts
// the history list reports about it, so the confirmation screen can say which of
// the restored paths will reach the other replica and which stay here.
type historyPreviewChange struct {
	Path          string `json:"path"`
	CurrentState  string `json:"current_state"`
	RestoredState string `json:"restored_state"`
	Class         string `json:"class"`
	Recorded      bool   `json:"recorded"`
	Travels       bool   `json:"travels"`
	Conflict      bool   `json:"conflict"`
}

func (s *Server) historyPreviewResponse(receipt propertyPreviewReceipt, plan *journal.RestorePlan, conflicts []journal.RestoreConflict) historyPreviewResponse {
	classifier := newHistoryPathClassifier(s.config())
	conflicting := historyConflictPaths(conflicts)

	changes := make([]historyPreviewChange, 0)
	entryID, command, timestamp := "", "", ""
	if plan != nil {
		entryID = plan.EntryID
		command = plan.Command
		timestamp = plan.Timestamp.UTC().Format(time.RFC3339Nano)
		for _, change := range plan.Changes {
			facts := classifier.facts(change.Path)
			changes = append(changes, historyPreviewChange{
				Path:          change.Path,
				CurrentState:  change.CurrentState,
				RestoredState: change.RestoredState,
				Class:         string(facts.Class),
				Recorded:      facts.Recorded,
				Travels:       facts.Travels,
				Conflict:      slices.Contains(conflicting, change.Path),
			})
		}
	}
	origin := historyOriginForCommand(command)
	return historyPreviewResponse{
		OK:            true,
		PreviewID:     receipt.ID,
		ExpiresAt:     receipt.ExpiresAt.UTC().Format(time.RFC3339Nano),
		Message:       historyPreviewMessage(len(changes), len(conflicts)),
		EntryID:       entryID,
		Command:       command,
		Origin:        string(origin),
		OriginLabel:   origin.label(),
		Timestamp:     timestamp,
		RequiresForce: len(conflicts) > 0,
		Changes:       changes,
		Conflicts:     historyConflictItems(conflicts),
	}
}

func historyPreviewMessage(changes, conflicts int) string {
	if conflicts == 0 {
		return "Restoring this entry rewrites " + pathCountLabel(changes) + " and replaces no newer work."
	}
	return "Restoring this entry rewrites " + pathCountLabel(changes) +
		" and would replace newer work at " + pathCountLabel(conflicts) + "."
}

func historyConflictItems(conflicts []journal.RestoreConflict) []undoConflictItemResponse {
	if len(conflicts) == 0 {
		return nil
	}
	out := make([]undoConflictItemResponse, 0, len(conflicts))
	for _, conflict := range conflicts {
		out = append(out, undoConflictItemResponse{
			Path:          conflict.Path,
			ExpectedState: conflict.Expected,
			CurrentState:  conflict.Actual,
		})
	}
	return out
}
