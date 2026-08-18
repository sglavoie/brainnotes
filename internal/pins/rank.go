package pins

import "math"

// midpoint returns a rank strictly between lo and hi. The bool is false when
// float64 holds no such value — the gap has collapsed after roughly 50
// successive splits — and the caller must renormalize instead.
func midpoint(lo, hi float64) (float64, bool) {
	if !isFinite(lo) || !isFinite(hi) || !(lo < hi) {
		return 0, false
	}
	mid := lo + (hi-lo)/2
	if !isFinite(mid) {
		// hi-lo overflowed to +Inf on an extreme pair; averaging the halves
		// cannot overflow.
		mid = lo/2 + hi/2
	}
	if !isFinite(mid) || mid <= lo || mid >= hi {
		return 0, false
	}
	return mid, true
}

// nextRank returns the rank appended after max. The bool is false when max is
// large enough that adding 1.0 is not representable as a distinct float64.
func nextRank(max float64) (float64, bool) {
	if !isFinite(max) {
		return 0, false
	}
	next := max + 1.0
	if !isFinite(next) || next <= max {
		return 0, false
	}
	return next, true
}

func isFinite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}
