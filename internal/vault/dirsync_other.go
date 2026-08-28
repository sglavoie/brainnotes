//go:build !windows

package vault

import (
	"fmt"
	"os"
)

// syncDir flushes a directory's entries so a rename that changed them survives
// power loss. POSIX guarantees rename atomicity within a running system but not
// across a crash until the directory itself is synced.
func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("vault: open %q: %w", dir, err)
	}
	defer handle.Close()
	if err := handle.Sync(); err != nil {
		return fmt.Errorf("vault: fsync %q: %w", dir, err)
	}
	return nil
}
