package wikilink

import "strings"

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
// Both `[[OldTitle]]` and `[[Folder/OldTitle]]` are rewritten.
func RewriteTitle(text, oldTitle, newTitle string) string {
	return Rewrite(text, func(l Link) (string, bool) {
		if !strings.EqualFold(l.Title(), oldTitle) {
			return "", false
		}
		newTarget := newTitle
		if l.Folder != "" {
			newTarget = l.Folder + "/" + newTitle
		}
		return FormatLink(newTarget, l.Heading, l.Alias), true
	})
}

// RewriteMove updates folder-prefixed wikilinks whose Target matches oldPath
// (case-insensitive) to point at newPath. Bare-title links without a folder
// prefix are left untouched — moving a file across folders only invalidates
// links that explicitly named the old folder.
func RewriteMove(text, oldPath, newPath string) string {
	return Rewrite(text, func(l Link) (string, bool) {
		if l.Folder == "" {
			return "", false
		}
		if !strings.EqualFold(l.Target, oldPath) {
			return "", false
		}
		return FormatLink(newPath, l.Heading, l.Alias), true
	})
}
