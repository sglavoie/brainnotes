package schema

import (
	"math"
	"strconv"
	"testing"
)

func TestTryCoerce_FloatOverflowToInt(t *testing.T) {
	intUpperExclusive := math.Ldexp(1, strconv.IntSize-1)
	intLowerInclusive := -intUpperExclusive

	// A float64 value larger than the platform int range should not be coerced to int.
	_, ok := TryCoerce(intUpperExclusive+2048, "int")
	if ok {
		t.Error("TryCoerce(>MaxInt, int) should return false")
	}

	// A float64 value smaller than the platform int range should not be coerced to int.
	_, ok = TryCoerce(intLowerInclusive-2048, "int")
	if ok {
		t.Error("TryCoerce(<MinInt, int) should return false")
	}

	// Two exact powers-of-two beyond the upper bound must be rejected on every platform.
	_, ok = TryCoerce(intUpperExclusive*4, "int")
	if ok {
		t.Error("TryCoerce(4*upperExclusive, int) should return false")
	}

	_, ok = TryCoerce(intLowerInclusive*4, "int")
	if ok {
		t.Error("TryCoerce(4*lowerInclusive, int) should return false")
	}

	// The exclusive upper bound itself must be rejected even though it is an
	// exactly representable float64.
	_, ok = TryCoerce(intUpperExclusive, "int")
	if ok {
		t.Error("TryCoerce(upperExclusive, int) should return false")
	}

	// The largest float64 strictly less than the exclusive upper bound should still work.
	prev := math.Nextafter(intUpperExclusive, 0)
	got, ok := TryCoerce(prev, "int")
	if !ok {
		t.Error("TryCoerce(prev-of-upperExclusive, int) should return true")
	}
	if n, isInt := got.(int); !isInt || float64(n) != prev {
		t.Errorf("TryCoerce(prev-of-upperExclusive, int) = %v, expected lossless int", got)
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
