package cli

import "strings"

// shellQuote renders s as one POSIX shell word, for a command bn suggests the
// user run. A word made only of characters no shell treats specially is left
// bare; anything else is double-quoted when that is safe, the way bn's own
// help writes `bn find --view "Active work"`, and single-quoted (with any
// embedded single quote spliced as '\”) when it contains a character double
// quotes would still expand.
func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, needsShellQuote) == -1 {
		return s
	}
	if !strings.ContainsAny(s, "$`\\\"!") {
		return `"` + s + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// needsShellQuote reports a rune a shell could treat specially.
func needsShellQuote(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	case strings.ContainsRune("-_./:@%+=,", r):
		return false
	}
	return true
}
