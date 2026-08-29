//go:build !darwin

package mutationmatrix

import "testing"

// protectRoot reports that there is nothing to protect against here.
//
// The storage-location policy is not a general "is this storage trustworthy"
// check; it tests for one specific macOS arrangement, and internal/vault has no
// equivalent attribute to consult on other platforms. Reporting false rather
// than faking a refusal keeps this half of the matrix honest: on Linux and
// Windows the assertion is skipped, not silently passed.
func protectRoot(t *testing.T, root string) bool {
	t.Helper()
	return false
}
