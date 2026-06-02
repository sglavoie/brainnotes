package cli

import (
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

// splitListValues splits a raw comma-separated string into coerced Go-native
// values, dropping empty entries (matching the legacy '=' behavior).
func splitListValues(rawVal, typeFlag string) ([]any, error) {
	return noteops.SplitFrontmatterListValues(rawVal, typeFlag)
}

// wikilinkNormalized returns v with each string element wrapped as `[[X]]`
// when key is a wikilink-typed property. It mirrors the wrap step performed
// by frontmatter.Marshal so an `opSet` comparison can detect that the new
// value would round-trip to the same on-disk bytes as the current value.
// Without this, `bn set parent=Foo` would always rewrite a note whose stored
// value is `[[Foo]]` because the raw user input differs byte-for-byte.
func wikilinkNormalized(v any, key string, fm *config.FrontmatterConfig) any {
	return noteops.WikilinkNormalizedFrontmatterValue(v, key, fm)
}

func wrapWikilinkValues(vals []any) []any {
	return noteops.WrapWikilinkFrontmatterValues(vals)
}

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
