package recall

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
)

// TermContribution is one overlapping term's additive contribution to a
// cosine similarity score. Contributions are normalized and therefore sum to
// LexicalMatch.Score, apart from floating-point rounding.
type TermContribution struct {
	Term         string
	Contribution float64
}

// LexicalMatch is the deterministic text similarity between two documents.
// Terms are ordered by decreasing contribution, then lexicographically.
type LexicalMatch struct {
	Score float64
	Terms []TermContribution
}

// LexicalIndex is an immutable, in-memory TF-IDF index keyed by vault-relative
// document path.
type LexicalIndex struct {
	vectors map[string]lexicalVector
}

type lexicalVector struct {
	weights map[string]float64
	terms   []string
	norm    float64
}

// BuildLexicalIndex builds weighted TF-IDF vectors for documents. Document
// frequency is corpus-wide and includes empty documents in the corpus size.
// Duplicate or empty paths are rejected because comparisons are path keyed.
func BuildLexicalIndex(documents []Document) (*LexicalIndex, error) {
	documents = append([]Document(nil), documents...)
	sort.SliceStable(documents, func(i, j int) bool {
		return documents[i].Path < documents[j].Path
	})

	termFrequencies := make(map[string]map[string]float64, len(documents))
	documentFrequencies := make(map[string]int)
	for index, document := range documents {
		if document.Path == "" {
			return nil, fmt.Errorf("recall: lexical: document %d has empty path", index)
		}
		if index > 0 && document.Path == documents[index-1].Path {
			return nil, fmt.Errorf("recall: lexical: duplicate document path %q", document.Path)
		}

		frequencies, err := documentTermFrequencies(document)
		if err != nil {
			return nil, err
		}
		termFrequencies[document.Path] = frequencies
		for _, term := range sortedFloatKeys(frequencies) {
			documentFrequencies[term]++
		}
	}

	corpusSize := float64(len(documents))
	inverseDocumentFrequencies := make(map[string]float64, len(documentFrequencies))
	for term, frequency := range documentFrequencies {
		// This is the smoothed IDF used by common TF-IDF implementations. The
		// added one keeps corpus-wide terms useful while downweighting them.
		inverseDocumentFrequencies[term] = math.Log((1+corpusSize)/(1+float64(frequency))) + 1
	}

	index := &LexicalIndex{vectors: make(map[string]lexicalVector, len(documents))}
	for _, document := range documents {
		frequencies := termFrequencies[document.Path]
		terms := sortedFloatKeys(frequencies)
		weights := make(map[string]float64, len(terms))
		squaredNorm := 0.0
		for _, term := range terms {
			weight := frequencies[term] * inverseDocumentFrequencies[term]
			weights[term] = weight
			squaredNorm += weight * weight
		}
		index.vectors[document.Path] = lexicalVector{
			weights: weights,
			terms:   terms,
			norm:    math.Sqrt(squaredNorm),
		}
	}
	return index, nil
}

// Len returns the number of indexed documents.
func (index *LexicalIndex) Len() int {
	if index == nil {
		return 0
	}
	return len(index.vectors)
}

// Compare returns the symmetric cosine similarity and distinctive overlapping
// terms for two paths. The boolean is false if either path is not indexed.
// Empty vectors compare successfully with a zero score and no terms.
func (index *LexicalIndex) Compare(sourcePath, candidatePath string) (LexicalMatch, bool) {
	if index == nil {
		return LexicalMatch{}, false
	}
	source, sourceOK := index.vectors[sourcePath]
	candidate, candidateOK := index.vectors[candidatePath]
	if !sourceOK || !candidateOK {
		return LexicalMatch{}, false
	}
	if source.norm == 0 || candidate.norm == 0 {
		return LexicalMatch{}, true
	}

	denominator := source.norm * candidate.norm
	terms := intersectTerms(source.terms, candidate.terms)
	contributions := make([]TermContribution, 0, len(terms))
	score := 0.0
	for _, term := range terms {
		contribution := source.weights[term] * candidate.weights[term] / denominator
		score += contribution
		contributions = append(contributions, TermContribution{
			Term:         term,
			Contribution: contribution,
		})
	}
	if score > 1 && score < 1+1e-12 {
		score = 1
	}
	sort.SliceStable(contributions, func(i, j int) bool {
		if contributions[i].Contribution != contributions[j].Contribution {
			return contributions[i].Contribution > contributions[j].Contribution
		}
		return contributions[i].Term < contributions[j].Term
	})
	return LexicalMatch{Score: score, Terms: contributions}, true
}

// Similarity returns the cosine similarity for two indexed paths. Missing
// paths, nil indexes, and empty vectors have zero similarity.
func (index *LexicalIndex) Similarity(sourcePath, candidatePath string) float64 {
	match, ok := index.Compare(sourcePath, candidatePath)
	if !ok {
		return 0
	}
	return match.Score
}

// Tokenize returns normalized lexical tokens in source order, including
// repetitions. Tokens are NFC-normalized, lowercased Unicode letter/number
// sequences with at least two runes. Combining marks are retained only when
// attached to an existing token.
func Tokenize(value string) []string {
	value = textnorm.NFC(strings.ToLower(textnorm.NFC(value)))
	var tokens []string
	var token []rune
	flush := func() {
		if len(token) >= 2 {
			tokens = append(tokens, string(token))
		}
		token = token[:0]
	}
	for _, current := range value {
		switch {
		case unicode.IsLetter(current) || unicode.IsNumber(current):
			token = append(token, current)
		case unicode.IsMark(current) && len(token) > 0:
			token = append(token, current)
		default:
			flush()
		}
	}
	flush()
	return tokens
}

func documentTermFrequencies(document Document) (map[string]float64, error) {
	frequencies := make(map[string]float64)
	for fieldIndex, field := range document.Fields {
		if math.IsNaN(field.Weight) || math.IsInf(field.Weight, 0) || field.Weight < 0 {
			return nil, fmt.Errorf(
				"recall: lexical %q: field %d has invalid weight %v",
				document.Path,
				fieldIndex,
				field.Weight,
			)
		}
		if field.Weight == 0 {
			continue
		}
		counts := make(map[string]int)
		for _, term := range Tokenize(field.Text) {
			counts[term]++
		}
		for term, count := range counts {
			// Apply sublinear TF within each semantic field, then preserve the
			// title/heading/prose weight as the field-level multiplier.
			frequencies[term] += field.Weight * (1 + math.Log(float64(count)))
		}
	}
	return frequencies, nil
}

func sortedFloatKeys(values map[string]float64) []string {
	if len(values) == 0 {
		return nil
	}
	terms := make([]string, 0, len(values))
	for term := range values {
		terms = append(terms, term)
	}
	sort.Strings(terms)
	return terms
}

func intersectTerms(left, right []string) []string {
	terms := make([]string, 0, min(len(left), len(right)))
	for leftIndex, rightIndex := 0, 0; leftIndex < len(left) && rightIndex < len(right); {
		switch {
		case left[leftIndex] < right[rightIndex]:
			leftIndex++
		case left[leftIndex] > right[rightIndex]:
			rightIndex++
		default:
			terms = append(terms, left[leftIndex])
			leftIndex++
			rightIndex++
		}
	}
	return terms
}
