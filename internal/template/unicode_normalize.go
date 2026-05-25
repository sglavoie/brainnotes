package template

import (
	"sort"
	"strings"
)

const (
	hangulSBase  = 0xAC00
	hangulLBase  = 0x1100
	hangulVBase  = 0x1161
	hangulTBase  = 0x11A7
	hangulLCount = 19
	hangulVCount = 21
	hangulTCount = 28
	hangulNCount = hangulVCount * hangulTCount
	hangulSCount = hangulLCount * hangulNCount
)

func equalFoldCanonical(a, b string) bool {
	return strings.EqualFold(normalizeCanonical(a), normalizeCanonical(b))
}

func normalizeCanonical(s string) string {
	if s == "" {
		return ""
	}

	decomposed := make([]rune, 0, len(s))
	for _, r := range s {
		appendCanonicalRunes(&decomposed, r)
	}
	reorderCanonicalMarks(decomposed)
	return string(decomposed)
}

func appendCanonicalRunes(dst *[]rune, r rune) {
	if appendHangulDecomposition(dst, r) {
		return
	}
	if entry, ok := lookupCanonicalDecomposition(r); ok {
		appendCanonicalRunes(dst, entry.to1)
		if entry.to2 != 0 {
			appendCanonicalRunes(dst, entry.to2)
		}
		return
	}
	*dst = append(*dst, r)
}

func appendHangulDecomposition(dst *[]rune, r rune) bool {
	if r < hangulSBase || r >= hangulSBase+hangulSCount {
		return false
	}

	sIndex := r - hangulSBase
	l := hangulLBase + sIndex/hangulNCount
	v := hangulVBase + (sIndex%hangulNCount)/hangulTCount
	t := hangulTBase + sIndex%hangulTCount
	*dst = append(*dst, l, v)
	if t != hangulTBase {
		*dst = append(*dst, t)
	}
	return true
}

func reorderCanonicalMarks(rs []rune) {
	segmentStart := 0
	for i := 0; i < len(rs); i++ {
		if canonicalCombiningClass(rs[i]) == 0 {
			segmentStart = i + 1
			continue
		}
		for j := i; j > segmentStart && canonicalCombiningClass(rs[j-1]) > canonicalCombiningClass(rs[j]); j-- {
			rs[j-1], rs[j] = rs[j], rs[j-1]
		}
	}
}

func lookupCanonicalDecomposition(r rune) (canonicalDecompositionEntry, bool) {
	i := sort.Search(len(canonicalDecompositionTable), func(i int) bool {
		return canonicalDecompositionTable[i].from >= r
	})
	if i < len(canonicalDecompositionTable) && canonicalDecompositionTable[i].from == r {
		return canonicalDecompositionTable[i], true
	}
	return canonicalDecompositionEntry{}, false
}

func canonicalCombiningClass(r rune) uint8 {
	i := sort.Search(len(canonicalCombiningClassTable), func(i int) bool {
		return canonicalCombiningClassTable[i].r >= r
	})
	if i < len(canonicalCombiningClassTable) && canonicalCombiningClassTable[i].r == r {
		return canonicalCombiningClassTable[i].class
	}
	return 0
}
