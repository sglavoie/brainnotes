//go:build windows

package vault

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

const lockingSupported = true

// tryLockExclusive takes an exclusive lock on the first byte without blocking,
// reporting false when another holder has it. Windows locks belong to the file
// handle, so two handles in one process contend the way flock descriptors do.
func tryLockExclusive(f *os.File) (bool, error) {
	overlapped := new(windows.Overlapped)
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
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
