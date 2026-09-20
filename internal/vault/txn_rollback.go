package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// rollback restores every applied change to the state captured during staging,
// newest first. It reports the original failure when compensation succeeds and
// a *TxnRollbackError naming the mixed paths when it does not.
func (t *FileTxn) rollback(failedPath string, cause error, applied []*txnChange) error {
	failure := &TxnRollbackError{Path: failedPath, Err: cause}
	for i := len(applied) - 1; i >= 0; i-- {
		change := applied[i]
		if err := t.restore(change); err != nil {
			failure.Failed = append(failure.Failed, change.path)
			failure.RollbackErrs = append(failure.RollbackErrs, err)
			continue
		}
		failure.Restored = append(failure.Restored, change.path)
	}
	if len(failure.RollbackErrs) > 0 {
		return failure
	}
	return cause
}

// restore replays the bytes staging captured for one applied path. A path that
// no longer holds this transaction's committed bytes is left exactly as it is
// and reported as mixed, because the difference can only come from a writer
// that ran after this transaction applied it.
//
// The committed-bytes check cannot be a plain read followed by a write: a save
// landing between the two would be overwritten by a check that already passed.
// Each undo therefore mirrors the mutation it compensates, and judges the bytes
// it actually displaces:
//
//   - Undoing an overwrite swaps the prior content back and requires what it
//     displaces to be this transaction's own bytes. The path stays occupied
//     throughout, exactly as the forward direction does.
//   - Undoing a create deletes the path, so custody is the right primitive: the
//     end state is vacancy.
//   - Undoing a delete places the prior content exclusively, so a writer that
//     recreated the path in the meantime keeps its file.
func (t *FileTxn) restore(change *txnChange) error {
	if err := t.verifyStillCommitted(change); err != nil {
		return err
	}
	if t.hooks.beforeRestore != nil {
		if err := t.hooks.beforeRestore(change.path); err != nil {
			return err
		}
	}
	switch {
	case change.remove:
		if !change.priorExists {
			return nil
		}
		return t.restoreRemoved(change)
	case !change.priorExists:
		return t.restoreCreated(change)
	default:
		return t.restoreOverwritten(change)
	}
}

// restoreOverwritten swaps the pre-transaction content back over a destination
// this transaction replaced. The compare-and-swap is what refuses a path an
// external writer has since changed: its bytes come back out of the swap and go
// straight back in, and rollback reports the path as mixed rather than
// destroying that edit.
func (t *FileTxn) restoreOverwritten(change *txnChange) error {
	if change.priorSource != "" {
		return t.restoreStreamedOverwrite(change)
	}
	staged, err := t.stageOwnedTempFile(change.path, change.priorData, change.priorMode)
	if err != nil {
		return err
	}
	outcome, err := compareAndSwap(&t.hooks, staged, change.path, ExpectContent(change.data))
	if err != nil && !outcome.sourceConsumed {
		// The replacement was refused before it moved anything, so the staged
		// copy is redundant rather than a recovery artifact.
		_ = os.Remove(staged)
	}
	t.noteCleanupFailure(outcome.cleanup)
	return err
}

func (t *FileTxn) restoreStreamedOverwrite(change *txnChange) error {
	recovery, err := t.stagePreservedTempFileFrom(
		change.priorSource, change.priorSize, change.priorDigest, change.priorMode)
	if err != nil {
		t.preserveIntentCleanup(change.priorSource)
		if recovery == "" {
			recovery = change.priorSource
		}
		return &TxnRecoveryError{Path: change.path, RecoveryPath: recovery, Err: err}
	}
	outcome, err := compareAndSwap(&t.hooks, change.priorSource, change.path, ExpectHash(change.contentDigest()))
	if outcome.sourceConsumed {
		change.priorSource = ""
	}
	if err != nil {
		var stranded *TxnRecoveryError
		if errors.As(err, &stranded) && stranded.Foreign {
			// The first swap restored the old preimage at the destination but the
			// foreign bytes could not be swapped back. Their displaced path is now
			// the unique recovery artifact; keep intent cleanup away from it and
			// discard only the redundant duplicate of the old preimage.
			t.preserveIntentCleanup(stranded.RecoveryPath)
			return errors.Join(err, removeSuperseded(change.path, recovery))
		}
		return &TxnRecoveryError{
			Path: change.path, RecoveryPath: recovery,
			Displaced: IsTxnConflict(err), Err: err,
		}
	}
	if cleanupErr := removeSuperseded(change.path, recovery); cleanupErr != nil {
		return cleanupErr
	}
	t.noteCleanupFailure(outcome.cleanup)
	return nil
}

// restoreCreated removes a path this transaction created. Custody makes the
// delete a compare-and-swap; a file another writer put there instead is
// recognized from the bytes custody took and handed straight back.
func (t *FileTxn) restoreCreated(change *txnChange) error {
	custody, err := takeVerifiedCustody(&t.hooks, change.path, ExpectHash(change.contentDigest()))
	if err != nil {
		return err
	}
	t.noteCleanupFailure(removeSuperseded(change.path, custody))
	return nil
}

// restoreRemoved puts the deleted content back at a path the transaction
// vacated on purpose, refusing to replace anything that occupies it now. A
// failed placement preserves and names the temp copy because it is the durable
// pre-transaction version needed for manual recovery after Commit returns.
func (t *FileTxn) restoreRemoved(change *txnChange) error {
	if change.priorSource != "" {
		if err := placeNew(&t.hooks, change.priorSource, change.path); err != nil {
			t.preserveIntentCleanup(change.priorSource)
			return &TxnRecoveryError{
				Path: change.path, RecoveryPath: change.priorSource,
				Displaced: IsTxnConflict(err), Err: err,
			}
		}
		change.priorSource = ""
		return nil
	}
	staged, err := t.stagePreservedTempFile(change.priorData, change.priorMode)
	if err != nil {
		if staged != "" {
			return &TxnRecoveryError{Path: change.path, RecoveryPath: staged, Err: err}
		}
		return err
	}
	txnCrash(txnCrashRollbackPreserved)
	if err := placeNew(&t.hooks, staged, change.path); err != nil {
		return &TxnRecoveryError{
			Path:         change.path,
			RecoveryPath: staged,
			Displaced:    IsTxnConflict(err),
			Err:          err,
		}
	}
	return nil
}

func (t *FileTxn) preserveIntentCleanup(path string) {
	if t.intent == nil {
		return
	}
	kept := t.intent.cleanup[:0]
	for _, cleanup := range t.intent.cleanup {
		if cleanup != path {
			kept = append(kept, cleanup)
		}
	}
	t.intent.cleanup = kept
}

// takeVerifiedCustody moves path aside and requires the custody file to hold
// exactly what expect describes. A mismatch is an edit that landed after the
// last validation, so the custody file — which holds that writer's bytes — goes
// straight back and the path is reported as a conflict.
//
// Only a mutation whose end state is an absent path may call this: a delete, and
// the undo of a create. Custody vacates the destination, which is precisely what
// an overwrite must never do, so replacing a file goes through compareAndSwap
// instead.
// The custody file is judged by streaming it through a hasher rather than by
// reading it, so a delete or an undone create of a large attachment costs one
// pass over the file and not a copy of it in memory. The verdict is reported
// against path, not against the temp name custody happens to be holding it under.
func takeVerifiedCustody(hooks *txnHooks, path string, expect FileExpectation) (string, error) {
	custody, err := takeCustody(path)
	if err != nil {
		return "", err
	}
	if err := verifyFileDigest(custody, expect); err != nil {
		var conflict *TxnConflictError
		if errors.As(err, &conflict) {
			conflict.Path = path
		}
		return "", errors.Join(err, placeNew(hooks, custody, path))
	}
	return custody, nil
}

// takeCustody atomically moves path aside into a sibling temp file. Whatever
// another writer left there travels with it, so nothing can change the
// destination between the moment its content is judged and the moment it is
// discarded — after this rename the destination no longer exists, which is why
// only a deliberate deletion may use it.
func takeCustody(path string) (string, error) {
	dir := filepath.Dir(path)
	tmp, tmpPath, err := openExclusiveTemp(dir, ".txn-custody-", ".tmp", 0o600)
	if err != nil {
		return "", fmt.Errorf("vault: tempfile in %q: %w", dir, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("vault: close %q: %w", tmpPath, err)
	}
	if err := os.Rename(path, tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		if os.IsNotExist(err) {
			return "", &TxnConflictError{Path: path, Expected: "file", Actual: "missing"}
		}
		return "", fmt.Errorf("vault: rename %q -> %q: %w", path, tmpPath, err)
	}
	return tmpPath, nil
}

// linkExclusive creates path as a hard link to source and refuses if anything
// already occupies it. The hard link is the create-or-fail primitive the
// filesystem gives us, so a file that appeared at path survives; the caller
// decides what happens to source afterwards.
func linkExclusive(hooks *txnHooks, source, path string) error {
	link := os.Link
	if hooks != nil && hooks.hardLink != nil {
		link = hooks.hardLink
	}
	if err := link(source, path); err != nil {
		if os.IsExist(err) {
			return &TxnConflictError{Path: path, Expected: "missing", Actual: "file"}
		}
		return fmt.Errorf("vault: link %q -> %q: %w", source, path, err)
	}
	return nil
}

// placeNew moves source onto path and refuses if anything already exists there,
// leaving source on disk for recovery instead of dropping its content. It is the
// primitive for every mutation whose destination is supposed to be absent:
// creating a file, putting a deleted one back, and handing custody back to a
// writer that got there first.
//
// It is deliberately not the overwrite path. Replacing an existing file goes
// through the native path-preserving primitive, which refuses outright on a
// platform that cannot prove it; placement has no such requirement, because a
// path that holds nothing has no continuity to preserve, so it stays portable.
//
// Placement must not depend on the hard link alone. It is the same primitive
// whose failure creates the situation being restored, so on a filesystem
// without link support the put-back would fail for exactly the reason the write
// did and leave the destination absent with its only copy in a temp file. A
// non-conflict link failure therefore falls back to an atomic no-replace move,
// which needs no link support and cannot overwrite a writer racing for the
// vacant destination.
func placeNew(hooks *txnHooks, source, path string) error {
	if hooks != nil && hooks.placeNew != nil {
		return hooks.placeNew(source, path)
	}
	err := linkExclusive(hooks, source, path)
	if err == nil {
		return os.Remove(source)
	}
	var conflict *TxnConflictError
	if errors.As(err, &conflict) {
		conflict.Actual = "file (content kept at " + source + ")"
		return conflict
	}
	if hooks != nil && hooks.beforeFallbackPlacement != nil {
		if hookErr := hooks.beforeFallbackPlacement(source, path); hookErr != nil {
			return errors.Join(err, hookErr)
		}
	}
	if renameErr := renameNoReplace(source, path); renameErr != nil {
		if os.IsExist(renameErr) {
			return &TxnConflictError{
				Path:     path,
				Expected: "missing",
				Actual:   "file (content kept at " + source + ")",
			}
		}
		return errors.Join(err, fmt.Errorf("vault: rename no-replace %q -> %q: %w", source, path, renameErr))
	}
	return nil
}
