package vault

import (
	"errors"
	"fmt"
	"strings"
)

// TxnRollbackError reports a commit failure whose compensating rollback also
// failed, so the vault is left in a mixed state and needs manual recovery.
type TxnRollbackError struct {
	// Path is the file whose commit step failed.
	Path string
	// Err is the original commit failure.
	Err error
	// Restored lists the paths successfully rolled back to their prior state.
	Restored []string
	// Failed lists the paths whose rollback failed or was refused because the
	// destination no longer held this transaction's bytes, in commit order.
	Failed []string
	// RollbackErrs holds one error per entry in Failed.
	RollbackErrs []error
}

func (e *TxnRollbackError) Error() string {
	if e == nil {
		return "vault: transaction rollback failed"
	}
	parts := make([]string, 0, len(e.RollbackErrs))
	for i, err := range e.RollbackErrs {
		path := ""
		if i < len(e.Failed) {
			path = e.Failed[i]
		}
		parts = append(parts, fmt.Sprintf("%s: %v", path, err))
	}
	return fmt.Sprintf(
		"vault: commit %s failed (%v) and rollback failed for %s; inspect 'git diff' to recover",
		e.Path, e.Err, strings.Join(parts, "; "))
}

func (e *TxnRollbackError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsTxnRollbackFailure reports whether err is a failed compensating rollback.
func IsTxnRollbackFailure(err error) bool {
	var rollback *TxnRollbackError
	return errors.As(err, &rollback)
}

// TxnRecoveryError reports content the transaction moved out of a path and
// could not put back, so two legitimate versions exist and only a person can
// decide which one wins. RecoveryPath always names the file that content is
// preserved in.
//
// Overwriting no longer produces this: a replacement is a path-preserving
// exchange, so a failed overwrite leaves the destination exactly as it was. What
// remains are the three cases where a path genuinely has to be vacated or
// re-occupied — undoing a create, undoing a delete, and putting a concurrent
// writer's bytes back after a compare-and-swap rejected them.
type TxnRecoveryError struct {
	// Path is the destination whose content could not be put back.
	Path string
	// RecoveryPath is the transaction temp file holding that content.
	RecoveryPath string
	// Displaced reports that another writer took the path, so the destination
	// holds that writer's file and both versions survive. When it is false and
	// Foreign is false, the put-back failed for some other reason and the
	// destination is still absent.
	Displaced bool
	// Foreign reports that the preserved content belongs to a writer outside
	// this transaction: a compare-and-swap found the destination had changed
	// under it, and putting that writer's bytes back failed, so the destination
	// carries this transaction's bytes instead.
	Foreign bool
	// Err is the mutation failure together with the failed put-back.
	Err error
}

func (e *TxnRecoveryError) Error() string {
	if e == nil {
		return "vault: transaction recovery needed"
	}
	if e.Foreign {
		return fmt.Sprintf(
			"vault: %s was changed by another writer while this transaction replaced it (%v) and that change could not be put back, so %s now holds this transaction's version; the other version is preserved at %s, merge it back to recover",
			e.Path, e.Err, e.Path, e.RecoveryPath)
	}
	if e.Displaced {
		return fmt.Sprintf(
			"vault: writing %s failed (%v) and its previous content could not be put back because another writer took the path; that content is preserved at %s, move it back into place to recover",
			e.Path, e.Err, e.RecoveryPath)
	}
	return fmt.Sprintf(
		"vault: writing %s failed (%v) and its previous content could not be put back, so %s is missing from the vault; that content is preserved at %s, move it back into place to recover",
		e.Path, e.Err, e.Path, e.RecoveryPath)
}

func (e *TxnRecoveryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsTxnRecoveryNeeded reports whether err left displaced content in a
// transaction temp file that a person has to move back by hand.
func IsTxnRecoveryNeeded(err error) bool {
	var recovery *TxnRecoveryError
	return errors.As(err, &recovery)
}

// TxnCleanupError reports a temp file that survived a mutation which itself
// landed. It is deliberately not a mutation failure: the destination holds
// exactly what the caller asked for, and rolling a committed exchange back
// because a scratch file could not be unlinked would turn a housekeeping problem
// into data loss. Callers that treat any error as a failed write should check
// IsTxnCleanupFailure first; the only action this ever asks for is deleting the
// named file.
type TxnCleanupError struct {
	// Path is the destination whose mutation landed.
	Path string
	// LeftoverPath is the transaction temp file that could not be removed. It
	// holds the destination's superseded content, so it is safe to delete.
	LeftoverPath string
	// Err is the removal failure.
	Err error
}

func (e *TxnCleanupError) Error() string {
	if e == nil {
		return "vault: transaction cleanup failed"
	}
	return fmt.Sprintf(
		"vault: %s was written as requested but its superseded copy could not be removed (%v); delete %s when convenient",
		e.Path, e.Err, e.LeftoverPath)
}

func (e *TxnCleanupError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsTxnCleanupFailure reports whether err is a leftover temp file after a
// mutation that landed. The vault holds the requested content either way.
func IsTxnCleanupFailure(err error) bool {
	var cleanup *TxnCleanupError
	return errors.As(err, &cleanup)
}

// TxnLockBusyError reports that another writer held a planned path for longer
// than the transaction was willing to wait. It is not a conflict: nothing was
// read, staged, or written, and the vault is exactly as it was, so the operation
// can be retried as-is.
type TxnLockBusyError struct {
	Path string
}

func (e *TxnLockBusyError) Error() string {
	if e == nil {
		return "vault: path is locked by another writer"
	}
	return fmt.Sprintf("vault: %s is being written by another process; retry", e.Path)
}

// IsTxnLockBusy reports whether err is a lock wait that timed out.
func IsTxnLockBusy(err error) bool {
	var busy *TxnLockBusyError
	return errors.As(err, &busy)
}
