package cli

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// splitListValues splits a raw comma-separated string into coerced Go-native
// values, dropping empty entries (matching the legacy '=' behavior).
func splitListValues(rawVal, typeFlag string) ([]any, error) {
	parts := strings.Split(rawVal, ",")
	vals := make([]any, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		value, err := coerceValue(trimmed, typeFlag)
		if err != nil {
			return nil, err
		}
		vals = append(vals, value)
	}
	return vals, nil
}

// wikilinkNormalized returns v with each string element wrapped as `[[X]]`
// when key is a wikilink-typed property. It mirrors the wrap step performed
// by frontmatter.Marshal so an `opSet` comparison can detect that the new
// value would round-trip to the same on-disk bytes as the current value.
// Without this, `bn set parent=Foo` would always rewrite a note whose stored
// value is `[[Foo]]` because the raw user input differs byte-for-byte.
func wikilinkNormalized(v any, key string, fm *config.FrontmatterConfig) any {
	if fm == nil || !containsString(fm.WikilinkProperties, key) {
		return v
	}

	switch vv := v.(type) {
	case string:
		return wrapWikilinkValue(vv)
	case []any:
		return wrapWikilinkValues(vv)
	default:
		return v
	}
}

func wrapWikilinkValues(vals []any) []any {
	out := make([]any, len(vals))
	for i, item := range vals {
		if s, ok := item.(string); ok {
			out[i] = wrapWikilinkValue(s)
			continue
		}
		out[i] = item
	}
	return out
}

// wrapWikilinkValue wraps a bare string in `[[...]]` unless it is empty or
// already wrapped. Mirrors the unexported helper in internal/frontmatter so
// the CLI can normalize input before delegating to ListAdd/ListRemove, which
// compare values byte-for-byte.
func wrapWikilinkValue(s string) string {
	if s == "" {
		return s
	}
	if strings.HasPrefix(s, "[[") && strings.HasSuffix(s, "]]") {
		return s
	}
	return "[[" + s + "]]"
}

var (
	// intValueRe accepts an optional leading `-` so negative integers
	// auto-coerce. Before the fix `rating=-3` was stored as the string "-3".
	intValueRe  = regexp.MustCompile(`^-?\d+$`)
	dateValueRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// coerceValue converts a raw string to a Go-native value. When typeFlag is
// empty, the order of auto-detection matches the spec: bool > int > date >
// string. "raw" forces the literal string with no coercion.
func coerceValue(s, typeFlag string) (any, error) {
	switch typeFlag {
	case "date":
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return nil, fmt.Errorf("set: invalid date %q: %w", s, err)
		}
		return t, nil
	case "int":
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil, fmt.Errorf("set: invalid int %q: %w", s, err)
		}
		return n, nil
	case "bool":
		switch strings.ToLower(s) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return nil, fmt.Errorf("set: invalid bool %q (expected true or false)", s)
	case "raw":
		return s, nil
	case "", "auto":
	// fall through to auto-detect
	default:
		return nil, fmt.Errorf("set: unknown --type %q (want date|int|bool|raw)", typeFlag)
	}

	switch strings.ToLower(s) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	if intValueRe.MatchString(s) {
		// Strings with a leading zero (e.g. "01", "007") are kept as strings to
		// preserve the original formatting. Auto-coercing would silently drop
		// the leading zero — surprising for version numbers, IDs, and the
		// like. Use `--type int` to force coercion. "-0", "0", "-1" still
		// coerce.
		hasLeadingZero := len(s) > 1 && s[0] == '0'
		if s[0] == '-' && len(s) > 2 && s[1] == '0' {
			hasLeadingZero = true
		}
		if !hasLeadingZero {
			if n, err := strconv.Atoi(s); err == nil {
				return n, nil
			}
		}
	}
	if dateValueRe.MatchString(s) {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return t, nil
		}
	}
	return s, nil
}
