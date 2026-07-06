// Package textnorm provides Unicode normalization helpers shared across the
// low-level vault, query, wikilink, and note-mutation layers. macOS stores
// filenames in NFD (decomposed) form while editors such as Obsidian write
// wikilinks in NFC (composed) form, so byte-wise comparisons of accented
// titles fail even though they represent the same text. Normalizing both sides
// to NFC before comparing (or before building lookup keys) makes accented
// titles resolve regardless of the on-disk form.
package textnorm

import "golang.org/x/text/unicode/norm"

// NFC returns s normalized to Unicode Normalization Form C (canonical
// composition). It is safe to call on any string, including already-composed
// input, and is idempotent.
func NFC(s string) string {
	return norm.NFC.String(s)
}
