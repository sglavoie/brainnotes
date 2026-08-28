//go:build unix

package vault

import (
	"fmt"
	"os"
	"syscall"
)

// volumeIdentity names the storage dir lives on. The replacement capability
// self-test is cached against it, and a staged file whose identity differs from
// its destination's is refused before the primitive runs, because every
// supported primitive is same-volume only.
func volumeIdentityOf(dir string) (string, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("vault: stat %q: %w", dir, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("vault: %q: this filesystem reports no device identity", dir)
	}
	return fmt.Sprintf("dev:%d", uint64(stat.Dev)), nil
}
