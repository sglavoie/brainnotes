package schema

import (
	"strings"

	"github.com/sglavoie/brainnotes/internal/textnorm"
)

// allowedKey is the form a value and an allowed spelling are compared in when
// repair looks for the allowed spelling a value was meant to be: NFC, then
// lowercased, the key query's tag/status/type predicates use. Without the NFC
// step a decomposed `café` written by a macOS tool matched no allowed entry
// and was reported value_not_allowed instead of being canonicalized.
func allowedKey(s string) string { return strings.ToLower(textnorm.NFC(s)) }

// canonicalizeString returns the single allowed spelling s matches under
// allowedKey. The allowed list's spelling wins, so repair rewrites a value to
// exactly the bytes lint then accepts.
func canonicalizeString(s string, allowed []string) (string, bool) {
	lower := allowedKey(s)
	var matches []string
	for _, a := range allowed {
		if allowedKey(a) == lower {
			matches = append(matches, a)
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return "", false
}

func canonicalizeAll(val any, allowed []string, isWikilink bool) (any, []string) {
	switch v := val.(type) {
	case string:
		if isWikilink {
			canon, ok := canonicalizeWikilinkString(v, allowed)
			if !ok {
				return val, []string{stripWikilink(v)}
			}
			return canon, nil
		}
		if isAllowedValue(v, allowed) {
			return val, nil
		}
		canon, ok := canonicalizeString(v, allowed)
		if !ok {
			return val, []string{v}
		}
		return canon, nil
	case []any:
		out := make([]any, len(v))
		copy(out, v)
		changed := false
		var unfixable []string
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				scalar := scalarToString(item)
				if scalar == "" {
					unfixable = appendUniqueString(unfixable, unsupportedValueLabel(item))
					continue
				}
				if isAllowedValue(scalar, allowed) {
					continue
				}
				unfixable = appendUniqueString(unfixable, scalar)
				continue
			}
			if isWikilink {
				canon, ok := canonicalizeWikilinkString(s, allowed)
				if ok {
					if canon != s {
						out[i] = canon
						changed = true
					}
					continue
				}
				unfixable = appendUniqueString(unfixable, stripWikilink(s))
				continue
			}
			if isAllowedValue(s, allowed) {
				continue
			}
			canon, ok := canonicalizeString(s, allowed)
			if !ok {
				unfixable = appendUniqueString(unfixable, s)
				continue
			}
			out[i] = canon
			changed = true
		}
		if changed {
			return out, unfixable
		}
		return val, unfixable
	default:
		// For non-string scalar types (int, bool, float64, time.Time),
		// convert to the canonical string representation and check
		// against the allowed list. Case-insensitive canonicalization
		// is not applicable to numeric/bool/date types, so mismatches
		// are reported as unfixable.
		s := scalarToString(v)
		if s == "" {
			return val, nil
		}
		if isAllowedValue(s, allowed) {
			return val, nil
		}
		return val, []string{s}
	}
}
