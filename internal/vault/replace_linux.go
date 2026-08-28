//go:build linux

package vault

import "golang.org/x/sys/unix"

const (
	// nativeReplacementSupported reports that this platform has a replacement
	// primitive that never leaves the destination name absent.
	nativeReplacementSupported = true
	// replacementExchangesNames reports that the primitive swaps the two names
	// rather than consuming the source, so the source name must also stay
	// continuously present across the call.
	replacementExchangesNames = true
	nativeReplacementName     = "renameat2(RENAME_EXCHANGE)"
)

// nativeReplace exchanges the source and destination names in one atomic step.
// RENAME_EXCHANGE requires kernel 3.15 and filesystem support; a filesystem
// that does not implement it fails with EINVAL or ENOSYS, which the capability
// self-test turns into a refusal before any note is touched.
func nativeReplace(source, destination string) (string, error) {
	err := unix.Renameat2(
		unix.AT_FDCWD,
		source,
		unix.AT_FDCWD,
		destination,
		unix.RENAME_EXCHANGE,
	)
	if err != nil {
		return "", err
	}
	return source, nil
}
