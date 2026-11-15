package textnorm

// EditDistance returns the optimal string alignment distance between a and b,
// counting a transposition of two adjacent runes as one edit.
//
// It is spelled out here rather than pulled in: a fifty-line matrix over a pair
// of short names is not worth another dependency. Comparison is over runes, so
// an accented letter costs one edit rather than two. Callers fold and normalize
// both sides first; this function compares exactly what it is given.
func EditDistance(a, b string) int {
	ar := []rune(a)
	br := []rune(b)
	if len(ar) == 0 || len(br) == 0 {
		return len(ar) + len(br)
	}
	rows := newDistanceRows(len(ar), len(br))
	for i := 1; i <= len(ar); i++ {
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			best := min(rows[i-1][j]+1, rows[i][j-1]+1, rows[i-1][j-1]+cost)
			if transposed(ar, br, i, j) {
				best = min(best, rows[i-2][j-2]+1)
			}
			rows[i][j] = best
		}
	}
	return rows[len(ar)][len(br)]
}

// newDistanceRows is the matrix with its first row and column filled in: the
// distance from an empty prefix is the length of the other one.
func newDistanceRows(n, m int) [][]int {
	rows := make([][]int, n+1)
	for i := range rows {
		rows[i] = make([]int, m+1)
		rows[i][0] = i
	}
	for j := 0; j <= m; j++ {
		rows[0][j] = j
	}
	return rows
}

// transposed reports whether the two runes ending at i and j are each other's
// swapped neighbours.
func transposed(ar, br []rune, i, j int) bool {
	return i > 1 && j > 1 && ar[i-1] == br[j-2] && ar[i-2] == br[j-1]
}
