//go:build darwin

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
	nativeReplacementName     = "renameatx_np(RENAME_SWAP)"
)

// nativeReplace exchanges the source and destination names in one atomic step.
// Both names exist before the call and both exist after it, so no reader can
// ever observe the destination missing — which is the whole reason this
// primitive replaced the rename-into-custody sequence.
func nativeReplace(source, destination string) (string, error) {
	if err := unix.RenamexNp(source, destination, unix.RENAME_SWAP); err != nil {
		return "", err
	}
	return source, nil
}
