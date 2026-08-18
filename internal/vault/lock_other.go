//go:build !darwin && !linux && !windows

package vault

import "os"

// lockingSupported is false here, so transactions never take a path lock and two
// concurrent bn writers on one note keep the unserialized behavior described on
// FileTxn.lockPaths.
const lockingSupported = false

// openFileLimit reports no budget: no lock descriptor is ever opened here, so
// nothing a transaction does is bounded by the descriptor limit.
func openFileLimit() (int, bool) { return 0, false }

func tryLock(_ *os.File, _ bool) (bool, error) { return true, nil }

func unlockFile(_ *os.File) error { return nil }
