package schema

import "strings"

func wrapAllWikilinks(val any) any {
	switch v := val.(type) {
	case string:
		return wrapSingleWikilink(v)
	case []any:
		out := make([]any, len(v))
		changed := false
		for i, item := range v {
			if s, ok := item.(string); ok {
				w := wrapSingleWikilink(s)
				out[i] = w
				if w != s {
					changed = true
				}
			} else {
				out[i] = item
			}
		}
		if changed {
			return out
		}
		return v
	}
	return val
}

func wrapSingleWikilink(s string) string {
	parts, ok := parseWikilinkRepairParts(s)
	if !ok || parts.full {
		return s
	}
	return formatWikilinkRepairParts(parts, true)
}

func parseWikilinkRepairParts(s string) (wikilinkRepairParts, bool) {
	if s == "" {
		return wikilinkRepairParts{}, false
	}
	starts := strings.HasPrefix(s, "[[")
	ends := strings.HasSuffix(s, "]]")
	openCount := strings.Count(s, "[[")
	closeCount := strings.Count(s, "]]")

	var raw string
	full := false
	switch {
	case starts && ends && len(s) >= 4 && openCount == 1 && closeCount == 1:
		raw = s[2 : len(s)-2]
		full = true
	case starts && !ends && openCount == 1 && closeCount == 0 && len(s) > 2:
		raw = s[2:]
	case !starts && ends && openCount == 0 && closeCount == 1 && len(s) > 2:
		raw = s[:len(s)-2]
	case openCount == 0 && closeCount == 0:
		raw = s
	default:
		return wikilinkRepairParts{}, false
	}

	parts, ok := splitWikilinkRepairContent(raw)
	if !ok {
		return wikilinkRepairParts{}, false
	}
	parts.full = full
	return parts, true
}

func splitWikilinkRepairContent(raw string) (wikilinkRepairParts, bool) {
	if raw == "" || strings.Contains(raw, "[[") || strings.Contains(raw, "]]") {
		return wikilinkRepairParts{}, false
	}

	parts := wikilinkRepairParts{}
	targetPart := raw
	if pipe := strings.IndexByte(targetPart, '|'); pipe >= 0 {
		parts.alias = targetPart[pipe+1:]
		targetPart = targetPart[:pipe]
	}

	if hash := strings.IndexByte(targetPart, '#'); hash >= 0 {
		parts.heading = targetPart[hash+1:]
		targetPart = targetPart[:hash]
	}

	if targetPart == "" {
		return wikilinkRepairParts{}, false
	}

	parts.target = targetPart
	return parts, true
}

func canonicalizeWikilinkString(s string, allowed []string) (string, bool) {
	parts, ok := parseWikilinkRepairParts(s)
	if !ok {
		return "", false
	}

	raw := formatWikilinkRepairParts(parts, false)
	switch {
	case isAllowedValue(raw, allowed), isAllowedValue(parts.target, allowed):
		return s, true
	}

	if canon, ok := canonicalizeString(raw, allowed); ok {
		if parts.full {
			return "[[" + canon + "]]", true
		}
		return canon, true
	}

	if canon, ok := canonicalizeString(parts.target, allowed); ok {
		parts.target = canon
		return formatWikilinkRepairParts(parts, parts.full), true
	}

	return "", false
}

func formatWikilinkRepairParts(parts wikilinkRepairParts, full bool) string {
	raw := parts.target
	if parts.heading != "" {
		raw += "#" + parts.heading
	}
	if parts.alias != "" {
		raw += "|" + parts.alias
	}
	if full {
		return "[[" + raw + "]]"
	}
	return raw
}
