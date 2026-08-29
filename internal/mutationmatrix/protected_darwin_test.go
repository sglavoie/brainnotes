//go:build darwin

package mutationmatrix

import (
	"testing"

	"golang.org/x/sys/unix"
)

// fileProviderDomainAttr is the extended attribute macOS puts on storage a File
// Provider extension owns. It is the whole basis of the location policy: a path
// under one that goes vacant is replicated as a deletion.
const fileProviderDomainAttr = "com.apple.file-provider-domain-id"

// protectRoot makes root look like storage the policy refuses. It runs after the
// fixture is on disk, so only the operation under test faces the refusal.
func protectRoot(t *testing.T, root string) bool {
	t.Helper()
	if err := unix.Setxattr(root, fileProviderDomainAttr, []byte("matrix-domain"), 0); err != nil {
		t.Skipf("cannot set %s on %q: %v", fileProviderDomainAttr, root, err)
	}
	return true
}
