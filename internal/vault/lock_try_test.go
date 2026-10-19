package vault

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestTryLockPathAcquiresAndReleases is the ordinary path: a free lock is taken,
// releasing it hands it back, and release is safe to call more than once so a
// deferred release next to an explicit one is not a double-unlock.
func TestTryLockPathAcquiresAndReleases(t *testing.T) {
	requireLocking(t)

	lockPath := filepath.Join(t.TempDir(), "index.lock")

	release, acquired, err := TryLockPath(lockPath)
	if err != nil {
		t.Fatalf("TryLockPath: %v", err)
	}
	if !acquired {
		release()
		t.Fatal("free lock was not acquired")
	}
	release()
	release()

	again, acquired, err := TryLockPath(lockPath)
	if err != nil {
		t.Fatalf("TryLockPath after release: %v", err)
	}
	if !acquired {
		t.Fatal("lock was not reacquirable after release")
	}
	again()
}

// TestTryLockPathReturnsPromptlyUnderContention is the contract that separates
// this from LockPaths. A held lock must come back as a clean miss immediately —
// not as an error, and not after the pathLockTimeout budget a blocking
// acquisition would spend. The caller's whole reason to use it is that skipping
// the work is cheaper than waiting for it.
func TestTryLockPathReturnsPromptlyUnderContention(t *testing.T) {
	requireLocking(t)

	lockPath := filepath.Join(t.TempDir(), "index.lock")
	held, acquired, err := TryLockPath(lockPath)
	if err != nil || !acquired {
		t.Fatalf("first TryLockPath: acquired=%v err=%v", acquired, err)
	}
	defer held()

	// The bound is a fraction of the blocking budget rather than a few
	// milliseconds: it must fail a poll-until-deadline regression without being
	// flaky on a loaded machine, where a single syscall can still be scheduled out
	// for longer than pathLockPollInterval.
	const attempts = 5
	limit := pathLockTimeout / 4
	for i := 0; i < attempts; i++ {
		start := time.Now()
		release, acquired, err := TryLockPath(lockPath)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("contended TryLockPath returned an error: %v", err)
		}
		if acquired {
			release()
			t.Fatal("acquired a lock another descriptor holds")
		}
		if elapsed >= limit {
			t.Fatalf("contended TryLockPath took %v, want well under the %v blocking budget", elapsed, pathLockTimeout)
		}
	}
}

// TestTryLockPathContendsWithinOneProcess is the flock-not-fcntl invariant seen
// from the outside. fcntl record locks belong to the process, so a second
// goroutine in one bn process would take a lock the first already holds and both
// would proceed. Exactly one of a concurrent fan-out must win.
func TestTryLockPathContendsWithinOneProcess(t *testing.T) {
	requireLocking(t)

	lockPath := filepath.Join(t.TempDir(), "index.lock")

	const goroutines = 8
	var (
		wg        sync.WaitGroup
		attempted sync.WaitGroup
		mu        sync.Mutex
		wins      int
		start     = make(chan struct{})
	)
	attempted.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			release, acquired, err := TryLockPath(lockPath)
			mu.Lock()
			if err == nil && acquired {
				wins++
			}
			mu.Unlock()
			attempted.Done()
			if acquired {
				// Hold it until every other goroutine has made its attempt, or a
				// late loser could win after an early winner released.
				attempted.Wait()
			}
			release()
		}()
	}
	close(start)
	wg.Wait()

	if wins != 1 {
		t.Fatalf("%d of %d concurrent goroutines acquired the lock, want exactly 1", wins, goroutines)
	}
}

// TestTryLockPathBlocksABlockingWaiter checks the lock it takes is the same
// exclusive flock the rest of the package uses, not a private mechanism that only
// excludes other TryLockPath callers.
func TestTryLockPathBlocksABlockingWaiter(t *testing.T) {
	requireLocking(t)

	lockPath := filepath.Join(t.TempDir(), "index.lock")
	release, acquired, err := TryLockPath(lockPath)
	if err != nil || !acquired {
		t.Fatalf("TryLockPath: acquired=%v err=%v", acquired, err)
	}

	file, err := lockOne(lockPath, lockPath, true, 20*time.Millisecond)
	if !IsTxnLockBusy(err) {
		if file != nil {
			_ = unlockFile(file)
			_ = file.Close()
		}
		release()
		t.Fatalf("blocking acquisition err = %v, want *TxnLockBusyError", err)
	}

	release()
	file, err = lockOne(lockPath, lockPath, true, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("blocking acquisition after release: %v", err)
	}
	_ = unlockFile(file)
	_ = file.Close()
}

// TestTryLockPathCreatesMissingParentDirectories matters because the lock this
// exists for lives outside .bnotes/locks, which acquirePathLocks is the only
// thing that creates. A caller naming a lock in a directory that does not exist
// yet must not get an ENOENT it has to pre-empt with its own MkdirAll.
func TestTryLockPathCreatesMissingParentDirectories(t *testing.T) {
	requireLocking(t)

	lockPath := filepath.Join(t.TempDir(), ".bnotes", "index.lock")
	release, acquired, err := TryLockPath(lockPath)
	if err != nil {
		t.Fatalf("TryLockPath into a missing directory: %v", err)
	}
	if !acquired {
		release()
		t.Fatal("lock in a fresh directory was not acquired")
	}
	release()

	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file was not created at %s: %v", lockPath, err)
	}
}

// TestTryLockPathRejectsAnEmptyPath pins that the failure modes stay separable:
// an error is a real problem, acquired=false is only ever contention. Release is
// non-nil in both cases so a deferred call needs no guard.
func TestTryLockPathRejectsAnEmptyPath(t *testing.T) {
	release, acquired, err := TryLockPath("")
	if err == nil {
		t.Fatal("empty lock path accepted")
	}
	if acquired {
		t.Fatal("acquired reported true alongside an error")
	}
	if release == nil {
		t.Fatal("release is nil; a deferred release would panic")
	}
	release()
}
