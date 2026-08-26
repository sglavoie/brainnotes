package query

import (
	"math"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// OrderNotesByRelevance is the one definition of what `relevance` and
// `-relevance` mean, executed by both `bn find --sort` and `/api/search`. These
// tests pin the three rules that definition is made of, because the failure they
// guard is a saved view with a direction and a cap selecting a different top N
// on one surface than on the other.

// relevanceNote makes a note addressable by path; nothing else about it matters
// to an ordering that reads a caller-supplied score.
func relevanceNote(path string) *vault.Note { return &vault.Note{Path: path} }

func relevancePaths(notes []*vault.Note) []string {
	paths := make([]string, 0, len(notes))
	for _, n := range notes {
		paths = append(paths, n.Path)
	}
	return paths
}

func equalPaths(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestOrderNotesByRelevance(t *testing.T) {
	anchor := relevanceNote("anchor.md")
	near := relevanceNote("near.md")
	tieA := relevanceNote("a-tie.md")
	tieB := relevanceNote("b-tie.md")
	unscoredA := relevanceNote("a-unscored.md")
	unscoredB := relevanceNote("b-unscored.md")

	notes := []*vault.Note{unscoredB, tieB, near, anchor, tieA, unscoredA}
	scores := map[string]float64{
		"anchor.md": math.Inf(1),
		"near.md":   0.8,
		"a-tie.md":  0.2,
		"b-tie.md":  0.2,
	}
	pathOf := func(n *vault.Note) string { return n.Path }

	cases := []struct {
		name       string
		descending bool
		want       []string
	}{
		{
			// Most related first, so the +Inf anchor leads. The equal-score pair
			// is ordered by path, and the two notes the ranker scored not at all
			// trail in path order.
			name: "relevance is most related first",
			want: []string{"anchor.md", "near.md", "a-tie.md", "b-tie.md", "a-unscored.md", "b-unscored.md"},
		},
		{
			// Only the score comparison flips. The tie stays in ascending path
			// order and the unscored notes stay last — reversing the whole slice
			// instead would answer b-unscored, a-unscored, b-tie, a-tie, ...
			name:       "-relevance reverses scores and nothing else",
			descending: true,
			want:       []string{"a-tie.md", "b-tie.md", "near.md", "anchor.md", "a-unscored.md", "b-unscored.md"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := OrderNotesByRelevance(notes, pathOf, scores, tc.descending)
			if !equalPaths(relevancePaths(got), tc.want) {
				t.Fatalf("ordering: got %v, want %v", relevancePaths(got), tc.want)
			}
		})
	}

	// The caller's selection is its own: ordering returns a fresh slice, and it
	// never drops a note it could not score.
	if !equalPaths(relevancePaths(notes), []string{
		"b-unscored.md", "b-tie.md", "near.md", "anchor.md", "a-tie.md", "a-unscored.md",
	}) {
		t.Fatalf("input slice was mutated: %v", relevancePaths(notes))
	}
}

// TestOrderNotesByRelevanceWithoutScores pins the degenerate cases: a note the
// caller cannot render as a path, and a call with no scores at all, both order
// by path rather than panicking or dropping notes.
func TestOrderNotesByRelevanceWithoutScores(t *testing.T) {
	notes := []*vault.Note{relevanceNote("b.md"), relevanceNote("a.md")}
	want := []string{"a.md", "b.md"}

	for _, descending := range []bool{false, true} {
		got := OrderNotesByRelevance(notes, func(*vault.Note) string { return "" }, nil, descending)
		if !equalPaths(relevancePaths(got), want) {
			t.Fatalf("unplaceable notes (descending=%v): got %v, want %v", descending, relevancePaths(got), want)
		}
		got = OrderNotesByRelevance(notes, nil, nil, descending)
		if !equalPaths(relevancePaths(got), want) {
			t.Fatalf("nil pathOf (descending=%v): got %v, want %v", descending, relevancePaths(got), want)
		}
	}
}
