package vault

import (
	"errors"
	"os"
)

// replacementOutcome describes what a compare-and-swap did to the two names it
// was given, so the caller can update its bookkeeping without re-deriving the
// filesystem state from the error.
type replacementOutcome struct {
	// sourceConsumed reports that the source name no longer holds the staged
	// bytes, so the caller must stop treating it as a temp file it owns.
	sourceConsumed bool
	// landed reports that the destination now holds the source's bytes, so the
	// change counts as applied and has to be compensated if a later one fails.
	landed bool
	// cleanup is a *TxnCleanupError for a temp file that survived a mutation
	// which itself succeeded. It is never the reason a commit fails.
	cleanup error
}

// compareAndSwap overwrites destination with source and requires the content it
// displaces to be exactly preimage.
//
// The order is the whole point. A read-then-write would judge bytes that a save
// landing in the gap has already replaced, so the check would pass against a
// version that no longer exists. Swapping first and judging afterwards makes the
// bytes it inspects the exact bytes it displaced: there is no gap to lose. A
// stale preimage is therefore always a writer that ran after the last
// validation, and the answer is to swap that writer's bytes straight back and
// report a conflict — which leaves the destination holding their version and
// this transaction holding nothing, exactly as if it had never reached the path.
//
// destination never stops existing at any point in either direction. That is the
// property the whole file is built for: the vacancy an evacuate-then-place
// sequence produces is what a synchronizing file provider reads as a deletion.
//
// hooks may be nil; a caller outside a transaction — Note.Save — passes nil and
// gets the same semantics with no test seams.
func compareAndSwap(hooks *txnHooks, source, destination string, preimage FileExpectation) (replacementOutcome, error) {
	displaced, err := replaceVia(hooks, source, destination)
	if err != nil {
		// Nothing moved: the destination holds what it held and the staged
		// bytes are still at source.
		return replacementOutcome{}, err
	}
	landed := replacementOutcome{sourceConsumed: true, landed: true}

	if hooks != nil && hooks.afterExchange != nil {
		if hookErr := hooks.afterExchange(destination, displaced); hookErr != nil {
			return strandDisplaced(destination, displaced, hookErr)
		}
	}

	mismatch := judgeDisplaced(destination, displaced, preimage)
	if mismatch == nil {
		landed.cleanup = removeSuperseded(destination, displaced)
		return landed, nil
	}

	if hooks != nil && hooks.beforeConflictRestore != nil {
		if hookErr := hooks.beforeConflictRestore(destination); hookErr != nil {
			return strandDisplaced(destination, displaced, errors.Join(mismatch, hookErr))
		}
	}
	returned, restoreErr := replaceVia(hooks, displaced, destination)
	if restoreErr != nil {
		return strandDisplaced(destination, displaced, errors.Join(mismatch, restoreErr))
	}
	// returned holds this transaction's own superseded bytes now; the concurrent
	// writer's version is back where it belongs.
	rejected := replacementOutcome{sourceConsumed: true}
	rejected.cleanup = removeSuperseded(destination, returned)
	return rejected, mismatch
}

// judgeDisplaced decides whether the content the swap pushed out of destination
// is the content the caller expected to replace.
//
// An expectation that pins no hash needs no read: the primitive fails outright
// unless both names exist, so a successful swap is already proof the destination
// was there. Skipping the read is not just an optimization — a note saved with
// mode 0000 is deliberately supported, and reading its displaced copy back would
// fail with a permission error that has nothing to do with a concurrent writer.
func judgeDisplaced(destination, displaced string, preimage FileExpectation) error {
	if preimage.Exists && preimage.SHA256 == "" {
		return nil
	}
	err := verifyFileDigest(displaced, preimage)
	var conflict *TxnConflictError
	if errors.As(err, &conflict) {
		conflict.Path = destination
	}
	return err
}

// strandDisplaced reports the one mixed state a path-preserving replacement can
// still produce: the swap landed, the displaced content turned out to belong to
// another writer, and it could not be put back. The destination is intact and
// holds this transaction's bytes — nothing is missing from the vault — but the
// other writer's version now lives only in a temp file, so it is named.
func strandDisplaced(destination, displaced string, cause error) (replacementOutcome, error) {
	return replacementOutcome{sourceConsumed: true, landed: true}, &TxnRecoveryError{
		Path:         destination,
		RecoveryPath: displaced,
		Foreign:      true,
		Err:          cause,
	}
}

// removeSuperseded drops a temp file whose content nobody needs any more. A
// failure to unlink it is housekeeping, not a failed write, so it comes back as
// a *TxnCleanupError the commit reports separately instead of rolling back over.
func removeSuperseded(destination, temp string) error {
	if err := os.Remove(temp); err != nil {
		return &TxnCleanupError{Path: destination, LeftoverPath: temp, Err: err}
	}
	return nil
}

// replaceVia routes every overwrite through the one safe replacement layer, or
// through the test hook that stands in for it.
func replaceVia(hooks *txnHooks, source, destination string) (string, error) {
	if hooks != nil && hooks.replaceExisting != nil {
		return hooks.replaceExisting(source, destination)
	}
	return replaceExisting(source, destination)
}

// noteCleanupFailure records a temp file that survived a mutation which landed.
func (t *FileTxn) noteCleanupFailure(err error) {
	if err == nil {
		return
	}
	t.cleanupFailures = append(t.cleanupFailures, err)
}

// cleanupFailure returns the collected housekeeping failures, or nil.
func (t *FileTxn) cleanupFailure() error {
	if len(t.cleanupFailures) == 0 {
		return nil
	}
	return errors.Join(t.cleanupFailures...)
}
