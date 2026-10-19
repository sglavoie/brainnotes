package vault

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// RenameNoReplace moves source to destination only when destination does not
// exist. A destination that does exist is reported as an error satisfying
// os.IsExist, and nothing moves.
//
// It is the last line of defense under a rename or move whose destination check
// ran earlier: a note that appeared at the destination since then is kept rather
// than silently replaced. Where the kernel primitive is unavailable — a platform
// without one, or a filesystem that refuses the flag — it falls back to an
// existence check followed by a plain rename. That fallback is not atomic against
// a writer that ignores bn's locks, which is why callers hold the path lock on
// both ends: it excludes every other bn writer for the length of the move.
func RenameNoReplace(source, destination string) error {
	err := renameNoReplace(source, destination)
	if err == nil {
		return nil
	}
	if !errors.Is(err, errors.ErrUnsupported) {
		return &os.LinkError{Op: "rename", Old: source, New: destination, Err: err}
	}
	if _, statErr := os.Lstat(destination); statErr == nil {
		return &os.LinkError{Op: "rename", Old: source, New: destination, Err: fs.ErrExist}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return fmt.Errorf("vault: stat rename destination %q: %w", destination, statErr)
	}
	return os.Rename(source, destination)
}
