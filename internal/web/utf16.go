package web

import (
	"unicode/utf8"
)

// The query APIs speak UTF-16 code-unit offsets, not byte offsets.
//
// A browser's caret is a UTF-16 index: `input.selectionStart` and
// `setSelectionRange` count code units, because that is how a JavaScript string
// is indexed. Go's spans are byte offsets. The two agree only for ASCII, and
// this vault is NFC-normalized precisely because accented titles are ordinary
// in it, so any query naming one would drift a highlight by a byte per
// non-ASCII character if the conversion were left to the client. Doing it here
// means the browser can use what it receives verbatim.

// utf16Offset converts a byte offset into s to the UTF-16 code-unit offset of
// the same position. The offset is clamped into range and moved back to a rune
// boundary, so a caller cannot produce a count for a position inside a
// character.
func utf16Offset(s string, byteOffset int) int {
	byteOffset = alignByteOffset(s, byteOffset)

	units := 0
	for _, r := range s[:byteOffset] {
		units++
		if r > 0xFFFF {
			// Astral runes are one Go rune but a surrogate pair in UTF-16.
			units++
		}
	}
	return units
}

// byteOffsetFromUTF16 converts a UTF-16 code-unit offset into s to the byte
// offset of the same position. An offset landing between the halves of a
// surrogate pair resolves to the start of that rune, and an out-of-range offset
// clamps to the nearest end.
func byteOffsetFromUTF16(s string, unitOffset int) int {
	if unitOffset <= 0 {
		return 0
	}

	units := 0
	for i, r := range s {
		if units >= unitOffset {
			return i
		}
		next := units + 1
		if r > 0xFFFF {
			next++
		}
		if next > unitOffset {
			// The offset splits this rune's surrogate pair; resolve to the
			// rune's start, the same way utf16Offset aligns backwards.
			return i
		}
		units = next
	}
	return len(s)
}

// alignByteOffset clamps a byte offset into s and moves it back to a rune
// boundary.
func alignByteOffset(s string, offset int) int {
	if offset < 0 {
		return 0
	}
	if offset > len(s) {
		return len(s)
	}
	for offset > 0 && offset < len(s) && !utf8.RuneStart(s[offset]) {
		offset--
	}
	return offset
}
