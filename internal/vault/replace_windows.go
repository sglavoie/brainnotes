//go:build windows

package vault

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// nativeReplacementSupported reports that this platform has a replacement
	// primitive that never leaves the destination name absent.
	nativeReplacementSupported = true
	// replacementExchangesNames is false: ReplaceFileW consumes the replacement
	// name instead of swapping the two, so only the destination name is required
	// to stay continuously present.
	replacementExchangesNames = false
	nativeReplacementName     = "ReplaceFileW"
)

const (
	replaceFileWriteThrough      = 0x00000001
	replaceFileIgnoreMergeErrors = 0x00000002
)

// The three partial-outcome codes ReplaceFileW reports. Only the second and
// third can leave the destination without its original content, which is why the
// classifier below decides from the filesystem rather than from the code alone.
const (
	errorUnableToRemoveReplaced   = syscall.Errno(1175)
	errorUnableToMoveReplacement  = syscall.Errno(1176)
	errorUnableToMoveReplacement2 = syscall.Errno(1177)
)

var (
	modkernel32ForReplace = windows.NewLazySystemDLL("kernel32.dll")
	procReplaceFileW      = modkernel32ForReplace.NewProc("ReplaceFileW")
)

func replaceFileW(replaced, replacement, backup string, flags uint32) error {
	replacedPtr, err := windows.UTF16PtrFromString(replaced)
	if err != nil {
		return err
	}
	replacementPtr, err := windows.UTF16PtrFromString(replacement)
	if err != nil {
		return err
	}
	backupPtr, err := windows.UTF16PtrFromString(backup)
	if err != nil {
		return err
	}
	if err := procReplaceFileW.Find(); err != nil {
		return err
	}
	r1, _, e1 := syscall.SyscallN(
		procReplaceFileW.Addr(),
		uintptr(unsafe.Pointer(replacedPtr)),
		uintptr(unsafe.Pointer(replacementPtr)),
		uintptr(unsafe.Pointer(backupPtr)),
		uintptr(flags),
		0,
		0,
	)
	if r1 != 0 {
		return nil
	}
	if e1 != 0 {
		return e1
	}
	return syscall.EINVAL
}

// nativeReplace installs source at destination through ReplaceFileW, which
// swaps the underlying data in place rather than unlinking the destination
// first, and moves the destination's previous content to a same-volume backup
// this call names. The backup is the displaced copy the compare-and-swap above
// judges.
func nativeReplace(source, destination string) (string, error) {
	backup, err := reserveBackupName(filepath.Dir(destination))
	if err != nil {
		return "", err
	}
	flags := uint32(replaceFileWriteThrough | replaceFileIgnoreMergeErrors)
	if err := replaceFileW(destination, source, backup, flags); err != nil {
		return "", classifyReplaceFailure(destination, source, backup, err)
	}
	return backup, nil
}

// reserveBackupName picks an unused sibling name for the displaced copy.
// ReplaceFileW wants a name rather than a handle, so the placeholder is created
// exclusively — which is what makes the name unique — and then removed.
func reserveBackupName(dir string) (string, error) {
	file, path, err := openExclusiveTemp(dir, ".txn-displaced-", ".tmp", 0o600)
	if err != nil {
		return "", fmt.Errorf("vault: reserve displaced copy in %q: %w", dir, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("vault: close %q: %w", path, err)
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("vault: clear %q: %w", path, err)
	}
	return path, nil
}

// classifyReplaceFailure decides what a failed ReplaceFileW left behind by
// looking at the filesystem, not at the error code. ERROR_UNABLE_TO_
// MOVE_REPLACEMENT_2 in particular can report failure after the destination has
// already been moved to the backup, which is exactly the destination-name gap
// this package exists to prevent — so the backup goes straight back and the
// caller is told the outcome was partial.
func classifyReplaceFailure(destination, source, backup string, cause error) error {
	_, statErr := os.Lstat(destination)
	switch {
	case statErr == nil:
		// The destination kept its content. Whatever ReplaceFileW managed to
		// write to the backup is redundant.
		_ = os.Remove(backup)
		return cause
	case !errors.Is(statErr, fs.ErrNotExist):
		return errors.Join(cause, statErr)
	}
	stage := replaceFailureStage(cause)
	if _, err := os.Lstat(backup); err == nil {
		restoreErr := renameNoReplace(backup, destination)
		if restoreErr == nil {
			return &PartialReplacementError{Path: destination, Stage: stage, Restored: true, Err: cause}
		}
		return &PartialReplacementError{
			Path:       destination,
			Stage:      stage,
			BackupPath: backup,
			Err:        errors.Join(cause, restoreErr),
		}
	}
	// Nothing to restore from. The source still holds the replacement bytes, so
	// name it: it is the only copy of anything the caller can act on.
	return &PartialReplacementError{Path: destination, Stage: stage, BackupPath: source, Err: cause}
}

// replaceFailureStage names which half of ReplaceFileW gave up, so the mixed
// state a person has to repair is described rather than only numbered.
func replaceFailureStage(cause error) string {
	switch {
	case errors.Is(cause, errorUnableToRemoveReplaced):
		return "the replaced file could not be removed"
	case errors.Is(cause, errorUnableToMoveReplacement):
		return "the replacement could not be renamed onto the destination"
	case errors.Is(cause, errorUnableToMoveReplacement2):
		return "the replacement could not be renamed onto the destination after the original was moved aside"
	default:
		return "the replacement failed"
	}
}
