//go:build linux

package vault

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// renameNoReplace reports a kernel or filesystem without RENAME_NOREPLACE
// (EINVAL from a filesystem that does not implement the flag, ENOSYS before
// Linux 3.15) as errors.ErrUnsupported, so a caller with a fallback can tell it
// apart from a real failure.
func renameNoReplace(source, destination string) error {
	err := unix.Renameat2(
		unix.AT_FDCWD,
		source,
		unix.AT_FDCWD,
		destination,
		unix.RENAME_NOREPLACE,
	)
	if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP) {
		return fmt.Errorf("%w: %w", errors.ErrUnsupported, err)
	}
	return err
}
