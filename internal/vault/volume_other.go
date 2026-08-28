//go:build !unix && !windows

package vault

import "fmt"

// volumeIdentity has no way to name storage here. Replacement is unsupported on
// these platforms anyway, so this only ever produces the refusal message.
func volumeIdentityOf(dir string) (string, error) {
	return "", fmt.Errorf("vault: %q: this platform reports no volume identity", dir)
}
