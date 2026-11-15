package query

import "testing"

func TestFuzzyTitleCost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		query, title string
		wantCost     int
		wantOK       bool
	}{
		{"deliberate practice", "deliberate practice", 0, true},
		{"delibrate pratice", "deliberate practice", 2, true},
		{"delibrat", "deliberate practice", 1, true}, // half-typed and misspelled
		{"practice", "deliberate practice", 0, true},
		{"deepwork", "deep work", 1, true}, // a missing space is one edit
		{"zetelkasten", "zettelkasten", 1, true},
		{"smart ntoes", "how to take smart notes", 1, true}, // a transposition
		{"café", "cafe society", 1, true},                   // one rune, not two bytes
		{"to", "do", 0, false},                              // short words must match exactly
		{"rust", "deliberate practice", 0, false},
		{"deliberate rust", "deliberate practice", 0, false}, // every word must be near
		{"", "deliberate practice", 0, false},
		{"...", "deliberate practice", 0, false},
	}
	for _, tc := range cases {
		cost, ok := FuzzyTitleCost(tc.query, tc.title)
		if ok != tc.wantOK || (ok && cost != tc.wantCost) {
			t.Errorf("FuzzyTitleCost(%q, %q) = %d, %v; want %d, %v", tc.query, tc.title, cost, ok, tc.wantCost, tc.wantOK)
		}
	}
}
