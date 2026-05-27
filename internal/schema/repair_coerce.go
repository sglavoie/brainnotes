package schema

import (
	"math"
	"math/bits"
	"strconv"
	"strings"
	"time"
)

// TryCoerce attempts a lossless conversion of val to targetType. Returns
// the converted value and true on success. Follows the same coercion
// rules as `bn set` (bool, int, float, date, string, list promotion).
func TryCoerce(val any, targetType string) (any, bool) {
	switch targetType {
	case "bool":
		if s, ok := val.(string); ok {
			switch strings.ToLower(s) {
			case "true":
				return true, true
			case "false":
				return false, true
			}
		}
	case "int":
		if s, ok := val.(string); ok {
			if isAutoIntString(s) {
				if n, err := strconv.Atoi(s); err == nil {
					return n, true
				}
			}
		}
		if f, ok := val.(float64); ok {
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return nil, false
			}
			// Use the platform-sized int bounds, not int64. The upper bound is
			// exclusive so float64(MaxInt) rounding on 64-bit platforms still
			// rejects 2^63 (already outside the signed-int range).
			intUpperExclusive := math.Ldexp(1, bits.UintSize-1)
			intLowerInclusive := -intUpperExclusive
			if f >= intUpperExclusive || f < intLowerInclusive {
				return nil, false
			}
			n := int(f)
			if float64(n) == f {
				return n, true
			}
		}
	case "float":
		if s, ok := val.(string); ok {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return f, true
			}
		}
		if n, ok := val.(int); ok {
			if !intExactlyRepresentableAsFloat64(n) {
				return nil, false
			}
			return float64(n), true
		}
	case "date":
		if s, ok := val.(string); ok {
			if t, err := time.Parse("2006-01-02", s); err == nil {
				return t, true
			}
		}
	case "string":
		switch v := val.(type) {
		case bool:
			if v {
				return "true", true
			}
			return "false", true
		case int:
			return strconv.Itoa(v), true
		case float64:
			return scalarToString(v), true
		case time.Time:
			if isDateOnlyTime(v) {
				return v.Format("2006-01-02"), true
			}
		}
	case "list":
		switch v := val.(type) {
		case string:
			return []any{v}, true
		case int:
			return []any{v}, true
		case bool:
			return []any{v}, true
		case float64:
			return []any{v}, true
		case time.Time:
			return []any{v}, true
		}
	}
	return nil, false
}

func isAutoIntString(s string) bool {
	if s == "" {
		return false
	}

	start := 0
	if s[0] == '-' {
		if len(s) == 1 {
			return false
		}
		start = 1
	}

	for i := start; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}

	if len(s)-start > 1 && s[start] == '0' {
		return false
	}
	return true
}

func intExactlyRepresentableAsFloat64(n int) bool {
	n64 := int64(n)
	if n64 == 0 {
		return true
	}

	var magnitude uint64
	if n64 < 0 {
		magnitude = uint64(-(n64 + 1)) + 1
	} else {
		magnitude = uint64(n64)
	}
	oddPart := magnitude >> bits.TrailingZeros64(magnitude)
	return bits.Len64(oddPart) <= 53
}
