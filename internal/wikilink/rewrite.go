package wikilink

import "strings"

// invalidNewTarget reports whether s would produce a malformed wikilink if
// emitted as the target of `[[ ... ]]`. The check first applies the same
// surrounding ASCII-space normalization Parse applies to wikilink fields.
// Empty strings collapse the link to `[[]]`; brackets and line-terminators
// break the `[[...]]` span on re-parse. `#` and `|` are wikilink separators —
// embedding them in a target would silently introduce a heading or alias on
// re-parse, changing link semantics. Whitespace-only strings, a trailing `/`
// (empty title segment), and bare-or-folder-prefixed `.md` suffixes that leave
// an empty title base are also rejected: Parse would either reject the
// rewritten link or normalize it back to something the rewrite didn't intend.
func invalidNewTarget(s string) bool {
	s = trimASCIISpace(s)
	if s == "" {
		return true
	}
	if strings.ContainsAny(s, "[]#|\r\n") {
		return true
	}
	if strings.HasSuffix(s, "/") {
		return true
	}
	// Reject targets whose final path segment is empty after stripping a
	// trailing .md (case-insensitive). Examples: ".md", "Folder/.md".
	base, _ := splitMDExt(s)
	if base == "" {
		return true
	}
	if idx := strings.LastIndex(base, "/"); idx >= 0 && idx == len(base)-1 {
		return true
	}
	return false
}

// splitMDExt returns s with a trailing case-insensitive `.md` suffix removed
// and a bool indicating whether the suffix was present. Obsidian treats
// `[[Note]]` and `[[Note.md]]` as equivalent link forms, so rename/move must
// match both. The original suffix casing is preserved by the caller when
// re-attaching the extension to the rewritten target.
func splitMDExt(s string) (string, string) {
	if len(s) >= 3 && strings.EqualFold(s[len(s)-3:], ".md") {
		return s[:len(s)-3], s[len(s)-3:]
	}
	return s, ""
}

// Rewrite walks every wikilink in text and asks fn how to handle it. When fn
// returns (replacement, true) the link's full `[[...]]` span is replaced with
// replacement; when it returns (_, false) the original text is preserved.
// Links inside code blocks/spans are skipped by Parse and never reach fn.
func Rewrite(text string, fn func(Link) (string, bool)) string {
	links := Parse(text)
	if len(links) == 0 {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	pos := 0
	for _, l := range links {
		if l.Start < pos {
			// Defensive: regex matches are non-overlapping in source order,
			// so this should never happen. Skip the link if it does.
			continue
		}
		b.WriteString(text[pos:l.Start])
		if replacement, ok := fn(l); ok {
			b.WriteString(replacement)
		} else {
			b.WriteString(text[l.Start:l.End])
		}
		pos = l.End
	}
	b.WriteString(text[pos:])
	return b.String()
}

// FormatLink reassembles a wikilink from its constituent parts. Empty heading
// and alias are omitted.
func FormatLink(target, heading, alias string) string {
	var b strings.Builder
	b.Grow(len(target) + len(heading) + len(alias) + 6)
	b.WriteString("[[")
	b.WriteString(target)
	if heading != "" {
		b.WriteByte('#')
		b.WriteString(heading)
	}
	if alias != "" {
		b.WriteByte('|')
		b.WriteString(alias)
	}
	b.WriteString("]]")
	return b.String()
}

// RewriteTitle renames every wikilink whose title (the part after the last `/`
// in Target, or the whole Target when there is no folder) equals oldTitle
// case-insensitively. The folder prefix, heading, and alias are preserved.
// Both `[[OldTitle]]` and `[[Folder/OldTitle]]` are rewritten. A trailing
// `.md` extension on the link's title is also matched and preserved on the
// rewritten target so `[[Note.md]]` is renamed alongside `[[Note]]`.
func RewriteTitle(text, oldTitle, newTitle string) string {
	oldTitle = trimASCIISpace(oldTitle)
	newTitle = trimASCIISpace(newTitle)
	if invalidNewTarget(newTitle) {
		return text
	}
	oldBase, _ := splitMDExt(oldTitle)
	newBase, _ := splitMDExt(newTitle)
	if oldBase == "" {
		return text
	}
	return Rewrite(text, func(l Link) (string, bool) {
		linkTitle := l.Title()
		linkBase, ext := splitMDExt(linkTitle)
		if !strings.EqualFold(linkBase, oldBase) {
			return "", false
		}
		newTarget := newBase + ext
		if l.Folder != "" {
			newTarget = l.Folder + "/" + newBase + ext
		}
		return FormatLink(newTarget, l.Heading, l.Alias), true
	})
}

// RewriteMove updates folder-prefixed wikilinks whose Target matches oldPath
// (case-insensitive) to point at newPath. Bare-title links without a folder
// prefix are left untouched — moving a file across folders only invalidates
// links that explicitly named the old folder. A trailing `.md` extension on
// the link's target is matched and preserved on the rewritten target.
func RewriteMove(text, oldPath, newPath string) string {
	oldPath = trimASCIISpace(oldPath)
	newPath = trimASCIISpace(newPath)
	if invalidNewTarget(newPath) {
		return text
	}
	oldBase, _ := splitMDExt(oldPath)
	newBase, _ := splitMDExt(newPath)
	if oldBase == "" {
		return text
	}
	return Rewrite(text, func(l Link) (string, bool) {
		if l.Folder == "" {
			return "", false
		}
		targetBase, ext := splitMDExt(l.Target)
		if !strings.EqualFold(targetBase, oldBase) {
			return "", false
		}
		return FormatLink(newBase+ext, l.Heading, l.Alias), true
	})
}
