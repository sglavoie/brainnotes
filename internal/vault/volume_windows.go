//go:build windows

package vault

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// volumeIdentity names the storage dir lives on. The replacement capability
// self-test is cached against it, and a staged file whose identity differs from
// its destination's is refused before the primitive runs, because ReplaceFileW
// is same-volume only.
func volumeIdentityOf(dir string) (string, error) {
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return "", fmt.Errorf("vault: %q: %w", dir, err)
	}
	handle, err := windows.CreateFile(
		path,
		0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return "", fmt.Errorf("vault: open %q: %w", dir, err)
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return "", fmt.Errorf("vault: volume of %q: %w", dir, err)
	}
	return fmt.Sprintf("volume:%d", info.VolumeSerialNumber), nil
}
