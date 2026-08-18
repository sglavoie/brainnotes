package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
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

// Commit validates and stages every planned change, records one journal entry,
// then applies the changes in path order. Any failure after the first
// successful apply triggers a compensating rollback of the applied paths.
func (t *FileTxn) Commit(journaler TxnJournal) error {
	if t == nil || len(t.order) == 0 {
		return nil
	}
	if err := t.lockPaths(); err != nil {
		t.unlockPaths()
		return err
	}
	defer t.unlockPaths()
	if err := t.stage(); err != nil {
		t.cleanupStaged()
		return err
	}
	if journaler != nil {
		for _, path := range t.order {
			if err := journaler.Capture(path); err != nil {
				t.cleanupStaged()
				return err
			}
		}
	}

	applied := make([]*txnChange, 0, len(t.order))
	for _, path := range t.order {
		change := t.changes[path]
		err := t.apply(change)
		if change.applied {
			applied = append(applied, change)
		}
		if err != nil {
			t.cleanupStagedAfter(path)
			return t.rollback(change.path, err, applied)
		}
	}
	syncParentDirs(t.order)
	return nil
}

// lockPaths takes one exclusive lock per planned path and holds it for the whole
// commit, so two bn writers never run their replacements against one note at the
// same time.
//
// Without it the custody step is observable. Replacing a destination renames it
// aside first, and during that window the path does not exist: a second writer
// reads it as missing, plans a create, and wins the vacant path. Both writers
// then hold a legitimate version and the one in custody can only come back
// through *TxnRecoveryError for a person to merge by hand. Serializing the
// writers turns that into an ordinary staging conflict — the second one sees the
// first one's committed file and replans — which callers already retry.
//
// The locks are taken in sorted path order, which is what makes concurrent
// multi-path transactions deadlock-free.
//
// A transaction planning more paths than the descriptor budget allows takes the
// vault-wide guard exclusively instead, so transaction size is not bounded by
// how many descriptors the process may hold. The exclusion it provides is
// strictly stronger; what it costs is that every other bn writer waits.
//
// External writers are unaffected: an editor saving a note takes no lock, which
// is why custody stays in place to guard against it.
func (t *FileTxn) lockPaths() error {
	locks, err := acquirePathLocks(t.root, t.lockWait, t.lockEscalation, t.order)
	if err != nil {
		return err
	}
	t.locks = locks
	return nil
}

func (t *FileTxn) unlockPaths() {
	t.locks.release()
	t.locks = nil
}

// stage verifies every precondition and writes every replacement to a temp file
// beside its destination. Nothing observable changes until apply renames them,
// so a failure here leaves the vault untouched.
func (t *FileTxn) stage() error {
	if err := t.verifyRequirements(); err != nil {
		return err
	}
	for _, path := range t.order {
		change := t.changes[path]
		data, mode, exists, err := currentFileState(path)
		if err != nil {
			return err
		}
		if err := verifyExpectation(path, change.expect, data, exists); err != nil {
			return err
		}
		change.priorExists = exists
		change.priorData = data
		change.priorMode = mode

		if change.remove {
			continue
		}
		wantMode := change.mode
		if wantMode == 0 {
			if exists {
				wantMode = mode
			} else {
				wantMode, err = defaultNewFileMode(filepath.Dir(path))
				if err != nil {
					return err
				}
			}
		}
		staged, err := stageTempFile(path, change.data, wantMode)
		if err != nil {
			return err
		}
		change.staged = staged
		change.mode = wantMode
	}
	return nil
}

func (t *FileTxn) apply(change *txnChange) error {
	if err := t.verifyRequirements(); err != nil {
		return err
	}
	if err := t.verifyUnchangedSinceStage(change); err != nil {
		return err
	}
	if t.hooks.beforeCommit != nil {
		if err := t.hooks.beforeCommit(change.path); err != nil {
			return err
		}
	}
	if err := t.claim(change); err != nil {
		return err
	}
	return t.verifyRequirements()
}

// claim replaces or deletes the destination without a check-then-write window.
// The validation above is a plain read, so a save landing between it and the
// mutation would be overwritten by a check that already passed. Claim therefore
// renames the destination into a custody temp file first: after that rename the
// path no longer exists, so the bytes it judges are exactly the bytes it
// discards, and a mismatch — which can only come from a writer that ran after
// the validation — travels back to the destination untouched.
//
// The replacement is then placed with an exclusive create, so a file that
// appeared while the path was vacant is never clobbered either. A placement
// that fails leaves the destination vacant with its only copy in custody, so
// the custody file goes straight back before the failure is reported.
func (t *FileTxn) claim(change *txnChange) error {
	custody := ""
	if change.priorExists {
		taken, err := t.takeVerifiedCustody(change.path, ExpectContent(change.priorData))
		if err != nil {
			return err
		}
		custody = taken
	}
	if change.remove {
		change.applied = true
		if custody == "" {
			return nil
		}
		return os.Remove(custody)
	}
	if err := t.place(change.staged, change.path); err != nil {
		return t.recoverCustody(err, change.path, custody)
	}
	change.applied = true
	if err := os.Remove(change.staged); err != nil {
		return fmt.Errorf("vault: remove %q: %w", change.staged, err)
	}
	change.staged = ""
	if custody == "" {
		return nil
	}
	return os.Remove(custody)
}

func (t *FileTxn) place(source, path string) error {
	if t.hooks.placeReplacement != nil {
		return t.hooks.placeReplacement(source, path)
	}
	return t.linkExclusive(source, path)
}

// recoverCustody undoes a failed replacement placement. The destination is
// already in custody by then and the replacement never landed, so the path is
// vacant and the custody file holds the only copy of what was there. Putting
// that copy back is what keeps a placement failure from stranding the note in a
// hidden temp file while the caller sees nothing but the underlying error.
//
// The put-back is exclusive, so a writer that created the path while it was
// vacant keeps its file. Both versions then survive and the caller gets a
// *TxnRecoveryError naming the destination and the file the content is kept in.
// A put-back that fails for any other reason leaves the destination absent and
// is reported as such, since telling the caller another writer took the path
// would send them looking for a file that is not there.
func (t *FileTxn) recoverCustody(cause error, path, custody string) error {
	if custody == "" {
		return cause
	}
	err := t.placeExclusive(custody, path)
	if err == nil {
		return cause
	}
	return &TxnRecoveryError{
		Path:         path,
		RecoveryPath: custody,
		Displaced:    IsTxnConflict(err),
		Err:          errors.Join(cause, err),
	}
}

// verifyRequirements re-checks every read-only precondition. Staging is a long
// way from the last rename — journal snapshots are written and earlier paths are
// replaced in between — so the check runs again around each apply, keeping the
// whole commit tied to the dependency it was planned against.
//
// The check before a rename cannot cover the rename itself: a dependency
// replaced in that window would leave the change on disk under content that
// decided nothing about it. Running it again once the change is visible is what
// makes that window decidable — the mismatch is reported as a conflict and the
// applied path is compensated, so the change does not remain committed.
func (t *FileTxn) verifyRequirements() error {
	for _, requirement := range t.requires {
		if err := verifyCurrentState(requirement.path, requirement.expect); err != nil {
			return err
		}
	}
	return nil
}

// verifyUnchangedSinceStage re-reads the destination immediately before it is
// replaced and compares it to the state staging observed. Staging can be a long
// way from the mutation — journal snapshots are written in between — so this
// second check refuses the common concurrent-edit case without moving any file.
// It is a pre-check only: claim re-judges the same expectation once it owns the
// destination, which is what covers the window this check cannot.
func (t *FileTxn) verifyUnchangedSinceStage(change *txnChange) error {
	expect := ExpectMissing()
	if change.priorExists {
		expect = ExpectHash(HashBytes(change.priorData))
	}
	return verifyCurrentState(change.path, expect)
}

// verifyStillCommitted requires the destination to still hold exactly what this
// transaction applied to it. Rollback runs after the path has been observable
// to other writers, so without this check compensation would overwrite an edit
// the transaction never saw.
func (t *FileTxn) verifyStillCommitted(change *txnChange) error {
	expect := ExpectMissing()
	if !change.remove {
		expect = ExpectContent(change.data)
	}
	return verifyCurrentState(change.path, expect)
}

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
// Rollback therefore takes the destination into custody first and judges the
// custody file, so the bytes it inspects are the bytes it discards, and puts
// every replacement in place with an exclusive create, so a file that appeared
// while the path was in custody is never clobbered either.
func (t *FileTxn) restore(change *txnChange) error {
	if err := t.verifyStillCommitted(change); err != nil {
		return err
	}
	if t.hooks.beforeRestore != nil {
		if err := t.hooks.beforeRestore(change.path); err != nil {
			return err
		}
	}
	if change.remove {
		if !change.priorExists {
			return nil
		}
		return t.restorePriorBytes(change)
	}

	custody, err := t.takeVerifiedCustody(change.path, ExpectContent(change.data))
	if err != nil {
		return err
	}
	if !change.priorExists {
		return os.Remove(custody)
	}
	if err := t.restorePriorBytes(change); err != nil {
		var recovery *TxnRecoveryError
		if errors.As(err, &recovery) && recovery.Displaced {
			// The destination now belongs to another writer. The custody file
			// contains only this transaction's superseded bytes; the separately
			// named recovery file contains the pre-transaction content that must
			// survive for manual recovery.
			return errors.Join(err, os.Remove(custody))
		}
		return errors.Join(err, t.placeExclusive(custody, change.path))
	}
	return os.Remove(custody)
}

// restorePriorBytes puts the staged prior content at a path the transaction has
// already vacated, refusing to replace anything that occupies it. A failed
// placement preserves and names the temp copy because it is the durable
// pre-transaction version needed for manual recovery after Commit returns.
func (t *FileTxn) restorePriorBytes(change *txnChange) error {
	staged, err := stageTempFile(change.path, change.priorData, change.priorMode)
	if err != nil {
		return err
	}
	if err := t.placeExclusive(staged, change.path); err != nil {
		return &TxnRecoveryError{
			Path:         change.path,
			RecoveryPath: staged,
			Displaced:    IsTxnConflict(err),
			Err:          err,
		}
	}
	return nil
}

// takeVerifiedCustody moves path aside and requires the custody file to hold
// exactly what expect describes. A mismatch is an edit that landed after the
// last validation, so the custody file — which holds that writer's bytes — goes
// straight back and the path is reported as a conflict.
func (t *FileTxn) takeVerifiedCustody(path string, expect FileExpectation) (string, error) {
	custody, err := takeCustody(path)
	if err != nil {
		return "", err
	}
	current, err := os.ReadFile(custody)
	if err != nil {
		readErr := fmt.Errorf("vault: read %q: %w", custody, err)
		return "", errors.Join(readErr, t.placeExclusive(custody, path))
	}
	if err := verifyExpectation(path, expect, current, true); err != nil {
		return "", errors.Join(err, t.placeExclusive(custody, path))
	}
	return custody, nil
}

// takeCustody atomically moves path aside into a sibling temp file. Whatever
// another writer left there travels with it, so nothing can change the
// destination between the moment its content is judged and the moment it is
// discarded — after this rename the destination no longer exists.
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
func (t *FileTxn) linkExclusive(source, path string) error {
	link := os.Link
	if t != nil && t.hooks.hardLink != nil {
		link = t.hooks.hardLink
	}
	if err := link(source, path); err != nil {
		if os.IsExist(err) {
			return &TxnConflictError{Path: path, Expected: "missing", Actual: "file"}
		}
		return fmt.Errorf("vault: link %q -> %q: %w", source, path, err)
	}
	return nil
}

// placeExclusive moves source onto path and refuses if anything already exists
// there, leaving source on disk for recovery instead of dropping its content.
// Every restoration goes through it: putting a displaced destination back after
// a failed placement, and replaying prior bytes during rollback.
//
// Restoration must not depend on the hard link alone. It is the same primitive
// whose failure creates the situation being restored, so on a filesystem
// without link support the put-back would fail for exactly the reason the write
// did and leave the destination absent with its only copy in a temp file. A
// non-conflict link failure therefore falls back to an atomic no-replace move,
// which needs no link support and cannot overwrite a writer racing for the
// vacant destination.
func (t *FileTxn) placeExclusive(source, path string) error {
	err := t.linkExclusive(source, path)
	if err == nil {
		return os.Remove(source)
	}
	var conflict *TxnConflictError
	if errors.As(err, &conflict) {
		conflict.Actual = "file (content kept at " + source + ")"
		return conflict
	}
	if t != nil && t.hooks.beforeFallbackPlacement != nil {
		if hookErr := t.hooks.beforeFallbackPlacement(source, path); hookErr != nil {
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

func (t *FileTxn) cleanupStaged() {
	for _, path := range t.order {
		if change := t.changes[path]; change != nil && change.staged != "" {
			_ = os.Remove(change.staged)
			change.staged = ""
		}
	}
}

// cleanupStagedAfter drops the temp files for paths at or after the failed one;
// the earlier paths are already applied and own no temp file.
func (t *FileTxn) cleanupStagedAfter(failedPath string) {
	seen := false
	for _, path := range t.order {
		if path == failedPath {
			seen = true
		}
		if !seen {
			continue
		}
		if change := t.changes[path]; change != nil && change.staged != "" {
			_ = os.Remove(change.staged)
			change.staged = ""
		}
	}
}

func currentFileState(path string) (data []byte, mode os.FileMode, exists bool, err error) {
	info, statErr := os.Lstat(path)
	switch {
	case statErr == nil:
	case os.IsNotExist(statErr):
		return nil, 0, false, nil
	default:
		return nil, 0, false, fmt.Errorf("vault: stat %q: %w", path, statErr)
	}
	if !info.Mode().IsRegular() {
		return nil, 0, false, &TxnConflictError{
			Path:     path,
			Expected: "file",
			Actual:   nonRegularLabel(info),
		}
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		return nil, 0, false, classifyStateReadError(path, readErr)
	}
	return data, info.Mode().Perm(), true, nil
}

// classifyStateReadError turns the read half of currentFileState into the right
// kind of failure. The stat said "regular file", so a read that now reports the
// path missing means another writer took it in between — takeCustody renames the
// destination aside, leaving exactly that window. That is a precondition
// mismatch, not an I/O fault, and every caller that retries a conflict must see
// it as one.
func classifyStateReadError(path string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return &TxnConflictError{Path: path, Expected: "file", Actual: "missing"}
	}
	return fmt.Errorf("vault: read %q: %w", path, err)
}

func nonRegularLabel(info os.FileInfo) string {
	switch {
	case info.IsDir():
		return "directory"
	case info.Mode()&os.ModeSymlink != 0:
		return "symlink"
	default:
		return "irregular"
	}
}

func verifyCurrentState(path string, expect FileExpectation) error {
	data, _, exists, err := currentFileState(path)
	if err != nil {
		return err
	}
	return verifyExpectation(path, expect, data, exists)
}

func verifyExpectation(path string, expect FileExpectation, data []byte, exists bool) error {
	actual := "missing"
	if exists {
		actual = "file(sha256=" + HashBytes(data) + ")"
	}
	if expect.Exists != exists {
		return &TxnConflictError{Path: path, Expected: expect.describe(), Actual: actual}
	}
	if expect.Exists && expect.SHA256 != "" && expect.SHA256 != HashBytes(data) {
		return &TxnConflictError{Path: path, Expected: expect.describe(), Actual: actual}
	}
	return nil
}

// stageTempFile writes data to a sibling temp file with the final mode already
// applied, so committing it is a single rename.
func stageTempFile(dest string, data []byte, mode os.FileMode) (string, error) {
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("vault: mkdir %q: %w", dir, err)
	}
	tmp, tmpPath, err := openExclusiveTemp(dir, ".txn-", ".tmp", 0o600)
	if err != nil {
		return "", fmt.Errorf("vault: tempfile in %q: %w", dir, err)
	}
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return "", fmt.Errorf("vault: write %q: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return "", fmt.Errorf("vault: fsync %q: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", fmt.Errorf("vault: close %q: %w", tmpPath, err)
	}
	if mode == 0 {
		mode = 0o644
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		cleanup()
		return "", fmt.Errorf("vault: chmod %q: %w", tmpPath, err)
	}
	return tmpPath, nil
}

func syncParentDirs(paths []string) {
	synced := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		dir := filepath.Dir(path)
		if _, ok := synced[dir]; ok {
			continue
		}
		synced[dir] = struct{}{}
		if f, err := os.Open(dir); err == nil {
			_ = f.Sync()
			_ = f.Close()
		}
	}
}
