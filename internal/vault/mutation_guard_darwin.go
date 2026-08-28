//go:build darwin

package vault

import (
	"errors"
	"fmt"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// fileProviderDomainAttr is the extended attribute macOS puts on a directory a
// File Provider extension owns — iCloud Drive, Dropbox, Google Drive, OneDrive
// and anything else using the same API. Its presence, not its value, is the
// signal: the domain id identifies which provider, and every provider has the
// behavior this guard exists for.
const fileProviderDomainAttr = "com.apple.file-provider-domain-id"

// fileProviderReason is the one clause every refusal under this policy carries.
// It states the mechanism rather than the product, because the failure is not
// specific to one provider: a replacement that vacates a path, even for the
// microseconds between two syscalls, is observable to the extension, which
// replicates it as a deletion and can later propagate that deletion back over
// the file that replaced it.
const fileProviderReason = "managed by a macOS File Provider (iCloud Drive, Dropbox, Google Drive, OneDrive), which replicates a path becoming vacant as a deletion"

// mutationLocationPolicy refuses any location beneath a File Provider domain.
var mutationLocationPolicy mutationLocationPolicyFunc = fileProviderLocationPolicy

// readFileProviderDomain reports whether path itself carries the File Provider
// domain attribute. It is a variable because the kernel will not fail a
// getxattr on demand, and "an unexpected xattr error refuses the write" is a
// behavior that has to stay covered by a test rather than by inspection.
var readFileProviderDomain = fileProviderDomainPresent

// fileProviderLocationPolicy walks path and its ancestors up to the volume root
// looking for a File Provider domain.
//
// A missing attribute continues the walk: the vault root itself is rarely the
// managed directory, and the one that lost a note was three levels below
// ~/Documents. Anything else — a permission failure, an attribute that cannot
// be read back, a directory that cannot be stat'ed — stops the walk and refuses
// the write, because a guard that cannot tell whether storage is safe has not
// established that it is.
//
// The walk stops at the volume root rather than at "/": a File Provider domain
// is a property of a location within a filesystem, and continuing past a mount
// point would let an unrelated ancestor on a different volume decide the
// verdict for this one.
func fileProviderLocationPolicy(path string) (string, string, error) {
	startDevice, err := deviceOf(path)
	if err != nil {
		return "", "", err
	}
	current := path
	for {
		managed, err := readFileProviderDomain(current)
		if err != nil {
			return "", "", err
		}
		if managed {
			return current, fileProviderReason, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", "", nil
		}
		parentDevice, err := deviceOf(parent)
		if err != nil {
			return "", "", err
		}
		if parentDevice != startDevice {
			return "", "", nil
		}
		current = parent
	}
}

func deviceOf(path string) (uint64, error) {
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return 0, fmt.Errorf("stat %q: %w", path, err)
	}
	return uint64(st.Dev), nil
}

// fileProviderDomainPresent queries the attribute's size and then reads it
// back. The presence of the attribute is what decides the verdict, so the
// second call adds no information in the ordinary case — it exists so an
// attribute that reports a size and then cannot be read is reported as an
// error instead of being resolved in favour of writing.
func fileProviderDomainPresent(path string) (bool, error) {
	size, err := unix.Getxattr(path, fileProviderDomainAttr, nil)
	switch {
	case err == nil:
	case errors.Is(err, unix.ENOATTR), errors.Is(err, unix.ENOTSUP), errors.Is(err, unix.ENOENT):
		// No domain here, no xattr support here, or nothing here any more.
		// None of the three says anything about the ancestors, so keep walking.
		return false, nil
	default:
		return false, fmt.Errorf("read %s on %q: %w", fileProviderDomainAttr, path, err)
	}
	if size < 0 {
		return false, fmt.Errorf("read %s on %q: negative attribute size %d", fileProviderDomainAttr, path, size)
	}
	value := make([]byte, size)
	read, err := unix.Getxattr(path, fileProviderDomainAttr, value)
	switch {
	case err == nil:
	case errors.Is(err, unix.ENOATTR):
		// Removed between the two calls, which means the directory was not
		// managed by the time the decision mattered.
		return false, nil
	default:
		return false, fmt.Errorf("read %s on %q: %w", fileProviderDomainAttr, path, err)
	}
	if read < 0 || read > size {
		return false, fmt.Errorf("read %s on %q: attribute length %d is out of range", fileProviderDomainAttr, path, read)
	}
	return true, nil
}
