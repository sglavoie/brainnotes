//go:build !darwin && !linux && !windows

package vault

import (
	"errors"
	"fmt"
)

var errRenameNoReplaceUnsupported = fmt.Errorf("atomic no-replace rename is unsupported on this platform: %w", errors.ErrUnsupported)

func renameNoReplace(_, _ string) error {
	return errRenameNoReplaceUnsupported
}
