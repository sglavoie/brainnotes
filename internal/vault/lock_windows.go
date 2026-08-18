//go:build windows

package vault

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

const lockingSupported = true

// openFileLimit reports no budget: Windows has no per-process descriptor rlimit,
// so handle count is not what bounds a transaction here.
func openFileLimit() (int, bool) { return 0, false }

// tryLock takes a lock on the first byte without blocking, reporting false when
// a conflicting holder has it. Windows locks belong to the file handle, so two
// handles in one process contend the way flock descriptors do, and a shared lock
// conflicts only with an exclusive one.
func tryLock(f *os.File, exclusive bool) (bool, error) {
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	overlapped := new(windows.Overlapped)
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		flags,
		0, 1, 0,
		overlapped,
	)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, windows.ERROR_LOCK_VIOLATION), errors.Is(err, windows.ERROR_IO_PENDING):
		return false, nil
	default:
		return false, err
	}
}

func unlockFile(f *os.File) error {
	overlapped := new(windows.Overlapped)
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, overlapped)
}
