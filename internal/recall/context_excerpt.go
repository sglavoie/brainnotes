package recall

import (
	"sort"
	"strings"
	"unicode/utf8"
)

type scoredContextBlock struct {
	index            int
	score            float64
	distinctiveTerms []string
}

func rankedContextExcerpts(
	source Document,
	candidate Document,
	result Result,
	lexical *LexicalIndex,
) []ContextExcerpt {
	match, _ := lexical.Compare(source.Path, candidate.Path)
	distinctiveWeights := make(map[string]float64, len(match.Terms))
	for _, term := range match.Terms {
		distinctiveWeights[term.Term] = 1 + term.Contribution
	}

	reasonWeights := make(map[string]float64)
	for _, reason := range result.Reasons {
		weight := 0.25 + max(reason.Contribution, 0)
		for _, term := range Tokenize(reason.Detail) {
			if weight > reasonWeights[term] {
				reasonWeights[term] = weight
			}
		}
	}

	scored := make([]scoredContextBlock, 0, len(candidate.Blocks))
	for index, block := range candidate.Blocks {
		sourceBlock, ok := candidate.BlockSource(index)
		if !ok || sourceBlock == "" || !utf8.ValidString(sourceBlock) {
			continue
		}
		blockTerms := make(map[string]struct{})
		for _, term := range Tokenize(block.Text) {
			blockTerms[term] = struct{}{}
		}
		blockScore := 0.0
		matchedDistinctive := make([]string, 0)
		for _, term := range match.Terms {
			if _, ok := blockTerms[term.Term]; !ok {
				continue
			}
			blockScore += distinctiveWeights[term.Term]
			matchedDistinctive = append(matchedDistinctive, term.Term)
		}
		for _, term := range sortedFloatKeys(reasonWeights) {
			if _, ok := blockTerms[term]; ok {
				blockScore += reasonWeights[term]
			}
		}
		scored = append(scored, scoredContextBlock{
			index:            index,
			score:            blockScore,
			distinctiveTerms: matchedDistinctive,
		})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		left := candidate.Blocks[scored[i].index].Location
		right := candidate.Blocks[scored[j].index].Location
		if left.Start != right.Start {
			return left.Start < right.Start
		}
		return scored[i].index < scored[j].index
	})

	reasons := make([]ContextReason, len(result.Reasons))
	for index, reason := range result.Reasons {
		reasons[index] = ContextReason{
			Kind:         reason.Kind,
			Contribution: reason.Contribution,
			Detail:       reason.Detail,
		}
	}
	excerpts := make([]ContextExcerpt, 0, min(MaxExcerptsPerRelatedNote, len(scored)))
	for _, selected := range scored[:min(MaxExcerptsPerRelatedNote, len(scored))] {
		block := candidate.Blocks[selected.index]
		sourceBlock, _ := candidate.BlockSource(selected.index)
		markdown, truncated := truncateContextRunes(sourceBlock, MaxExcerptRunes)
		if markdown == candidate.Body {
			markdown, truncated = excludeWholeRelatedBody(markdown)
		}
		if markdown == "" {
			continue
		}
		excerpt := ContextExcerpt{
			Path:             candidate.Path,
			Title:            candidate.Title,
			StartLine:        block.Location.StartLine,
			EndLine:          block.Location.StartLine + strings.Count(markdown, "\n"),
			Markdown:         markdown,
			Truncated:        truncated,
			DistinctiveTerms: append([]string(nil), selected.distinctiveTerms...),
			Reasons:          append([]ContextReason(nil), reasons...),
		}
		excerpt.DistinctiveTerms = presentContextTerms(excerpt.Markdown, excerpt.DistinctiveTerms)
		excerpts = append(excerpts, excerpt)
	}
	return excerpts
}

func truncateContextRunes(value string, maximum int) (string, bool) {
	if maximum < 0 {
		maximum = 0
	}
	if utf8.RuneCountInString(value) <= maximum {
		return value, false
	}
	if maximum == 0 {
		return "", true
	}
	count := 0
	for index := range value {
		if count == maximum {
			return value[:index], true
		}
		count++
	}
	return value, false
}

func excludeWholeRelatedBody(value string) (string, bool) {
	runes := utf8.RuneCountInString(value)
	if runes <= 1 {
		return "", true
	}
	truncated, _ := truncateContextRunes(value, runes-1)
	return truncated, true
}

func presentContextTerms(markdown string, terms []string) []string {
	present := make(map[string]struct{})
	for _, term := range Tokenize(markdown) {
		present[term] = struct{}{}
	}
	filtered := make([]string, 0, len(terms))
	for _, term := range terms {
		if _, ok := present[term]; ok {
			filtered = append(filtered, term)
		}
	}
	return filtered
}

func fitContextExcerpt(
	bundle ContextBundle,
	relatedIndex int,
	excerpt ContextExcerpt,
	maxBytes int,
) (ContextBundle, bool, bool, error) {
	fullBundle := appendContextExcerpt(bundle, relatedIndex, excerpt)
	withinLimit, err := contextBundleWithinLimit(fullBundle, maxBytes)
	if err != nil {
		return ContextBundle{}, false, false, err
	}
	if withinLimit {
		return fullBundle, true, true, nil
	}

	runeCount := utf8.RuneCountInString(excerpt.Markdown)
	low, high := 1, runeCount-1
	best := 0
	for low <= high {
		middle := low + (high-low)/2
		partial := contextExcerptPrefix(excerpt, middle)
		partialBundle := appendContextExcerpt(bundle, relatedIndex, partial)
		fits, fitErr := contextBundleWithinLimit(partialBundle, maxBytes)
		if fitErr != nil {
			return ContextBundle{}, false, false, fitErr
		}
		if fits {
			best = middle
			low = middle + 1
		} else {
			high = middle - 1
		}
	}
	if best == 0 {
		return bundle, false, false, nil
	}
	partial := contextExcerptPrefix(excerpt, best)
	partialBundle := appendContextExcerpt(bundle, relatedIndex, partial)
	return partialBundle, false, true, nil
}

func contextExcerptPrefix(excerpt ContextExcerpt, runes int) ContextExcerpt {
	prefix, truncated := truncateContextRunes(excerpt.Markdown, runes)
	excerpt.Markdown = prefix
	excerpt.Truncated = excerpt.Truncated || truncated
	excerpt.EndLine = excerpt.StartLine + strings.Count(prefix, "\n")
	excerpt.DistinctiveTerms = presentContextTerms(prefix, excerpt.DistinctiveTerms)
	return excerpt
}
