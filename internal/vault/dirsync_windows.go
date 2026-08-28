//go:build windows

package vault

// syncDir is a no-op on Windows: a directory handle has no flush, and the
// metadata durability POSIX gets from an fsync on the parent is what
// REPLACEFILE_WRITE_THROUGH already asks the filesystem for on this platform.
func syncDir(_ string) error { return nil }
