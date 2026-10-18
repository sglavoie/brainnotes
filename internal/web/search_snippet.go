package web

import (
	"html"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var snippetWikilink = regexp.MustCompile(`!?\[\[([^\]\n]+)\]\]`)
var snippetMarkdownLink = regexp.MustCompile(`!?\[([^\]\n]+)\]\([^\)\n]+\)`)
var snippetLinePrefix = regexp.MustCompile(`(?m)^[ \t]{0,3}(?:#{1,6}[ \t]+|>[ \t]?|[-+*][ \t]+|[0-9]+[.)][ \t]+)`)

// Snippets are presentation only. Matching still uses the original note body.
// Escape every note-supplied byte before adding our own highlight markup.
func makeSearchSnippet(body, query string) string {
	plain := snippetWikilink.ReplaceAllStringFunc(body, func(link string) string {
		target := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(link, "!"), "[["), "]]")
		if _, label, ok := strings.Cut(target, "|"); ok {
			return label
		}
		return target
	})
	plain = snippetMarkdownLink.ReplaceAllString(plain, "$1")
	plain = snippetLinePrefix.ReplaceAllString(plain, "")
	runes := []rune(strings.Join(strings.Fields(plain), " "))
	needle := []rune(strings.ToLower(query))
	start, end := snippetWindow(runes, needle)
	result := highlightSnippet(runes[start:end], needle)
	if start > 0 {
		result = "…" + result
	}
	if end < len(runes) {
		result += "…"
	}
	return result
}

func snippetWindow(runes, needle []rune) (int, int) {
	match := 0
	folded := strings.ToLower(string(runes))
	if index := strings.Index(folded, string(needle)); index >= 0 {
		match = utf8.RuneCountInString(folded[:index])
	}
	width := max(searchSnippetRunes, len(needle))
	start := max(0, match-(width-len(needle))/2)
	end := min(len(runes), start+width)
	// Complete boundary words, with a bounded extension for paths and long tokens.
	for n := 0; start > 0 && !unicode.IsSpace(runes[start-1]) && n < 24; n++ {
		start--
	}
	for n := 0; end < len(runes) && !unicode.IsSpace(runes[end]) && n < 24; n++ {
		end++
	}
	return start, end
}

func highlightSnippet(text, needle []rune) string {
	var out strings.Builder
	folded := strings.ToLower(string(text))
	query := string(needle)
	for len(needle) > 0 {
		index := strings.Index(folded, query)
		if index < 0 {
			break
		}
		start := utf8.RuneCountInString(folded[:index])
		out.WriteString(html.EscapeString(string(text[:start])))
		out.WriteString("<mark>" + html.EscapeString(string(text[start:start+len(needle)])) + "</mark>")
		text = text[start+len(needle):]
		folded = folded[index+len(query):]
	}
	out.WriteString(html.EscapeString(string(text)))
	return out.String()
}
