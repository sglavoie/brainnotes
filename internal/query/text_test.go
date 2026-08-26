package query

import (
	"sort"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

const (
	// The same word in both Unicode forms: composed "é" (U+00E9) versus "e"
	// followed by a combining acute accent (U+0301). macOS filenames and
	// editor-written bodies routinely disagree about which form they use.
	cafeNFC = "Caf\u00e9"
	cafeNFD = "Cafe\u0301"
)

func TestFoldText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"lowercases", "Deep Work", "deep work"},
		{"trims surrounding space", "  Deep Work\n", "deep work"},
		{"composes decomposed accents", cafeNFD, "caf\u00e9"},
		{"leaves composed accents alone", cafeNFC, "caf\u00e9"},
		{"lowercases accented text", "CAF\u00c9", "caf\u00e9"},
		{"empty stays empty", "   ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FoldText(tc.in); got != tc.want {
				t.Errorf("FoldText(%q): got %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestFoldTextIsIdempotentAndAgreesAcrossForms is the property the whole helper
// exists for: NFC and NFD renderings of the same text fold to identical bytes.
func TestFoldTextIsIdempotentAndAgreesAcrossForms(t *testing.T) {
	folded := FoldText(cafeNFD)
	if folded != FoldText(cafeNFC) {
		t.Errorf("NFD and NFC forms folded differently: %q vs %q", folded, FoldText(cafeNFC))
	}
	if FoldText(folded) != folded {
		t.Errorf("FoldText is not idempotent: %q -> %q", folded, FoldText(folded))
	}
}

func TestTextPredicate(t *testing.T) {
	// The title is stored composed and the body decomposed, so a needle in
	// either form has to match both.
	cafeNote := cafeNFC + ".md"
	_, notes := newVault(t, map[string]string{
		cafeNote:     "---\n---\nA plain ascii body.\n",
		"Morning.md": "---\n---\nDiscussion du " + cafeNFD + " matinal.\n",
		"Other.md":   "---\n---\nNothing relevant here.\n",
	})

	cases := []struct {
		name   string
		folded string
		want   []string
	}{
		{"matches title", FoldText("caf"), []string{cafeNote, "Morning.md"}},
		{"matches body", FoldText("matinal"), []string{"Morning.md"}},
		{"is case-insensitive", FoldText("NOTHING RELEVANT"), []string{"Other.md"}},
		{"a decomposed needle matches a composed title", FoldText(cafeNFD), []string{cafeNote, "Morning.md"}},
		{"a composed needle matches a decomposed body", FoldText(cafeNFC), []string{cafeNote, "Morning.md"}},
		{"no match", FoldText("absent"), nil},
		{"an empty needle matches everything", "", []string{cafeNote, "Morning.md", "Other.md"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Text(tc.folded)
			var got []string
			for rel, n := range notes {
				if p(n) {
					// The filesystem may hand the name back in either form;
					// compare on a single canonical rendering.
					got = append(got, textnorm.NFC(rel))
				}
			}
			sort.Strings(got)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("Text(%q): got %v, want %v", tc.folded, got, tc.want)
			}
		})
	}
}

func TestTextPredicateRejectsNilNote(t *testing.T) {
	var nilNote *vault.Note
	if Text("anything")(nilNote) {
		t.Errorf("Text matched a nil note")
	}
	if Text("")(nilNote) {
		t.Errorf("Text with an empty needle matched a nil note")
	}
}
