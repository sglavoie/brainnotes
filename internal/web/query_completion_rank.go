package web

import (
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/queryexpr"
	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
)

// rankQueryCompletions filters candidates by the caret's prefix and orders
// them: prefix matches first (a user typing "wi" means it starts with "wi"),
// then by how many notes back the value, then by display spelling.
func rankQueryCompletions(candidates []candidate, c queryexpr.Completion) []queryCompletion {
	prefix := foldQueryValue(c.Prefix)

	type scored struct {
		candidate
		exactPrefix bool
	}
	matches := make([]scored, 0, len(candidates))
	for _, cand := range candidates {
		if prefix == "" {
			matches = append(matches, scored{candidate: cand, exactPrefix: true})
			continue
		}
		folded := foldQueryValue(cand.value)
		switch {
		case strings.HasPrefix(folded, prefix):
			matches = append(matches, scored{candidate: cand, exactPrefix: true})
		case strings.Contains(folded, prefix):
			matches = append(matches, scored{candidate: cand})
		}
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].exactPrefix != matches[j].exactPrefix {
			return matches[i].exactPrefix
		}
		if matches[i].count != matches[j].count {
			return matches[i].count > matches[j].count
		}
		return facetDisplayLess(matches[i].value, matches[j].value)
	})

	if len(matches) > queryCompletionLimit {
		matches = matches[:queryCompletionLimit]
	}

	out := make([]queryCompletion, 0, len(matches))
	for _, match := range matches {
		out = append(out, queryCompletion{
			Value:  match.value,
			Insert: queryCompletionInsert(match.candidate, c),
			Kind:   match.kind,
			Detail: match.detail,
			Count:  match.count,
		})
	}
	return out
}

// queryCompletionInsert renders the text that replaces Completion.Span. A field
// name gains the ":" that starts its value; a value is bracketed, quoted, or
// left bare exactly as the lexer requires to read it back as one atom.
func queryCompletionInsert(cand candidate, c queryexpr.Completion) string {
	if c.Kind == queryexpr.CompleteField {
		return cand.value + ":"
	}
	if cand.wikilink || c.Wikilink {
		return queryexpr.Atom{Text: cand.value, Wikilink: true}.String()
	}
	if c.Quoted {
		// The span covers the opening quote the user already typed, so the
		// replacement has to supply both.
		return quoteQueryValue(cand.value)
	}
	return queryexpr.Atom{Text: cand.value}.String()
}

// quoteQueryValue wraps a value in the phrase quoting the lexer understands.
// Only `\"` and `\\` are escapes there, so nothing else may be escaped.
func quoteQueryValue(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

// foldQueryValue folds a value for matching the way the compiler compares one:
// NFC first, then case. The vault is NFC-normalized and the lexer normalizes
// atom text, so a decomposed prefix must still match a composed value.
func foldQueryValue(value string) string {
	return strings.ToLower(textnorm.NFC(strings.TrimSpace(value)))
}
