package query

import (
	"strings"
	"unicode"

	"github.com/sglavoie/brainnotes/internal/textnorm"
)

// FuzzyTitleCost reports how far a title is from a typed search, for the
// "closest titles" offered when an exact search finds nothing. Both arguments
// must already be folded through FoldText. The cost is the number of single
// rune edits spent, so 0 is a title containing every typed word and a lower
// cost is a closer title; ok is false when the title is not close at all.
//
// It is deliberately not a search predicate. Search stays exact substring
// matching so a saved view and a bulk selection mean precisely what they say;
// this only ranks candidates to offer a person whose search came back empty.
//
// Matching is per word: every typed word must be near some word of the title,
// or near that word's opening runes, which is what lets a half-typed and
// misspelled word ("delibrat") still find its title. The whole strings are
// compared as well, so a missing space ("deepwork") costs one edit.
func FuzzyTitleCost(foldedQuery, foldedTitle string) (cost int, ok bool) {
	queryWords := fuzzyWords(foldedQuery)
	titleWords := fuzzyWords(foldedTitle)
	if len(queryWords) == 0 || len(titleWords) == 0 {
		return 0, false
	}
	whole := textnorm.EditDistance(foldedQuery, foldedTitle)
	wholeOK := whole <= fuzzyBudget(len([]rune(foldedQuery)))

	total := 0
	for _, word := range queryWords {
		best, found := fuzzyWordCost(word, titleWords)
		if !found {
			return whole, wholeOK
		}
		total += best
	}
	if wholeOK && whole < total {
		return whole, true
	}
	return total, true
}

// fuzzyWordCost is the cheapest way word matches any title word, within the
// budget its own length allows.
func fuzzyWordCost(word []rune, titleWords [][]rune) (int, bool) {
	budget := fuzzyBudget(len(word))
	best := budget + 1
	for _, candidate := range titleWords {
		best = min(best, textnorm.EditDistance(string(word), string(candidate)))
		// A typo changes the length too, so the opening that was meant may be
		// a little shorter or longer than what was typed.
		for length := max(1, len(word)-budget); length <= len(word)+budget && length < len(candidate); length++ {
			best = min(best, textnorm.EditDistance(string(word), string(candidate[:length])))
		}
	}
	return best, best <= budget
}

// fuzzyBudget is how many edits a word of n runes may spend. Short words get
// none: one edit turns "to" into most two-letter words.
func fuzzyBudget(n int) int {
	switch {
	case n <= 2:
		return 0
	case n <= 4:
		return 1
	case n <= 8:
		return 2
	default:
		return 3
	}
}

func fuzzyWords(folded string) [][]rune {
	fields := strings.FieldsFunc(folded, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	words := make([][]rune, 0, len(fields))
	for _, field := range fields {
		words = append(words, []rune(field))
	}
	return words
}
