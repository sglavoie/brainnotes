//go:build darwin || linux

package vault

import (
	"errors"
	"math"
	"os"

	"golang.org/x/sys/unix"
)

const lockingSupported = true

// openFileLimit reports the process soft descriptor limit. A limit too large to
// hold in an int is reported as unbounded, since a budget that big constrains
// nothing.
func openFileLimit() (int, bool) {
	var limit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &limit); err != nil {
		return 0, false
	}
	if limit.Cur > uint64(math.MaxInt32) {
		return 0, false
	}
	return int(limit.Cur), true
}

// tryLock takes an advisory lock without blocking, reporting false when a
// conflicting holder has it. A shared lock conflicts only with an exclusive one,
// which is what lets ordinary writers hold the vault guard together while an
// oversized transaction takes it alone.
//
// It must be flock rather than an fcntl record lock. A flock lock belongs to the
// open file description, so two descriptors in one process contend with each
// other; fcntl locks belong to the process and would not, which would leave
// concurrent writers inside a single bn process — the web server, or any
// goroutine fan-out — silently unserialized.
func tryLock(f *os.File, exclusive bool) (bool, error) {
	how := unix.LOCK_SH
	if exclusive {
		how = unix.LOCK_EX
	}
	err := unix.Flock(int(f.Fd()), how|unix.LOCK_NB)
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
