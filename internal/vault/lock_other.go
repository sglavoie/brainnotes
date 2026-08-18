//go:build !darwin && !linux && !windows

package vault

import "os"

// lockingSupported is false here, so transactions never take a path lock and two
// concurrent bn writers on one note keep the unserialized behavior described on
// FileTxn.lockPaths.
const lockingSupported = false

func tryLockExclusive(_ *os.File) (bool, error) { return true, nil }

func unlockFile(_ *os.File) error { return nil }
