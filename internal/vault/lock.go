package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	// pathLockRelDir holds one lock file per note path. It lives under the existing
	// hidden .bnotes directory so Walk already skips it.
	pathLockRelDir = ".bnotes/locks"
	// pathLockTimeout bounds how long a writer waits for another bn writer. It is
	// generous relative to a note rewrite so ordinary contention always waits it
	// out, and short enough that a stale holder cannot hang a command.
	pathLockTimeout = 5 * time.Second
	// pathLockPollInterval paces the non-blocking retry loop.
	pathLockPollInterval = 5 * time.Millisecond
	// pathLockFDReserve keeps descriptors free for everything else the process
	// holds open while a transaction holds its locks: the notes it reads, the
	// journal it writes, and, under `bn serve`, every live connection.
	pathLockFDReserve = 128
	// pathLockGuardFDs counts the descriptors a per-path acquisition holds on top
	// of one per path: the shared vault guard. It is subtracted from the budget so
	// the threshold describes paths the process can actually add locks for.
	pathLockGuardFDs = 1
	// maxLockedPathsCeiling bounds how many paths are locked individually even on
	// a process with a huge descriptor budget: every one of them is held for the
	// whole commit. A larger set is not refused, it escalates to vaultLockFileName.
	maxLockedPathsCeiling = 4096
	// vaultLockFileName is the single guard every bn writer takes before it locks
	// anything else. Ordinary writers take it shared, so they still exclude each
	// other only per path; a set too large to hold one descriptor per path takes it
	// exclusively instead, which excludes every other bn writer using one
	// descriptor no matter how many paths the set covers.
	vaultLockFileName = "vault.lock"
)

// MaxLockedPaths reports how many paths one transaction locks individually
// before it escalates to the vault-wide guard. Per-path locks are held together
// for the whole commit, so the constraint is the process descriptor budget
// rather than a fixed number. It is not a limit on transaction size: a set
// larger than this commits under one exclusive vault lock instead.
func MaxLockedPaths() int {
	limit, ok := openFileLimit()
	if !ok {
		return maxLockedPathsCeiling
	}
	return clampLockedPaths(limit)
}

// clampLockedPaths turns a descriptor limit into the escalation threshold. It
// must never report more paths than the process can hold descriptors for: a
// threshold above the budget makes a set below it take one lock per path and
// fail acquisition on EMFILE, when escalating to the single exclusive guard
// would have committed it. So there is no floor — a process with a limit under
// the reserve gets a zero threshold and escalates every write, which costs
// concurrency and never correctness.
func clampLockedPaths(limit int) int {
	budget := limit - pathLockFDReserve - pathLockGuardFDs
	if budget < 0 {
		return 0
	}
	if budget > maxLockedPathsCeiling {
		return maxLockedPathsCeiling
	}
	return budget
}

// LockPaths takes one exclusive lock per path and returns the release function.
// It is how a writer that does not go through FileTxn joins the same mutual
// exclusion: replacing a note leaves its path transiently vacant, and a writer
// that ignores the locks can still take that vacancy and strand the other
// writer's content in a recovery file.
//
// Locks are taken in sorted path order, behind a shared lock on the vault guard,
// which is what makes concurrent multi-path callers deadlock-free. Release is
// idempotent and safe to defer.
//
// The locks are not re-entrant: every acquisition opens a fresh descriptor
// because flock ownership belongs to the open file description. A caller holding
// a lock on a path must not call into anything that locks the same path again,
// or it will wait out the full budget against itself. The guard extends that to
// a second case: a caller holding any lock must not nest a set large enough to
// escalate, since the exclusive guard that set needs is blocked by the shared
// guard the caller already holds.
func LockPaths(root string, paths ...string) (func(), error) {
	locks, err := acquirePathLocks(root, 0, 0, paths)
	if err != nil {
		return func() {}, err
	}
	return locks.release, nil
}

// TryLockPath takes an exclusive lock on lockPath without waiting, reporting
// acquired=false when another holder has it. It is the wait-free end of the same
// primitive LockPaths uses: one attempt, no polling, so a caller whose work is
// optional — refreshing a cache another process is already refreshing — can skip
// it instead of stalling for the wait budget.
//
// The caller names the lock file itself rather than passing a note path, because
// not every lock in a vault belongs to the sha256 note-path namespace
// lockFileName builds under pathLockRelDir. Missing parent directories are
// created. release is idempotent and safe to defer; it is non-nil even when the
// lock was not acquired, so `defer release()` needs no nil check.
//
// It lives here rather than in the calling package so the flock-not-fcntl
// invariant stays owned by vault: an fcntl record lock belongs to the process,
// so two goroutines inside one bn process would not contend and the skip would
// silently never happen.
func TryLockPath(lockPath string) (release func(), acquired bool, err error) {
	noop := func() {}
	if lockPath == "" {
		return noop, false, errors.New("vault: try lock: empty lock path")
	}
	// A lock file is an artifact like any other, so the location policy decides
	// before one can exist. Callers of this entry point treat every failure as
	// "skip the optional work", which is the right answer here too: a refusal
	// means the cache this lock guards is simply never written.
	if err := GuardMutation("", lockPath); err != nil {
		return noop, false, err
	}
	if dir := filepath.Dir(lockPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return noop, false, fmt.Errorf("vault: create lock directory %q: %w", dir, err)
		}
	}
	// A negative budget puts lockOne's deadline strictly in the past, so its loop
	// makes exactly one tryLock attempt and reports a miss as *TxnLockBusyError
	// without ever sleeping. Zero would leave the deadline equal to the instant the
	// loop starts, and whether that counts as expired depends on the clock's
	// resolution — a one-poll wait is not what a try-lock promises. Routing through
	// lockOne keeps descriptor handling identical: a fresh open file description per
	// acquisition, which is what makes one process contend with itself.
	file, err := lockOne(lockPath, lockPath, true, -1)
	if err != nil {
		if IsTxnLockBusy(err) {
			return noop, false, nil
		}
		return noop, false, err
	}
	locks := &pathLocks{files: []*os.File{file}}
	return locks.release, true, nil
}

// pathLocks is a set of held exclusive path locks.
type pathLocks struct {
	files []*os.File
}

// acquirePathLocks locks every path under root, in sorted order, behind the
// shared vault guard. A zero wait means pathLockTimeout and a zero escalateAbove
// means MaxLockedPaths(). On any failure the locks it already took are released,
// so the caller never has to unwind a partial acquisition.
//
// A set larger than escalateAbove takes the vault guard exclusively and no
// per-path locks at all, so a descriptor budget too small for even one path lock
// resolves to a zero threshold and escalates every write. That is what keeps a
// whole-list rewrite — a pin
// renormalize orders as many notes as the list holds — from being bounded by the
// process descriptor limit: the exclusive guard already excludes every other bn
// writer, so the per-path locks it would otherwise take are redundant.
//
// Escalation costs concurrency, not correctness, and only for the one oversized
// commit: every other bn writer in the vault waits on the guard until it
// finishes, and an escalating writer that never finds a gap between ordinary
// writers exhausts its wait and returns a retryable *TxnLockBusyError.
//
// Paths are resolved the same way FileTxn resolves its planned paths, so a
// caller passing a raw absolute path and a transaction passing an
// already-resolved one meet on the same lock file. Resolution is idempotent, so
// re-resolving a resolved path is a no-op.
//
// Every acquisition first finishes any transaction a previous process left
// prepared but unapplied. This is the "before mutation" half of the recovery
// protocol, and it belongs here rather than in FileTxn because taking a path lock
// is what every in-vault writer does before it touches the filesystem — routing
// recovery through the same chokepoint means a writer cannot skip it by not
// knowing about it. A recovery failure is returned rather than swallowed, so a
// vault holding an undecided half-applied transaction refuses further writes.
func acquirePathLocks(root string, wait time.Duration, escalateAbove int, paths []string) (*pathLocks, error) {
	// Recovery takes its own locks, so it has to run before this call takes any:
	// it goes through acquirePathLocksRaw, and nesting the two would deadlock a
	// writer against itself on a shared path.
	if err := recoverTxnIntents(root); err != nil {
		return nil, err
	}
	return acquirePathLocksRaw(root, wait, escalateAbove, paths)
}

// acquirePathLocksRaw is acquirePathLocks without the recovery pass. Only
// restart recovery itself calls it directly.
func acquirePathLocksRaw(root string, wait time.Duration, escalateAbove int, paths []string) (*pathLocks, error) {
	locks := &pathLocks{}
	// Every bn writer takes its locks before it stages, journals, or renames
	// anything, so this is the one place that makes the location policy an
	// invariant instead of a convention: a caller reaching the filesystem
	// through the lock is guarded whether it knows about the policy or not. It
	// runs before the escalation shortcut below, because a refusal must not
	// depend on how many paths the caller happened to plan.
	if err := GuardMutation(root, paths...); err != nil {
		return nil, err
	}
	if !lockingSupported || len(paths) == 0 {
		return locks, nil
	}
	if wait <= 0 {
		wait = pathLockTimeout
	}
	if escalateAbove <= 0 {
		escalateAbove = MaxLockedPaths()
	}
	// Lock names have to be relative to the resolved root, or two processes
	// reaching one vault by different symlinked names would hash different lock
	// files and exclude nobody.
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("vault: resolve vault root %q: %w", root, err)
	}
	resolved := make([]string, 0, len(paths))
	for _, path := range paths {
		abs, err := resolveVaultPath(root, path)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, abs)
	}
	// Duplicates would deadlock against themselves, so collapse them. They also
	// must not count toward escalation, or two names for one note would push a
	// small set over the budget.
	sort.Strings(resolved)
	resolved = dedupeSorted(resolved)

	dir := filepath.Join(realRoot, filepath.FromSlash(pathLockRelDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("vault: create lock directory %q: %w", dir, err)
	}

	exclusiveGuard := len(resolved) > escalateAbove
	guard, err := lockOne(filepath.Join(dir, vaultLockFileName), realRoot, exclusiveGuard, wait)
	if err != nil {
		return nil, err
	}
	locks.files = append(locks.files, guard)
	if exclusiveGuard {
		return locks, nil
	}

	for _, path := range resolved {
		file, err := lockOne(filepath.Join(dir, lockFileName(realRoot, path)), path, true, wait)
		if err != nil {
			locks.release()
			return nil, err
		}
		locks.files = append(locks.files, file)
	}
	return locks, nil
}

func dedupeSorted(sorted []string) []string {
	unique := sorted[:0]
	for i, path := range sorted {
		if i > 0 && path == sorted[i-1] {
			continue
		}
		unique = append(unique, path)
	}
	return unique
}

// lockOne blocks until it owns path's lock or the wait budget runs out. A timeout
// means another writer is still working, not that anything changed, so it is
// reported as a retryable *TxnLockBusyError.
func lockOne(name, path string, exclusive bool, wait time.Duration) (*os.File, error) {
	// A fresh descriptor per acquisition is required: flock locks belong to the
	// open file description, so a cached descriptor would make a process fail to
	// contend with itself.
	file, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("vault: open lock %q: %w", name, err)
	}
	deadline := time.Now().Add(wait)
	for {
		held, err := tryLock(file, exclusive)
		if err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("vault: lock %q: %w", path, err)
		}
		if held {
			return file, nil
		}
		if time.Now().After(deadline) {
			_ = file.Close()
			return nil, &TxnLockBusyError{Path: path}
		}
		time.Sleep(pathLockPollInterval)
	}
}

// release drops every held lock, newest first. Lock files are deliberately left
// on disk: unlinking one another process holds would let a third process create a
// new file at the same name and lock nothing.
func (l *pathLocks) release() {
	if l == nil {
		return
	}
	for i := len(l.files) - 1; i >= 0; i-- {
		file := l.files[i]
		_ = unlockFile(file)
		_ = file.Close()
	}
	l.files = nil
}

// lockFileName hashes the path so one flat directory can hold a lock for every
// note without mirroring the vault's folders or hitting name-length limits.
func lockFileName(root, path string) string {
	name := path
	if rel, err := filepath.Rel(root, path); err == nil {
		name = filepath.ToSlash(rel)
	}
	return HashBytes([]byte(name)) + ".lock"
}
