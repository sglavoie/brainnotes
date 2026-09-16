// Package textnorm provides Unicode normalization helpers shared across the
// low-level vault, query, wikilink, and note-mutation layers. macOS stores
// filenames in NFD (decomposed) form while editors such as Obsidian write
// wikilinks in NFC (composed) form, so byte-wise comparisons of accented
// titles fail even though they represent the same text. Normalizing both sides
// to NFC before comparing (or before building lookup keys) makes accented
// titles resolve regardless of the on-disk form.
package textnorm

import (
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// NFC returns s normalized to Unicode Normalization Form C (canonical
// composition). It is safe to call on any string, including already-composed
// input, and is idempotent.
func NFC(s string) string {
	return norm.NFC.String(s)
}

// Fold returns the Unicode case-folded form of s, which is what "the same name
// ignoring case" means for text that is not ASCII.
//
// Deliberately not strings.ToLower. That applies the *simple* lowercase
// mapping, which is rune-to-rune by construction: it maps `ẞ` (U+1E9E) to `ß`
// rather than to `ss`, and `İ` (U+0130) to `i` rather than to `i` plus a
// combining dot above. A case-insensitive filesystem treats each of those pairs
// as one name, so a caller building a collision key with ToLower would report
// two distinct keys for one file.
//
// Folding is also not normalization-preserving, which is why a caller building a
// key must normalize *after* folding and not only before: the fold of `ΐ`
// (U+0390) is U+03B9 U+0308 U+0301, a decomposed sequence NFC composes straight
// back to U+0390. Folding a normalized string can therefore hand back an
// unnormalized one.
//
// A cases.Caser may be stateful, so one is made per call rather than shared.
func Fold(s string) string {
	return cases.Fold().String(s)
}
