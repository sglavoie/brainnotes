//go:build darwin

package vault

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// renameNoReplace reports a volume that refuses RENAME_EXCL (some network and
// FAT-family filesystems answer ENOTSUP) as errors.ErrUnsupported, so a caller
// with a fallback can tell it apart from a real failure.
func renameNoReplace(source, destination string) error {
	err := unix.RenamexNp(source, destination, unix.RENAME_EXCL)
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.ENOSYS) {
		return fmt.Errorf("%w: %w", errors.ErrUnsupported, err)
	}
	return err
}
