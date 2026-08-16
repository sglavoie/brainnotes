//go:build !darwin && !linux && !windows

package vault

import "errors"

var errRenameNoReplaceUnsupported = errors.New("atomic no-replace rename is unsupported on this platform")

func renameNoReplace(_, _ string) error {
	return errRenameNoReplaceUnsupported
}
