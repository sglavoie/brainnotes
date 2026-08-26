package web

import (
	"testing"
	"unicode/utf16"
)

func TestUTF16Offset(t *testing.T) {
	cases := []struct {
		name       string
		s          string
		byteOffset int
		want       int
	}{
		{name: "empty", s: "", byteOffset: 0, want: 0},
		{name: "ascii", s: "tag:wip", byteOffset: 4, want: 4},
		{name: "before accent", s: "tag:caf\u00e9", byteOffset: 4, want: 4},
		{name: "after accent", s: "tag:caf\u00e9", byteOffset: 9, want: 8},
		{name: "decomposed accent", s: "tag:cafe\u0301", byteOffset: 10, want: 9},
		{name: "after astral rune", s: "tag:\U0001F3A7 x", byteOffset: 8, want: 6},
		{name: "end after astral rune", s: "tag:\U0001F3A7 x", byteOffset: 10, want: 8},
		{name: "negative clamps", s: "tag:wip", byteOffset: -3, want: 0},
		{name: "past end clamps", s: "tag:wip", byteOffset: 99, want: 7},
		// A byte offset inside a rune falls back to that rune's start rather
		// than counting a half character.
		{name: "mid rune", s: "tag:caf\u00e9", byteOffset: 8, want: 7},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := utf16Offset(tc.s, tc.byteOffset); got != tc.want {
				t.Fatalf("utf16Offset(%q, %d) = %d, want %d", tc.s, tc.byteOffset, got, tc.want)
			}
		})
	}
}

func TestUTF16OffsetMatchesTheStandardEncoder(t *testing.T) {
	// The hand-rolled loop must agree with unicode/utf16 at every rune
	// boundary; the loop exists only to avoid allocating a []uint16 per call.
	for _, s := range []string{
		"",
		"tag:wip and folder:Archive",
		"tag:caf\u00e9",
		"tag:cafe\u0301",
		"near:[[\U0001F3A7 Focus]]<=2",
		"\U0001F600\U0001F600\U0001F600",
	} {
		for i := 0; i <= len(s); i++ {
			if !isRuneBoundary(s, i) {
				continue
			}
			want := len(utf16.Encode([]rune(s[:i])))
			if got := utf16Offset(s, i); got != want {
				t.Fatalf("utf16Offset(%q, %d) = %d, want %d", s, i, got, want)
			}
			if back := byteOffsetFromUTF16(s, want); back != i {
				t.Fatalf("byteOffsetFromUTF16(%q, %d) = %d, want %d", s, want, back, i)
			}
		}
	}
}

func TestByteOffsetFromUTF16(t *testing.T) {
	const s = "tag:\U0001F3A7 x"

	cases := []struct {
		unitOffset int
		want       int
	}{
		{unitOffset: -1, want: 0},
		{unitOffset: 0, want: 0},
		{unitOffset: 4, want: 4},
		// 5 splits the surrogate pair: resolve to the rune's start.
		{unitOffset: 5, want: 4},
		{unitOffset: 6, want: 8},
		{unitOffset: 8, want: 10},
		{unitOffset: 99, want: 10},
	}

	for _, tc := range cases {
		if got := byteOffsetFromUTF16(s, tc.unitOffset); got != tc.want {
			t.Errorf("byteOffsetFromUTF16(%q, %d) = %d, want %d", s, tc.unitOffset, got, tc.want)
		}
	}

	// A trailing astral rune is the case where the split lands past every
	// boundary the range loop visits.
	const trailing = "a\U0001F600"
	if got, want := byteOffsetFromUTF16(trailing, 2), 1; got != want {
		t.Errorf("byteOffsetFromUTF16(%q, 2) = %d, want %d", trailing, got, want)
	}
}

func isRuneBoundary(s string, i int) bool {
	if i == 0 || i == len(s) {
		return true
	}
	return s[i]&0xC0 != 0x80
}
