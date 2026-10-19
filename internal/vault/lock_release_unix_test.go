//go:build darwin || linux

package vault

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// A fork in another goroutine duplicates every open descriptor into a child
// that keeps it until exec. dupHeld stands in for that child's copy.
func dupHeld(t *testing.T, lock *FileLock) *os.File {
	t.Helper()
	fd, err := unix.Dup(int(lock.file.Fd()))
	if err != nil {
		t.Fatalf("dup: %v", err)
	}
	dup := os.NewFile(uintptr(fd), "forked-copy")
	t.Cleanup(func() { _ = dup.Close() })
	return dup
}

// Releasing a lock nobody was handed must free it even while a forked child
// still holds a duplicate of the descriptor, or the holder's next single try
// reads busy.
func TestReleaseFreesALockStillDuplicatedByAForkedChild(t *testing.T) {
	path := filepath.Join(t.TempDir(), "safety.lock")
	lock, acquired, err := TryLockPathHandle(path)
	if err != nil || !acquired {
		t.Fatalf("TryLockPathHandle = %v, %v", acquired, err)
	}
	dupHeld(t, lock)
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	release, acquired, err := TryLockPath(path)
	if err != nil || !acquired {
		t.Fatalf("TryLockPath after Release = %v, %v; want the lock free", acquired, err)
	}
	release()
}

// A lock whose descriptor was handed out for exec inheritance must stay held
// by the inheriting copy after the parent releases its own.
func TestReleaseKeepsAHandedOutLockHeldByItsInheritor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claim.lock")
	lock, acquired, err := TryLockPathHandle(path)
	if err != nil || !acquired {
		t.Fatalf("TryLockPathHandle = %v, %v", acquired, err)
	}
	_ = lock.File()
	inherited := dupHeld(t, lock)
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, acquired, err := TryLockPath(path); err != nil || acquired {
		t.Fatalf("TryLockPath while inherited = %v, %v; want busy", acquired, err)
	}
	_ = inherited.Close()
	release, acquired, err := TryLockPath(path)
	if err != nil || !acquired {
		t.Fatalf("TryLockPath after the inheritor closed = %v, %v", acquired, err)
	}
	release()
}
