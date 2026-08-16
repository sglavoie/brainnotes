package schema

import "strings"

func canonicalizeString(s string, allowed []string) (string, bool) {
	lower := strings.ToLower(s)
	var matches []string
	for _, a := range allowed {
		if strings.ToLower(a) == lower {
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
