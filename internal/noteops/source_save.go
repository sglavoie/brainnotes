package noteops

import (
	"path/filepath"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// SaveSourceRequest is one whole-note Markdown write.
type SaveSourceRequest struct {
	RelPath string
	Content string
	// BaseRevision is the revision the edit was made against. It is required for
	// a save of an existing note and must be empty for a create.
	BaseRevision string
	// SaveID is the client's idempotency key, or empty to opt out of one. An id
	// makes a resubmission answerable even after the note moved on; without one
	// a resubmission is answerable only while the bytes still converge.
	SaveID string
	// Create asks for a note that does not exist yet.
	Create bool
	// Attachments are staged uploads this note references and that must land
	// with it. They are committed in the note's own transaction, under the note's
	// own journal entry, so a save either stores the note and every file it
	// points at or stores none of them — and one `bn undo` takes back all of it.
	Attachments []AttachmentCommit
	// UnresolvedAttachments names uploads this note references that the caller
	// could not find in staging.
	//
	// They are carried rather than refused by the caller because a save that
	// already happened is exactly the save whose staging is gone: the first
	// attempt committed the bytes and dropped the staged copy, and the retry that
	// never saw the answer names the same upload ids again. So this is refused
	// *after* a keyed save has had the chance to replay its recorded outcome, and
	// only then — a save that is genuinely writing a note must not store a link
	// to a file nothing is going to put there.
	UnresolvedAttachments []string
}

// SaveSourceResult reports what a save did.
type SaveSourceResult struct {
	Destination PathResolution
	// Revision is the note's revision after the save, which is the token the
	// client has to carry into its next edit. It is filled in on every answer
	// that leaves the submitted bytes on disk, including a replay, a converged
	// no-op, and a save whose snapshot failed.
	Revision string
	// Changed is false for a save this call did not have to write: the note
	// already held these exact bytes, or a recorded outcome answered it.
	Changed bool
	Created bool
	// Replayed is true when this answer came from a persisted outcome rather
	// than from a write this call performed.
	Replayed bool
	// Attachments names every referenced upload that is in the vault once this
	// answer is given, whether this call wrote it or a previous attempt did. It
	// is what lets a client stop holding the staged copy.
	Attachments []SavedAttachment
	Journal     *journal.Entry
}

// SaveSource writes relPath's whole Markdown source, journaled and pinned to the
// revision the edit was made against.
//
// The six ways a submitted note can be lost are each answered rather than
// swallowed, and the order the checks run in is part of that:
//
//   - A *stale* base revision is refused before anything is written, as a
//     SourceRevisionError the caller can turn into a merge.
//   - A *duplicate* submission and a *lost response* are the same request twice.
//     A keyed save replays its recorded outcome; an unkeyed one converges,
//     because the bytes on disk already are the bytes submitted.
//   - *Invalid frontmatter* is refused first of all, so nothing else — not a
//     recorded outcome, not a revision comparison — can turn unstorable content
//     into a success.
//   - A *no-op* is reported as a success that changed nothing and returns the
//     revision the note is actually at, never as a conflict.
//   - A *snapshot failure* comes back as an error *with* a non-nil result: the
//     bytes are on disk and the caller has to be able to say so.
//
// Callers must handle that last shape. It is the one case where both return
// values are non-nil, and a caller that checks err alone will report a landed
// save as a failure and invite a resubmission planned against a revision this
// save already replaced.
//
// Unlike a capture this never replans on a conflict. An append is commutative
// and a whole-note save is not: re-reading and retrying would overwrite whatever
// the other writer just stored, so a conflict is the user's answer to give.
func SaveSource(vaultRoot string, cfg *config.Config, req SaveSourceRequest, opts ...SourceOption) (*SaveSourceResult, error) {
	options := sourceOptions{command: "bn source save"}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	plan, err := planSourceSave(vaultRoot, cfg, req)
	if err != nil {
		return nil, err
	}
	if plan.settled != nil {
		return plan.settled, nil
	}

	result, err := commitSourceSave(vaultRoot, plan, options)
	if err != nil && result == nil {
		if replay := replaySourceSaveAfterConflict(plan); replay != nil {
			return replay, nil
		}
	}
	return result, err
}

// sourceSavePlan is one planned save: the exact bytes, the precondition they are
// pinned to, and the record the same transaction will carry.
type sourceSavePlan struct {
	destination PathResolution
	content     []byte
	expect      vault.FileExpectation
	// revision is the digest of content, which is both the record's digest and
	// the revision the note ends at — a whole-file write makes those the same
	// number.
	revision string
	base     string
	created  bool
	// attachments are the referenced uploads: the ones this transaction has to
	// write, and the full list the answer reports.
	attachments attachmentPlan
	ledger      *sourceSaveLedgerWrite
	// settled holds the answer for a save that needs no transaction at all: a
	// replayed record, or a note that already holds the submitted bytes.
	settled *SaveSourceResult
}

func planSourceSave(vaultRoot string, cfg *config.Config, req SaveSourceRequest) (sourceSavePlan, error) {
	if err := validateSaveSourceRequest(req); err != nil {
		return sourceSavePlan{}, err
	}
	dest, err := resolveSourceDestination(vaultRoot, cfg, req.RelPath)
	if err != nil {
		return sourceSavePlan{}, err
	}
	content := []byte(req.Content)
	digest := vault.HashBytes(content)

	ledger, replay, err := planSourceSaveLedger(vaultRoot, dest, req.SaveID, digest)
	if err != nil {
		return sourceSavePlan{}, err
	}
	if replay != nil {
		return sourceSavePlan{settled: replay}, nil
	}
	return planSourceSaveWrite(vaultRoot, cfg, req, sourceSaveTarget{
		destination: dest,
		content:     content,
		revision:    digest,
		ledger:      ledger,
	})
}

// sourceSaveTarget is what the checks above settled before anything about the
// note on disk was read: where the bytes go, what they are, and the record that
// will answer a resubmission.
type sourceSaveTarget struct {
	destination PathResolution
	content     []byte
	revision    string
	ledger      *sourceSaveLedgerWrite
}

// planSourceSaveWrite plans a save that is really going to write, which is the
// only kind that reaches it: a replayed one has already been answered.
//
// That is why the unresolved uploads are refused here rather than at the top.
// The save whose staging is gone is very often the save that already succeeded
// and cleared it, so refusing before the replay would turn the retry a lost
// response invites into a permanent failure.
func planSourceSaveWrite(vaultRoot string, cfg *config.Config, req SaveSourceRequest, target sourceSaveTarget) (sourceSavePlan, error) {
	if err := refuseUnresolvedAttachments(req.UnresolvedAttachments); err != nil {
		return sourceSavePlan{}, err
	}
	attachments, err := planAttachmentCommits(vaultRoot, req.Attachments)
	if err != nil {
		return sourceSavePlan{}, err
	}
	current, err := ReadSource(vaultRoot, cfg, target.destination.RelPath)
	if err != nil {
		return sourceSavePlan{}, err
	}
	settled, err := settledSourceSave(current, target.revision, attachments)
	if err != nil {
		return sourceSavePlan{}, err
	}
	if settled != nil {
		return sourceSavePlan{settled: settled}, nil
	}
	expect, err := sourceSaveExpectation(req, current)
	if err != nil {
		return sourceSavePlan{}, err
	}
	plan := sourceSavePlan{
		destination: target.destination,
		content:     target.content,
		expect:      expect,
		revision:    target.revision,
		base:        req.BaseRevision,
		created:     !current.Exists,
		attachments: attachments,
		ledger:      target.ledger,
	}
	recordSourceSave(target.ledger, req, plan)
	return plan, nil
}

// recordSourceSave fills in the outcome the transaction will persist under this
// save id, for a save that has one.
func recordSourceSave(ledger *sourceSaveLedgerWrite, req SaveSourceRequest, plan sourceSavePlan) {
	if ledger == nil {
		return
	}
	ledger.record = sourceSaveRecord{
		Version:     sourceSaveRecordVersion,
		SaveID:      strings.TrimSpace(req.SaveID),
		Digest:      plan.revision,
		RelPath:     plan.destination.RelPath,
		Revision:    plan.revision,
		Created:     plan.created,
		Attachments: plan.attachments.saved,
	}
}

// settledSourceSave answers the save that needs no transaction, and refuses the
// one that looks like it but is not.
//
// A converged note whose attachments are all in the vault is finished: there is
// nothing left to write, and the answer names them so the client can drop its
// staged copies.
//
// A converged note with an attachment still in staging is a different thing
// wearing the same shape, and writing the file anyway would not fix it: the note
// already holds these exact bytes, so there is no note write to carry the
// attachment, and committing the file alone would journal a change to a note
// that did not change. It is refused, because the alternative is reporting
// success over a file that is not in the vault and will be swept out of staging
// within the day.
func settledSourceSave(current *SourceDocument, digest string, attachments attachmentPlan) (*SaveSourceResult, error) {
	settled := convergedSourceSave(current, digest)
	if settled == nil {
		return nil, nil
	}
	if len(attachments.pending) > 0 {
		return nil, Errorf(CodeConflict,
			"source: %s already holds these bytes, so nothing was written, and %d referenced attachment(s) are not in the vault",
			current.Destination.RelPath, len(attachments.pending))
	}
	settled.Attachments = attachments.saved
	return settled, nil
}

// validateSaveSourceRequest refuses a request that could not be stored, and one
// whose revision fields contradict what it is asking for.
//
// Content validation runs first, ahead of the record lookup, so a reused save id
// carrying unstorable bytes is refused rather than answered with the outcome the
// id recorded for different content. Refusing loudly is the point: replaying
// there would be the silent discard.
func validateSaveSourceRequest(req SaveSourceRequest) error {
	if err := ValidateSource(req.Content); err != nil {
		return err
	}
	if err := validateSourceSaveID(req.SaveID); err != nil {
		return err
	}
	base := strings.TrimSpace(req.BaseRevision)
	if req.Create {
		if base != "" {
			return Errorf(CodeInvalidRequest, "source: a create must not carry a base revision")
		}
		return nil
	}
	if base == "" {
		return Errorf(CodeInvalidRequest, "source: a save must carry the revision it was made against")
	}
	if !validSourceRevision(base) {
		return Errorf(CodeInvalidRequest, "source: base revision is not a content digest")
	}
	return nil
}

// convergedSourceSave answers the save whose bytes the note already holds.
//
// The check runs ahead of the revision comparison on purpose. A client
// resubmitting after a lost response — or a second tab saving the same edit —
// hands over a base revision that is stale *because of its own earlier save*,
// and calling that a conflict would tell somebody their edit was rejected while
// the file on disk holds exactly what they asked for. An identical outcome is
// convergence, not contention.
//
// It is also the answer to a repeated create, which is why it runs ahead of the
// existence check as well. A destination already holding exactly these bytes is
// the create that already happened; refusing it as a conflict would report a
// collision to the very client whose request produced the file.
//
// Nothing is written, and that includes the record: a converged save is
// self-idempotent, because the next resubmission converges the same way.
func convergedSourceSave(current *SourceDocument, digest string) *SaveSourceResult {
	if current == nil || !current.Exists || current.Revision != digest {
		return nil
	}
	return &SaveSourceResult{
		Destination: current.Destination,
		Revision:    current.Revision,
	}
}

// sourceSaveExpectation is the precondition the transaction pins the note to.
//
// It is derived from what was just read rather than from what the client sent,
// which is why the stale answer and the transaction's answer cannot disagree:
// the comparison here produces the message, and the same digest goes into the
// expectation that produces the guarantee.
func sourceSaveExpectation(req SaveSourceRequest, current *SourceDocument) (vault.FileExpectation, error) {
	if req.Create {
		if current.Exists {
			return vault.FileExpectation{}, newSourceExistsError(current.Destination.RelPath, current.Revision)
		}
		return vault.ExpectMissing(), nil
	}
	if !current.Exists {
		return vault.FileExpectation{}, Errorf(CodeNotFound,
			"source: %s no longer exists, so nothing was written", current.Destination.RelPath)
	}
	if current.Revision != strings.TrimSpace(req.BaseRevision) {
		return vault.FileExpectation{}, newSourceRevisionError(
			current.Destination.RelPath, req.BaseRevision, current.Revision)
	}
	return vault.ExpectHash(current.Revision), nil
}

// commitSourceSave writes the note, its attachments, the idempotency record,
// and nothing else, in one transaction under one undo entry.
func commitSourceSave(vaultRoot string, plan sourceSavePlan, cfg sourceOptions) (*SaveSourceResult, error) {
	txn, err := vault.NewFileTxn(vaultRoot)
	if err != nil {
		return nil, WrapError(CodeInternal, "source: open transaction", err)
	}
	if err := txn.Write(plan.destination.AbsPath, plan.content, 0, plan.expect); err != nil {
		return nil, classifyTxnError("source: plan save", err)
	}
	if err := stageAttachments(txn, plan.attachments); err != nil {
		return nil, err
	}
	if err := stageSourceSaveLedger(txn, plan.ledger); err != nil {
		return nil, err
	}
	for _, requirement := range cfg.requires {
		if err := txn.Require(requirement.path, requirement.expect); err != nil {
			return nil, classifyTxnError("source: pin configuration", err)
		}
	}
	// A create journals a file that does not exist yet, so `bn undo` deletes the
	// note it made. The record is journaled with it and goes back the same way,
	// which is what keeps an undone save re-submittable. The attachments are
	// named here for the same reason the note is: journaling them is what makes
	// the one undo take back the whole save rather than the prose alone, leaving
	// uploaded files nothing refers to.
	targets := append([]string{plan.destination.AbsPath}, plan.attachments.paths()...)
	j, err := BeginOperation(vaultRoot, "save", cfg.command, targets, cfg.journalOptions...)
	if err != nil {
		return nil, err
	}
	defer j.Release()
	if err := txn.Commit(j); err != nil {
		return nil, classifySourceSaveTxnError(plan, err)
	}
	result := &SaveSourceResult{
		Destination: plan.destination,
		Revision:    plan.revision,
		Changed:     true,
		Created:     plan.created,
		Attachments: plan.attachments.saved,
	}
	prunePlannedSourceLedger(plan)
	entry, err := j.Finalize()
	if err != nil {
		// The bytes are on disk and the history does not cover them. The result
		// comes back beside the error because a caller told only "500" resubmits,
		// and that resubmission would be planned against a revision this save
		// already replaced — turning an honest snapshot failure into a lost edit.
		return result, err
	}
	result.Journal = entry
	return result, nil
}

func prunePlannedSourceLedger(plan sourceSavePlan) {
	if plan.ledger == nil {
		return
	}
	// The operation still owns its checkpoint admission here. Ledger pruning is
	// receipt housekeeping, but it is nevertheless a durable mutation and must
	// not be allowed to trail the note/history boundary.
	pruneSourceSaveLedger(filepath.Dir(plan.ledger.destination.AbsPath))
}

// classifySourceSaveTxnError turns the transaction's verdict into this API's.
//
// A plain conflict means a writer landed between the read and the commit, which
// is the same thing a stale base revision means and has to be answered the same
// way. No actual revision is named: the file has moved again, and a digest read
// here could be stale before it reached the client. A conflict reported
// alongside displaced content or a failed rollback carries recovery
// instructions and is passed through untouched, which is the same ordering
// captureShouldReplan uses.
//
// The conflict is reported against the note even though the transaction pins two
// paths, because the second is the idempotency record and its path derives from
// the save id alone. Losing that race takes a client that reused one id across
// two notes, and the message is then wrong about which path moved — but the
// retry is answered precisely: planSourceSaveLedger finds a usable record naming
// a different note and says so, in the one sentence that tells the client to mint
// a fresh id.
func classifySourceSaveTxnError(plan sourceSavePlan, err error) error {
	if vault.IsTxnRecoveryNeeded(err) || vault.IsTxnRollbackFailure(err) {
		return classifyTxnError("source: save note", err)
	}
	if vault.IsTxnConflict(err) {
		return newSourceRevisionError(plan.destination.RelPath, plan.base, "")
	}
	return classifyTxnError("source: save note", err)
}

// replaySourceSaveAfterConflict answers a conflict that a duplicate of this very
// request caused.
//
// Two identical keyed submissions in flight at once both plan against the same
// state; the winner lands and the loser's preconditions all fail, on the note
// and on the record alike. Re-reading the record once is what tells those apart
// from a real conflict: a record naming this save id, these bytes and this path
// exists only because this exact save landed, so replaying it is reporting the
// truth rather than retrying anything.
//
// One re-read, not a loop. Either the record is there or the conflict was real.
func replaySourceSaveAfterConflict(plan sourceSavePlan) *SaveSourceResult {
	if plan.ledger == nil {
		return nil
	}
	state, err := readSourceSaveLedgerState(plan.ledger.destination.AbsPath)
	if err != nil || !state.usable {
		return nil
	}
	if state.record.Digest != plan.revision || state.record.RelPath != plan.destination.RelPath {
		return nil
	}
	return replayedSourceSave(plan.destination, state.record)
}
