package vault

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

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
	lock, acquired, err := TryLockPathHandle(lockPath)
	if err != nil || !acquired {
		return func() {}, acquired, err
	}
	return func() { _ = lock.Release() }, true, nil
}

// FileLock is a held named flock. File exposes the descriptor only for passing
// the same open file description across exec; ordinary callers should retain
// the handle and call Release.
type FileLock struct {
	file *os.File
	once sync.Once
	err  error
	// handedOut records that File exposed the descriptor, so a child may share
	// this open file description and its flock.
	handedOut atomic.Bool
}

func (l *FileLock) File() *os.File {
	if l == nil {
		return nil
	}
	l.handedOut.Store(true)
	return l.file
}

func (l *FileLock) Release() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		// Closing, rather than explicitly unlocking, is load-bearing for exec
		// inheritance: a duplicated descriptor in the child keeps the same flock
		// until the last process closes it. LOCK_UN here would drop that shared
		// open-file-description lock out from under a still-running descendant.
		//
		// A lock whose descriptor was never handed out is unlocked first. Any
		// other goroutine's fork briefly duplicates every descriptor into a
		// child that has not exec'd yet; close-on-exec drops the copy only at
		// exec, so Close alone would leave the flock held by that child for the
		// window and the holder's next single-try acquisition would read busy.
		if !l.handedOut.Load() {
			_ = unlockFile(l.file)
		}
		l.err = l.file.Close()
	})
	return l.err
}

// TryLockPathHandle is TryLockPath with a descriptor-bearing result for the
// checkpoint client that must let an exec child inherit lock ownership.
func TryLockPathHandle(lockPath string) (lock *FileLock, acquired bool, err error) {
	if lockPath == "" {
		return nil, false, errors.New("vault: try lock: empty lock path")
	}
	// A lock file is an artifact like any other, so the location policy decides
	// before one can exist. Callers of this entry point treat every failure as
	// "skip the optional work", which is the right answer here too: a refusal
	// means the cache this lock guards is simply never written.
	if err := GuardMutation("", lockPath); err != nil {
		return nil, false, err
	}
	if dir := filepath.Dir(lockPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, false, fmt.Errorf("vault: create lock directory %q: %w", dir, err)
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
			return nil, false, nil
		}
		return nil, false, err
	}
	return &FileLock{file: file}, true, nil
}

// LockFile takes lockPath's flock, shared or exclusive, within wait, and returns
// the release function.
//
// It is the named-lock end of the same primitive LockPaths and TryLockPath use,
// for a lock that is not a note-path lock and whose holder needs to wait rather
// than skip. A shared mode is what makes it more than TryLockPath with a
// deadline: a barrier that many writers hold together and one coordinator holds
// alone cannot be expressed with exclusive locks at all.
//
// Missing parent directories are created. A wait that runs out is reported as a
// retryable *TxnLockBusyError, exactly as a contended path lock is. release is
// idempotent and safe to defer.
//
// A zero wait means the default budget; a negative one means a single attempt,
// which is what a caller with a deadline already behind it wants to pass rather
// than silently inheriting a fresh five seconds.
//
// It lives here for the reason TryLockPath does: flock-not-fcntl is this
// package's invariant, and an fcntl record lock belongs to the process, so two
// goroutines inside one bn process would not contend and a barrier built on it
// would exclude nothing.
func LockFile(lockPath string, exclusive bool, wait time.Duration) (func(), error) {
	return LockFileContext(context.Background(), lockPath, exclusive, wait)
}

// LockFileContext is LockFile that also stops waiting when ctx is done. A
// cancelled wait returns ctx's error wrapped, never *TxnLockBusyError: the lock
// was not proven busy for the whole budget, the caller stopped asking. Nothing
// is held on any error return.
func LockFileContext(ctx context.Context, lockPath string, exclusive bool, wait time.Duration) (func(), error) {
	lock, err := LockFileHandleContext(ctx, lockPath, exclusive, wait)
	if err != nil {
		return func() {}, err
	}
	return func() { _ = lock.Release() }, nil
}

// LockFileHandle is LockFile with access to the held descriptor for exec
// inheritance. It otherwise has identical acquisition semantics.
func LockFileHandle(lockPath string, exclusive bool, wait time.Duration) (*FileLock, error) {
	return LockFileHandleContext(context.Background(), lockPath, exclusive, wait)
}

// LockFileHandleContext is LockFileHandle with a cancellable wait; see
// LockFileContext.
func LockFileHandleContext(ctx context.Context, lockPath string, exclusive bool, wait time.Duration) (*FileLock, error) {
	if lockPath == "" {
		return nil, errors.New("vault: lock file: empty lock path")
	}
	// A lock file is an artifact like any other, so the location policy decides
	// before one can exist.
	if err := GuardMutation("", lockPath); err != nil {
		return nil, err
	}
	if dir := filepath.Dir(lockPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("vault: create lock directory %q: %w", dir, err)
		}
	}
	if wait == 0 {
		wait = pathLockTimeout
	}
	file, err := lockOneContext(ctx, lockPath, lockPath, exclusive, wait)
	if err != nil {
		return nil, err
	}
	return &FileLock{file: file}, nil
}
