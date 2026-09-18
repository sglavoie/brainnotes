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
	// placeNew stands in for the exclusive placement that puts a staged file at
	// a destination that must not already exist. The filesystem will not fail
	// that call on demand, so this is how the failure paths after it stay
	// covered by tests.
	placeNew func(source, path string) error
	// hardLink stands in for os.Link, the primitive exclusive placement uses and
	// falls back from. Failing it reproduces a vault on a filesystem without
	// link support, which is the failure that has to leave the destination
	// restored rather than stranded in custody.
	hardLink func(source, path string) error
	// replaceExisting stands in for the native path-preserving replacement. The
	// kernel will not fail a swap on demand, so this is how the refusal that
	// must leave the destination untouched stays covered by tests.
	replaceExisting func(source, path string) (string, error)
	// afterExchange runs between a successful exchange and the compare-and-swap
	// judgement of the displaced copy. It exists so the restoration path — which
	// only runs when a writer lands inside a single syscall — is reachable.
	afterExchange func(destination, displaced string) error
	// beforeConflictRestore runs after a stale preimage is detected and before
	// the concurrent writer's bytes are swapped back, so the stranded-version
	// case stays covered.
	beforeConflictRestore func(path string) error
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

	// source names a file holding the replacement bytes, for a change planned
	// through WriteFrom rather than Write. When it is set, data is always nil and
	// digest and size describe the content instead; see txn_stream.go.
	source string
	// digest and size describe the content a source-backed change writes. They
	// are what the recovery intent records and what every later comparison pins,
	// standing in for the hash Write would have taken of its slice.
	digest string
	size   int64

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

// contentDigest and contentSize describe the bytes the change writes. A
// source-backed change never held them, so it answers from what it verified
// while staging rather than from a slice it does not have.
func (c *txnChange) contentDigest() string {
	if c.source != "" {
		return c.digest
	}
	return HashBytes(c.data)
}

func (c *txnChange) contentSize() int64 {
	if c.source != "" {
		return c.size
	}
	return int64(len(c.data))
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
	// cleanupFailures collects temp files that survived mutations which landed.
	// They are reported after the commit rather than rolled back, because the
	// vault already holds what the caller asked for.
	cleanupFailures []error
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
