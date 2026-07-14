package recall

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestLexicalSimilarityRanksRelatedProseAboveSuperficialOverlap(t *testing.T) {
	t.Parallel()

	documents := []Document{
		lexicalDocument(
			"Source.md",
			"Bayesian Inference",
			"Calibrating posterior beliefs",
			"Bayesian models update prior beliefs with observed evidence and posterior predictive checks.",
		),
		lexicalDocument(
			"Strong.md",
			"Posterior Predictive Checks",
			"Bayesian model calibration",
			"Observed evidence updates prior beliefs into posterior distributions for calibrated inference.",
		),
		lexicalDocument(
			"Superficial.md",
			"Quarterly Evidence Log",
			"Evidence archive",
			strings.Repeat("evidence ", 30)+"receipts invoices quarterly operations.",
		),
		lexicalDocument(
			"Unrelated.md",
			"Garden Irrigation",
			"Seasonal planting",
			"Tomato seedlings need compost, sunlight, careful watering.",
		),
	}
	index, err := BuildLexicalIndex(documents)
	if err != nil {
		t.Fatalf("BuildLexicalIndex: %v", err)
	}

	strong, ok := index.Compare("Source.md", "Strong.md")
	if !ok {
		t.Fatal("Compare rejected indexed related paths")
	}
	superficial, ok := index.Compare("Source.md", "Superficial.md")
	if !ok {
		t.Fatal("Compare rejected indexed superficial paths")
	}
	unrelated, ok := index.Compare("Source.md", "Unrelated.md")
	if !ok {
		t.Fatal("Compare rejected indexed unrelated paths")
	}
	if strong.Score <= superficial.Score || superficial.Score <= unrelated.Score {
		t.Fatalf(
			"scores strong=%v superficial=%v unrelated=%v, want strict descending relevance",
			strong.Score,
			superficial.Score,
			unrelated.Score,
		)
	}
	if unrelated.Score != 0 || len(unrelated.Terms) != 0 {
		t.Errorf("unrelated match = %#v, want zero score and no terms", unrelated)
	}
	if !hasContributionTerm(strong.Terms, "bayesian") || !hasContributionTerm(strong.Terms, "posterior") {
		t.Errorf("strong overlap terms = %#v, want bayesian and posterior", strong.Terms)
	}
}

func TestLexicalCompareIsSymmetricDeterministicAndHandlesEmptyVectors(t *testing.T) {
	t.Parallel()

	documents := []Document{
		lexicalDocument("B.md", "Alpha Beta", "", "shared evidence synthesis"),
		{Path: "Empty.md"},
		lexicalDocument("A.md", "Beta Gamma", "", "shared evidence analysis"),
	}
	first, err := BuildLexicalIndex(documents)
	if err != nil {
		t.Fatalf("BuildLexicalIndex: %v", err)
	}
	second, err := BuildLexicalIndex([]Document{documents[2], documents[1], documents[0]})
	if err != nil {
		t.Fatalf("second BuildLexicalIndex: %v", err)
	}

	forward, ok := first.Compare("A.md", "B.md")
	if !ok {
		t.Fatal("Compare rejected indexed paths")
	}
	reverse, ok := first.Compare("B.md", "A.md")
	if !ok {
		t.Fatal("reverse Compare rejected indexed paths")
	}
	rebuilt, ok := second.Compare("A.md", "B.md")
	if !ok {
		t.Fatal("rebuilt Compare rejected indexed paths")
	}
	if !reflect.DeepEqual(forward, reverse) {
		t.Errorf("asymmetric match\nforward: %#v\nreverse: %#v", forward, reverse)
	}
	if !reflect.DeepEqual(forward, rebuilt) {
		t.Errorf("repeated build differs\nfirst:  %#v\nsecond: %#v", forward, rebuilt)
	}
	if got := contributionSum(forward.Terms); math.Abs(got-forward.Score) > 1e-15 {
		t.Errorf("term contribution sum = %.17g, score = %.17g", got, forward.Score)
	}
	for index := 1; index < len(forward.Terms); index++ {
		previous := forward.Terms[index-1]
		current := forward.Terms[index]
		if previous.Contribution < current.Contribution ||
			(previous.Contribution == current.Contribution && previous.Term > current.Term) {
			t.Errorf("terms are not contribution/term ordered: %#v", forward.Terms)
			break
		}
	}

	empty, ok := first.Compare("A.md", "Empty.md")
	if !ok || empty.Score != 0 || len(empty.Terms) != 0 {
		t.Errorf("empty-vector match = %#v, %v; want successful zero match", empty, ok)
	}
	if _, ok := first.Compare("A.md", "Missing.md"); ok {
		t.Error("Compare accepted a missing path")
	}
	if got := first.Similarity("A.md", "Missing.md"); got != 0 {
		t.Errorf("missing-path Similarity = %v, want 0", got)
	}
	if first.Len() != 3 || (*LexicalIndex)(nil).Len() != 0 {
		t.Errorf("Len results = %d and %d, want 3 and 0", first.Len(), (*LexicalIndex)(nil).Len())
	}
}

func TestLexicalDistinctiveTermsUseSmoothedCorpusIDF(t *testing.T) {
	t.Parallel()

	documents := []Document{
		lexicalDocument("A.md", "", "", "common rare"),
		lexicalDocument("B.md", "", "", "common rare"),
		lexicalDocument("C.md", "", "", "common alpha"),
		lexicalDocument("D.md", "", "", "common bravo"),
	}
	index, err := BuildLexicalIndex(documents)
	if err != nil {
		t.Fatalf("BuildLexicalIndex: %v", err)
	}
	match, ok := index.Compare("A.md", "B.md")
	if !ok {
		t.Fatal("Compare rejected indexed paths")
	}
	if len(match.Terms) != 2 {
		t.Fatalf("terms = %#v, want rare and common", match.Terms)
	}
	if match.Terms[0].Term != "rare" || match.Terms[1].Term != "common" {
		t.Errorf("terms = %#v, want rarer corpus term first", match.Terms)
	}
	if match.Terms[0].Contribution <= match.Terms[1].Contribution {
		t.Errorf("rare contribution %v <= common contribution %v", match.Terms[0], match.Terms[1])
	}
}

func TestLexicalFieldWeightsAndSublinearTermFrequency(t *testing.T) {
	t.Parallel()

	documents := []Document{
		lexicalDocument("Source.md", "", "", "alpha beta"),
		lexicalDocument("Title.md", "alpha", "", "gamma"),
		lexicalDocument("Prose.md", "", "", "beta gamma"),
		lexicalDocument("Repeated.md", "", "", strings.Repeat("beta ", 100)+"gamma"),
	}
	index, err := BuildLexicalIndex(documents)
	if err != nil {
		t.Fatalf("BuildLexicalIndex: %v", err)
	}
	if title, prose := index.Similarity("Source.md", "Title.md"), index.Similarity("Source.md", "Prose.md"); title <= prose {
		t.Errorf("title-weighted score %v <= prose-only score %v", title, prose)
	}
	if repeated := index.Similarity("Source.md", "Repeated.md"); repeated >= 0.9 {
		t.Errorf("sublinear repeated-term score = %v, want repetition bounded below 0.9", repeated)
	}
}

func TestTokenizeNormalizesUnicodeAndKeepsNoStopwordTable(t *testing.T) {
	t.Parallel()

	got := Tokenize("É COFFEE cafe\u0301 東京 A 1 go Go can't co-op")
	want := []string{"coffee", "café", "東京", "go", "go", "can", "co", "op"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize = %#v, want %#v", got, want)
	}
}

func TestBuildLexicalIndexRejectsAmbiguousOrInvalidDocuments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		documents []Document
		wantError string
	}{
		{
			name:      "empty path",
			documents: []Document{{Path: ""}},
			wantError: "empty path",
		},
		{
			name:      "duplicate path",
			documents: []Document{{Path: "Same.md"}, {Path: "Same.md"}},
			wantError: "duplicate document path",
		},
		{
			name: "negative weight",
			documents: []Document{{
				Path:   "Bad.md",
				Fields: []TextField{{Text: "terms", Weight: -1}},
			}},
			wantError: "invalid weight",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := BuildLexicalIndex(test.documents)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("BuildLexicalIndex error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func lexicalDocument(path, title, heading, prose string) Document {
	fields := make([]TextField, 0, 3)
	if title != "" {
		fields = append(fields, TextField{Kind: TextTitle, Text: title, Weight: TitleWeight, BlockIndex: -1})
	}
	if heading != "" {
		fields = append(fields, TextField{Kind: TextHeading, Text: heading, Weight: HeadingWeight, BlockIndex: 0})
	}
	if prose != "" {
		fields = append(fields, TextField{Kind: TextProse, Text: prose, Weight: ProseWeight, BlockIndex: 1})
	}
	return Document{Path: path, Title: title, Fields: fields}
}

func hasContributionTerm(terms []TermContribution, want string) bool {
	for _, term := range terms {
		if term.Term == want {
			return true
		}
	}
	return false
}

func contributionSum(terms []TermContribution) float64 {
	total := 0.0
	for _, term := range terms {
		total += term.Contribution
	}
	return total
}
