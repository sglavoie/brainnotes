package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FileExpectation is the precondition a transaction verifies for one path
// immediately before it stages the replacement. It is what turns a concurrent
// external edit into a conflict instead of a silent overwrite.
type FileExpectation struct {
	// Exists requires the path to be an existing regular file.
	Exists bool
	// SHA256 is the hex content hash the existing file must have. Empty means
	// the content is not checked, only existence.
	SHA256 string
}

// ExpectMissing requires the path to be absent.
func ExpectMissing() FileExpectation { return FileExpectation{} }

// ExpectAnyContent requires the path to exist without pinning its content.
func ExpectAnyContent() FileExpectation { return FileExpectation{Exists: true} }

// ExpectHash requires the path to exist with the given hex content hash. An
// empty hash degrades to ExpectAnyContent.
func ExpectHash(sha256Hex string) FileExpectation {
	return FileExpectation{Exists: true, SHA256: strings.ToLower(strings.TrimSpace(sha256Hex))}
}

// ExpectContent requires the path to exist holding exactly data.
func ExpectContent(data []byte) FileExpectation {
	return FileExpectation{Exists: true, SHA256: HashBytes(data)}
}

// HashBytes returns the lowercase hex SHA-256 of data.
func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (e FileExpectation) describe() string {
	if !e.Exists {
		return "missing"
	}
	if e.SHA256 == "" {
		return "file"
	}
	return "file(sha256=" + e.SHA256 + ")"
}

// TxnConflictError reports a path whose on-disk state no longer matches the
// state the transaction was planned against.
type TxnConflictError struct {
	Path     string
	Expected string
	Actual   string
}

func (e *TxnConflictError) Error() string {
	if e == nil {
		return "vault: transaction conflict"
	}
	return fmt.Sprintf("vault: %s changed on disk: expected=%s actual=%s", e.Path, e.Expected, e.Actual)
}

// IsTxnConflict reports whether err is a transaction precondition failure.
func IsTxnConflict(err error) bool {
	var conflict *TxnConflictError
	return errors.As(err, &conflict)
}

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

// TxnRecoveryError reports content the transaction displaced and could not put
// back. Replacing a destination moves it into a temp file first, so a placement
// failure leaves the path vacant with the only copy of its previous content in
// that file; the transaction puts the copy back, and this error is what it
// returns when the put-back does not land. RecoveryPath always names the file
// that content is preserved in.
type TxnRecoveryError struct {
	// Path is the destination whose previous content could not be put back.
	Path string
	// RecoveryPath is the transaction temp file holding that content.
	RecoveryPath string
	// Displaced reports that another writer took the vacant path, so the
	// destination holds that writer's file and both versions survive. When it is
	// false the put-back failed for some other reason and the destination is
	// still absent.
	Displaced bool
	// Err is the placement failure together with the failed put-back.
	Err error
}

func (e *TxnRecoveryError) Error() string {
	if e == nil {
		return "vault: transaction recovery needed"
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

// TxnJournal captures the pre-commit state of every path a transaction touches.
// Commit calls Capture once per path after staging succeeds and before any file
// on disk is replaced, so one action yields one journal entry.
type TxnJournal interface {
	Capture(path string) error
}

// txnHooks injects deterministic failures into commit and rollback. It exists
// so the rollback error paths — which cannot be provoked reliably through the
// filesystem — stay covered by tests.
type txnHooks struct {
	beforeCommit            func(path string) error
	beforeRestore           func(path string) error
	beforeFallbackPlacement func(source, path string) error
	// placeReplacement stands in for the exclusive create that puts a staged
	// replacement at its destination. The filesystem will not fail that call on
	// demand, so this is how the recovery path after a placement failure stays
	// covered by tests.
	placeReplacement func(source, path string) error
	// hardLink stands in for os.Link, the primitive replacement placement uses
	// and restoration falls back from. Failing it reproduces a vault on a
	// filesystem without link support, which is the failure that has to leave
	// the destination restored rather than stranded in custody.
	hardLink func(source, path string) error
}

// txnRequirement is a read-only precondition: a file whose content decides what
// the transaction writes, but which the transaction never changes.
type txnRequirement struct {
	path   string
	expect FileExpectation
}

type txnChange struct {
	path   string
	remove bool
	data   []byte
	mode   os.FileMode
	expect FileExpectation

	// staged is the temp file holding the replacement bytes, empty for removals.
	staged string
	// applied records that the destination already carries this change, so a
	// failure discovered after the rename still rolls the path back.
	applied bool
	// priorExists, priorData, and priorMode capture what the path held when the
	// change was staged; they are the compensating rollback's source of truth.
	priorExists bool
	priorData   []byte
	priorMode   os.FileMode
}

// FileTxn plans a set of in-vault file changes, validates every precondition
// and stages every replacement before touching a single destination, then
// commits them all. A failure during commit rolls the already-committed paths
// back to the state they held when the transaction was staged.
//
// Atomicity is per-action, not per-process: a rename cannot be un-renamed by
// the kernel, so rollback is compensating rather than transactional. A path
// that another writer changed after this transaction applied it is never
// compensated, since replaying the prior bytes would destroy that edit. When a
// compensating write fails or is refused that way, Commit returns a
// *TxnRollbackError naming exactly which paths were left mixed.
type FileTxn struct {
	root     string
	changes  map[string]*txnChange
	order    []string
	requires []txnRequirement
	hooks    txnHooks
	locks    *pathLocks
	// lockWait overrides pathLockTimeout when non-zero. Like hooks, it exists so a
	// test can reach the lock-timeout path without a multi-second wait.
	lockWait time.Duration
	// lockEscalation overrides MaxLockedPaths when non-zero, so a test can reach
	// the escalated vault-wide lock without planning thousands of paths.
	lockEscalation int
}

// NewFileTxn creates a transaction rooted at an existing vault directory. Every
// planned path must resolve inside that root.
func NewFileTxn(root string) (*FileTxn, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("vault: transaction needs a root")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("vault: transaction root %q: %w", root, err)
	}
	return &FileTxn{root: abs, changes: make(map[string]*txnChange)}, nil
}

// NewFileTxn creates a transaction rooted at the vault.
func (v *Vault) NewFileTxn() (*FileTxn, error) {
	if v == nil {
		return nil, errors.New("vault: nil receiver")
	}
	return NewFileTxn(v.Root)
}

// Write plans replacing path with data. A zero mode preserves the destination's
// current mode, or derives a new file's mode from the process umask.
func (t *FileTxn) Write(path string, data []byte, mode os.FileMode, expect FileExpectation) error {
	return t.plan(&txnChange{path: path, data: data, mode: mode, expect: expect})
}

// Remove plans deleting path.
func (t *FileTxn) Remove(path string, expect FileExpectation) error {
	return t.plan(&txnChange{path: path, remove: true, expect: expect})
}

// Require pins a file the transaction depends on but never writes, such as the
// configuration whose declarations decided the planned bytes. The precondition
// is verified when the transaction stages and again immediately before and
// after each change is applied, so a dependency that changes mid-commit stops
// the transaction and the changes it already made are rolled back instead of
// being left behind under declarations that never governed them. A required
// path is never staged, journaled, or rolled back.
func (t *FileTxn) Require(path string, expect FileExpectation) error {
	if t == nil {
		return errors.New("vault: nil transaction")
	}
	abs, err := t.resolve(path)
	if err != nil {
		return err
	}
	if _, planned := t.changes[abs]; planned {
		return fmt.Errorf("vault: transaction already plans a change for %q", abs)
	}
	t.requires = append(t.requires, txnRequirement{path: abs, expect: expect})
	return nil
}

// Len reports how many paths the transaction will change.
func (t *FileTxn) Len() int {
	if t == nil {
		return 0
	}
	return len(t.order)
}

// Paths returns the planned paths in commit order.
func (t *FileTxn) Paths() []string {
	if t == nil {
		return nil
	}
	return append([]string(nil), t.order...)
}

// SetLockEscalationThresholdForTest overrides the number of paths FileTxn
// locks individually before taking the vault-wide guard. Production callers
// must leave the threshold unset so it continues to derive from
// MaxLockedPaths; this seam lets cross-package tests cover escalation without
// creating thousands of files. The override can lower, but never raise, the
// production-derived threshold.
func (t *FileTxn) SetLockEscalationThresholdForTest(threshold int) {
	if t == nil || threshold <= 0 {
		return
	}
	if maximum := MaxLockedPaths(); threshold > maximum {
		threshold = maximum
	}
	t.lockEscalation = threshold
}

func (t *FileTxn) plan(change *txnChange) error {
	if t == nil {
		return errors.New("vault: nil transaction")
	}
	abs, err := t.resolve(change.path)
	if err != nil {
		return err
	}
	change.path = abs
	if _, exists := t.changes[abs]; exists {
		return fmt.Errorf("vault: transaction already plans a change for %q", abs)
	}
	for _, requirement := range t.requires {
		if requirement.path == abs {
			return fmt.Errorf("vault: transaction already requires %q to stay unchanged", abs)
		}
	}
	t.changes[abs] = change
	t.order = append(t.order, abs)
	sort.Strings(t.order)
	return nil
}

func (t *FileTxn) resolve(path string) (string, error) {
	return resolveVaultPath(t.root, path)
}

// resolveVaultPath turns path into its fully symlink-resolved absolute form and
// verifies it lands inside root. It is idempotent, so resolving an already
// resolved path returns it unchanged — which is what lets path locking normalize
// transaction paths and raw caller paths to the same name.
func resolveVaultPath(root, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("vault: path is empty")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("vault: resolve vault root %q: %w", root, err)
	}
	realDir, err := resolveDirForWrite(filepath.Dir(path))
	if err != nil {
		return "", fmt.Errorf("vault: resolve %q: %w", filepath.Dir(path), err)
	}
	resolved := filepath.Join(realDir, filepath.Base(path))
	rel, err := filepath.Rel(realRoot, resolved)
	if err != nil {
		return "", fmt.Errorf("vault: rel %q: %w", resolved, err)
	}
	if rel == "." || relEscapesRoot(rel) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("vault: %q resolves outside vault root %q", path, root)
	}
	return resolved, nil
}
