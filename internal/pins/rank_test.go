package pins

import (
	"math"
	"testing"
)

func TestMidpoint(t *testing.T) {
	cases := []struct {
		name   string
		lo, hi float64
		want   float64
		wantOK bool
	}{
		{name: "unit gap", lo: 1, hi: 2, want: 1.5, wantOK: true},
		{name: "negative gap", lo: -2, hi: -1, want: -1.5, wantOK: true},
		{name: "across zero", lo: -1, hi: 1, want: 0, wantOK: true},
		{name: "extreme pair does not overflow", lo: -math.MaxFloat64, hi: math.MaxFloat64, want: 0, wantOK: true},
		{name: "equal neighbours", lo: 1, hi: 1},
		{name: "inverted neighbours", lo: 2, hi: 1},
		{name: "adjacent floats", lo: 1, hi: math.Nextafter(1, 2)},
		{name: "NaN", lo: math.NaN(), hi: 1},
		{name: "infinity", lo: 1, hi: math.Inf(1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := midpoint(tc.lo, tc.hi)
			if ok != tc.wantOK {
				t.Fatalf("midpoint(%v, %v) ok = %v, want %v", tc.lo, tc.hi, ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Fatalf("midpoint(%v, %v) = %v, want %v", tc.lo, tc.hi, got, tc.want)
			}
			if ok && (got <= tc.lo || got >= tc.hi) {
				t.Fatalf("midpoint(%v, %v) = %v, not strictly between", tc.lo, tc.hi, got)
			}
		})
	}
}

func TestNextRank(t *testing.T) {
	cases := []struct {
		name   string
		max    float64
		want   float64
		wantOK bool
	}{
		{name: "unit step", max: 1, want: 2, wantOK: true},
		{name: "negative", max: -3.5, want: -2.5, wantOK: true},
		{name: "huge value cannot step", max: math.MaxFloat64},
		{name: "NaN", max: math.NaN()},
		{name: "infinity", max: math.Inf(1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := nextRank(tc.max)
			if ok != tc.wantOK {
				t.Fatalf("nextRank(%v) ok = %v, want %v", tc.max, ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Fatalf("nextRank(%v) = %v, want %v", tc.max, got, tc.want)
			}
		})
	}
}

// exhaustGap splits (lo, hi) toward lo until float64 has nothing left to give,
// returning the collapsed pair and the number of splits it took.
func exhaustGap(t *testing.T, lo, hi float64) (float64, float64, int) {
	t.Helper()
	splits := 0
	for splits < 1000 {
		mid, ok := midpoint(lo, hi)
		if !ok {
			return lo, hi, splits
		}
		hi = mid
		splits++
	}
	t.Fatalf("gap between %v and %v never collapsed", lo, hi)
	return 0, 0, 0
}

func TestMidpoint_GapCollapsesAfterAboutFiftySplits(t *testing.T) {
	_, _, splits := exhaustGap(t, 1.0, 2.0)
	if splits < 40 || splits > 60 {
		t.Fatalf("gap collapsed after %d splits, want roughly 50", splits)
	}
}
