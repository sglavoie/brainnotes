package queryexpr

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// CompletionKind says what a caret position expects next.
type CompletionKind string

const (
	// CompleteField means the caret sits where a field name (or a bare word)
	// may start, so the candidate set is the query vocabulary.
	CompleteField CompletionKind = "field"
	// CompleteValue means the caret sits after a field's operator, so the
	// candidate set is that field's values.
	CompleteValue CompletionKind = "value"
)

// Completion is the syntactic context at a caret offset. It is deliberately
// *not* a parse: a query being typed is usually incomplete (`tag:` has no
// value yet, `near:[[Deep` has no closing bracket), and Parse rejects exactly
// the states a completion UI exists to help with. So this scans backwards from
// the caret over the same rune classes the lexer uses and reports what the
// preceding text commits to.
//
// Field, Arg, and Prefix are the pieces a caller needs to source candidates;
// Span is the byte range the chosen candidate replaces, which covers an opening
// quote or `[[` when the caret is inside one so the caller can re-quote freely.
type Completion struct {
	Kind CompletionKind
	// Field is the field whose value is being typed, empty for CompleteField.
	// It is lower-cased, since field lookup is case-insensitive.
	Field string
	// Arg is the field's already-typed qualifier, as in the `created` of
	// `age:created>`. Empty when the term has no argument.
	Arg string
	// Prefix is the partial token under the caret, with any opening quote or
	// `[[` stripped. It is the raw source text, not NFC-normalized: a caller
	// matching it against candidates should fold it the same way it folds them.
	Prefix string
	// Span is the byte range in the source that a candidate replaces.
	Span Span
	// Quoted reports that the caret is inside a "..." phrase.
	Quoted bool
	// Wikilink reports that the caret is inside a [[...]] target.
	Wikilink bool
}

// Field is one entry of the query vocabulary, described for a completion UI.
type Field struct {
	// Name is the reserved field name as it is spelled in a query.
	Name string
	// Hint is a one-line description of what the field selects.
	Hint string
	// NeedsGraph reports that evaluating the field reads the link graph.
	NeedsGraph bool
}

// fieldHints documents the reserved vocabulary for a completion UI. It is a
// separate table from reservedFields so adding a hint never touches a lowering,
// and TestFieldHintsCoverEveryReservedField pins that the two stay in step.
var fieldHints = map[string]string{
	"tag":          "notes carrying a tag",
	"category":     "notes in a frontmatter category",
	"status":       "notes with a status value",
	"type":         "notes with a type (or kind) value",
	"has":          "notes that store a frontmatter property",
	"missing":      "notes that omit a frontmatter property",
	"folder":       "notes under a vault folder",
	"age":          "notes by date, e.g. age>90d or age:created<30d",
	"prop":         "any frontmatter key, e.g. prop:rating>3",
	"pinned":       "pinned notes, spelled pinned:true",
	"orphan":       "notes nothing links to, spelled orphan:true",
	"dead-end":     "notes that link nowhere, spelled dead-end:true",
	"broken-links": "notes with an unresolved wikilink, spelled broken-links:true",
	"backlinks":    "incoming link count, e.g. backlinks>3",
	"links":        "outgoing link count, e.g. links<2",
	"links-to":     "notes linking to a target, e.g. links-to:[[Deep Work]]",
	"linked-from":  "notes a target links to",
	"near":         "notes within N hops of a target, e.g. near:[[Hub]]<=2",
	"path-to":      "notes connected to a target at any distance",
	"degree":       "distinct linked neighbours, e.g. degree>=5",
	"cluster-size": "size of the note's connected component",
}

// Fields returns the whole reserved vocabulary in sorted order, with the hint
// and graph requirement a completion UI needs to present it.
func Fields() []Field {
	out := make([]Field, 0, len(reservedFields))
	for name, f := range reservedFields {
		out = append(out, Field{Name: name, Hint: fieldHints[name], NeedsGraph: f.needsGraph})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// IsReservedField reports whether name is part of the reserved vocabulary.
// Lookup is case-insensitive, matching the compiler.
func IsReservedField(name string) bool {
	_, ok := lookupReserved(name)
	return ok
}

// CompletionAt reports the completion context at a byte offset into src. pos is
// clamped into range and moved back to a rune boundary, so a caller converting
// a caret from another offset unit cannot produce a panic or a split rune.
func CompletionAt(src string, pos int) Completion {
	pos = alignOffset(src, pos)

	if quoteStart, ok := openQuoteBefore(src, pos); ok {
		c := Completion{
			Kind:   CompleteValue,
			Prefix: src[quoteStart+1 : pos],
			Span:   Span{Start: quoteStart, End: pos},
			Quoted: true,
		}
		c.Field, c.Arg, c.Kind = fieldBefore(src, quoteStart)
		return c
	}

	start := wordStartBefore(src, pos)
	c := Completion{Prefix: src[start:pos], Span: Span{Start: start, End: pos}}
	// A wikilink target may contain spaces, so the run of word runes under the
	// caret is only its last word: take the whole target instead.
	if linkStart, ok := openWikilinkBefore(src, start); ok {
		start = linkStart
		c.Prefix = src[linkStart+2 : pos]
		c.Span.Start = linkStart
		c.Wikilink = true
	}
	c.Field, c.Arg, c.Kind = fieldBefore(src, start)
	return c
}

// fieldBefore inspects the text ending at start and reports the field whose
// value would begin there. A value position requires the operator to be
// *adjacent* to the caret's token: `tag:wip` is a value, while `tag: wip` is a
// value the parser would accept but which reads as a fresh word to a person
// typing, and guessing wrong there offers tag values where field names belong.
func fieldBefore(src string, start int) (field, arg string, kind CompletionKind) {
	i := skipListPrefix(src, start)
	i, ok := skipOperatorBefore(src, i)
	if !ok {
		return "", "", CompleteField
	}

	text, i, parenthesised, ok := atomBefore(src, i)
	if !ok {
		return "", "", CompleteField
	}

	// `age:created>90d`, `near:[[Hub]]<=2`, and `age(created)>90d`: the atom
	// before the caret's operator is the term's *argument* whenever a field
	// name introduces it, which is the parser's own one-token-lookahead rule
	// read backwards.
	if parenthesised {
		if name, _, ok := wordBefore(src, i); ok {
			return strings.ToLower(name), text, CompleteValue
		}
		return "", "", CompleteField
	}
	if i > 0 && src[i-1] == ':' {
		if name, _, ok := wordBefore(src, i-1); ok {
			return strings.ToLower(name), text, CompleteValue
		}
	}
	return strings.ToLower(text), "", CompleteValue
}

// atomBefore reads the atom ending at i — a wikilink, a quoted phrase, a
// parenthesised argument, or a bare word — and reports its text, the offset it
// starts at, and whether it was parenthesised.
func atomBefore(src string, i int) (text string, start int, parenthesised bool, ok bool) {
	switch {
	case i >= 4 && strings.HasSuffix(src[:i], "]]"):
		open := strings.LastIndex(src[:i-2], "[[")
		if open < 0 {
			return "", i, false, false
		}
		return src[open+2 : i-2], open, false, true
	case i >= 2 && src[i-1] == '"':
		open, found := openQuoteBefore(src, i-1)
		if !found {
			return "", i, false, false
		}
		return src[open+1 : i-1], open, false, true
	case i >= 2 && src[i-1] == ')':
		depth := 0
		for j := i - 1; j > 0; j-- {
			switch src[j-1] {
			case ')':
				depth++
			case '(':
				if depth == 0 {
					return src[j : i-1], j - 1, true, true
				}
				depth--
			}
		}
		return "", i, false, false
	default:
		word, start, ok := wordBefore(src, i)
		return word, start, false, ok
	}
}

// skipListPrefix walks back over an in-progress list value (`tag:[a,b,`) so the
// caret still resolves to the field the list belongs to. It only engages when
// the caret's token directly follows `[` or `,`; anywhere else a bracket is
// either closed or part of a wikilink the caller already stepped over.
func skipListPrefix(src string, i int) int {
	if i <= 0 || (src[i-1] != '[' && src[i-1] != ',') {
		return i
	}
	depth := 0
	for j := i; j > 0; j-- {
		switch src[j-1] {
		case ']':
			depth++
		case '[':
			if depth == 0 {
				return j - 1
			}
			depth--
		}
	}
	return i
}

// skipOperatorBefore steps back over a comparison operator ending at i.
func skipOperatorBefore(src string, i int) (int, bool) {
	if i <= 0 {
		return i, false
	}
	switch src[i-1] {
	case ':':
		return i - 1, true
	case '=':
		if i >= 2 {
			switch src[i-2] {
			case '!', '<', '>', '=':
				return i - 2, true
			}
		}
		return i - 1, true
	case '<', '>':
		return i - 1, true
	}
	return i, false
}

// wordBefore reads the bare word ending at i, returning it and the offset it
// starts at.
func wordBefore(src string, i int) (string, int, bool) {
	start := wordStartBefore(src, i)
	if start == i {
		return "", i, false
	}
	return src[start:i], start, true
}

// wordStartBefore finds the start of the run of word runes ending at pos, using
// the lexer's own rune classes so `links-to` and `2026-05-19` stay whole. A
// leading `-` is dropped: at a token boundary it is negation, not a word.
func wordStartBefore(src string, pos int) int {
	start := pos
	for start > 0 {
		r, size := utf8.DecodeLastRuneInString(src[:start])
		if !isWordRune(r) {
			break
		}
		start -= size
	}
	if start < pos && src[start] == '-' {
		if start == 0 {
			return start + 1
		}
		if prev, _ := utf8.DecodeLastRuneInString(src[:start]); !isWordRune(prev) {
			return start + 1
		}
	}
	return start
}

// openQuoteBefore reports the offset of an unterminated `"` opened before pos.
func openQuoteBefore(src string, pos int) (int, bool) {
	open := -1
	for i := 0; i < pos; i++ {
		switch src[i] {
		case '\\':
			if open >= 0 {
				i++
			}
		case '"':
			if open >= 0 {
				open = -1
			} else {
				open = i
			}
		}
	}
	if open < 0 {
		return 0, false
	}
	return open, true
}

// openWikilinkBefore reports the offset of a `[[` opened immediately before
// start with no closing `]]` between it and start.
func openWikilinkBefore(src string, start int) (int, bool) {
	open := strings.LastIndex(src[:start], "[[")
	if open < 0 {
		return 0, false
	}
	if strings.Contains(src[open:start], "]]") {
		return 0, false
	}
	return open, true
}

// alignOffset clamps pos into src and moves it back to a rune boundary.
func alignOffset(src string, pos int) int {
	if pos < 0 {
		return 0
	}
	if pos > len(src) {
		return len(src)
	}
	for pos > 0 && pos < len(src) && !utf8.RuneStart(src[pos]) {
		pos--
	}
	return pos
}

// Fields used by an expression, for a caller that wants to report which parts
// of the vocabulary a query touches.
//
// FieldsUsed walks the parsed tree and returns every named field, lower-cased,
// deduplicated, and sorted. A bare word or quoted phrase is free text and names
// no field, so it contributes nothing; `prop:rating` contributes prop, since
// prop is the field and rating is its argument.
func FieldsUsed(expr Expr) []string {
	seen := map[string]struct{}{}
	var walk func(Expr)
	walk = func(e Expr) {
		switch node := e.(type) {
		case nil:
			return
		case *And:
			for _, operand := range node.Operands {
				walk(operand)
			}
		case *Or:
			for _, operand := range node.Operands {
				walk(operand)
			}
		case *Not:
			walk(node.Operand)
		case *Term:
			if node.Field == "" {
				return
			}
			seen[strings.ToLower(node.Field)] = struct{}{}
		}
	}
	walk(expr)

	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
