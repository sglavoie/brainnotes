package cli

import (
	"github.com/sglavoie/brainnotes/internal/noteops"
)

// wrapWikilinkValue wraps a bare string in `[[...]]` unless it is empty or
// already wrapped. Mirrors the unexported helper in internal/frontmatter so
// the CLI can normalize input before delegating to ListAdd/ListRemove, which
// compare values byte-for-byte.
func wrapWikilinkValue(s string) string {
	return noteops.WrapWikilinkFrontmatterValue(s)
}

// coerceValue converts a raw string to a Go-native value. When typeFlag is
// empty, the order of auto-detection matches the spec: bool > int > date >
// string. "raw" forces the literal string with no coercion.
func coerceValue(s, typeFlag string) (any, error) {
	return noteops.CoerceFrontmatterValue(s, typeFlag)
}
