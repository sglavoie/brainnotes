package schema

import (
	"math"
	"strconv"
	"testing"
)

func TestTryCoerce_FloatOverflowToInt(t *testing.T) {
	// A float64 value larger than MaxInt64 should not be coerced to int.
	_, ok := TryCoerce(float64(math.MaxInt64)+2048, "int")
	if ok {
		t.Error("TryCoerce(>MaxInt64, int) should return false")
	}

	// A float64 value smaller than MinInt64 should not be coerced to int.
	_, ok = TryCoerce(float64(math.MinInt64)-2048, "int")
	if ok {
		t.Error("TryCoerce(<MinInt64, int) should return false")
	}

	// 1e19 exceeds MaxInt64 (~9.2e18).
	_, ok = TryCoerce(1e19, "int")
	if ok {
		t.Error("TryCoerce(1e19, int) should return false")
	}

	// -1e19 is below MinInt64.
	_, ok = TryCoerce(-1e19, "int")
	if ok {
		t.Error("TryCoerce(-1e19, int) should return false")
	}

	// float64(MaxInt64) is 2^63 (one beyond MaxInt64); must be rejected
	// even though the round-trip float64(int(f)) == f would pass due
	// to precision loss.
	_, ok = TryCoerce(float64(math.MaxInt64), "int")
	if ok {
		t.Error("TryCoerce(float64(MaxInt64), int) should return false")
	}

	// The largest float64 strictly less than 2^63 should still work.
	prev := math.Nextafter(float64(math.MaxInt64), 0)
	got, ok := TryCoerce(prev, "int")
	if !ok {
		t.Error("TryCoerce(prev-of-MaxInt64, int) should return true")
	}
	if n, isInt := got.(int); !isInt || float64(n) != prev {
		t.Errorf("TryCoerce(prev-of-MaxInt64, int) = %v, expected lossless int", got)
	}
}

func TestTryCoerce_FloatNaNToInt(t *testing.T) {
	_, ok := TryCoerce(math.NaN(), "int")
	if ok {
		t.Error("TryCoerce(NaN, int) should return false")
	}
}

func TestTryCoerce_FloatInfToInt(t *testing.T) {
	_, ok := TryCoerce(math.Inf(1), "int")
	if ok {
		t.Error("TryCoerce(+Inf, int) should return false")
	}
	_, ok = TryCoerce(math.Inf(-1), "int")
	if ok {
		t.Error("TryCoerce(-Inf, int) should return false")
	}
}

func TestTryCoerce_IntPrecisionLossToFloat(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("requires 64-bit int")
	}

	exact := int(1 << 53)
	got, ok := TryCoerce(exact, "float")
	if !ok {
		t.Fatal("TryCoerce(1<<53, float) should return true")
	}
	if got != float64(exact) {
		t.Errorf("TryCoerce(1<<53, float) = %v, want %v", got, float64(exact))
	}

	exactLargePowerOfTwo := int(1 << 60)
	got, ok = TryCoerce(exactLargePowerOfTwo, "float")
	if !ok {
		t.Fatal("TryCoerce(1<<60, float) should return true")
	}
	if got != float64(exactLargePowerOfTwo) {
		t.Errorf("TryCoerce(1<<60, float) = %v, want %v", got, float64(exactLargePowerOfTwo))
	}

	inexact := int(1<<53) + 1
	_, ok = TryCoerce(inexact, "float")
	if ok {
		t.Error("TryCoerce((1<<53)+1, float) should return false")
	}
}
