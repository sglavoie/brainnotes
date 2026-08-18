//go:build darwin || linux

package vault

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

const lockingSupported = true

// tryLockExclusive takes an exclusive advisory lock without blocking, reporting
// false when another holder has it.
//
// It must be flock rather than an fcntl record lock. A flock lock belongs to the
// open file description, so two descriptors in one process contend with each
// other; fcntl locks belong to the process and would not, which would leave
// concurrent writers inside a single bn process — the web server, or any
// goroutine fan-out — silently unserialized.
func tryLockExclusive(f *os.File) (bool, error) {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, unix.EWOULDBLOCK):
		return false, nil
	default:
		return false, err
	}
}

func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
