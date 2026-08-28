//go:build !darwin && !linux && !windows

package vault

import "errors"

const (
	// nativeReplacementSupported is false here: there is no primitive that can
	// install a replacement without first vacating the destination name, and
	// there is deliberately no fallback that does.
	nativeReplacementSupported = false
	replacementExchangesNames  = false
	nativeReplacementName      = "none"
)

var errNativeReplacementUnsupported = errors.New("no path-preserving atomic replacement primitive on this platform")

func nativeReplace(_, _ string) (string, error) {
	return "", errNativeReplacementUnsupported
}
