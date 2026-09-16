package textnorm

import (
	"strings"
	"testing"
)

// Every string here is written with \uXXXX escapes rather than as literal
// characters, deliberately. The whole subject of this file is the difference
// between two byte sequences that render identically, and an editor or a review
// tool that normalizes the source would quietly rewrite a literal pair into the
// same bytes — turning the assertion into a tautology that still passes.
const (
	composedE     = "\u00e9"             // é, one rune
	decomposed    = "e\u0301"            // e + combining acute, two runes
	sharpS        = "\u1e9e"             // ẞ, capital sharp s
	smallSharp    = "\u00df"             // ß, what a simple lowercase mapping gives
	dottedI       = "\u0130"             // İ, capital I with dot above
	foldedI       = "i\u0307"            // i + combining dot above, what folding gives
	iotaDialytika = "\u0390"             // ΐ, composed
	iotaFolded    = "\u03b9\u0308\u0301" // its fold, decomposed
)

// TestNFCComposes is the property every caller in this repository relies on:
// the form macOS writes into a filename and the form an editor writes into a
// wikilink produce one key.
func TestNFCComposes(t *testing.T) {
	t.Parallel()

	if got, want := NFC("Caf"+decomposed+".md"), "Caf"+composedE+".md"; got != want {
		t.Errorf("NFC(decomposed) = %q, want %q", got, want)
	}
	if got, want := NFC("Caf"+composedE+".md"), "Caf"+composedE+".md"; got != want {
		t.Errorf("NFC is not idempotent on composed input: got %q, want %q", got, want)
	}
	// Stated as an inequality too, so the test above cannot pass by the two
	// inputs having been the same string all along.
	if "Caf"+decomposed+".md" == "Caf"+composedE+".md" {
		t.Fatal("the composed and decomposed fixtures are the same bytes; the test proves nothing")
	}
}

// TestFoldIsFullCaseFolding is the reason Fold exists rather than
// strings.ToLower. Each pair below is one name on a case-insensitive filesystem,
// so a caller building a collision key has to give them one key.
func TestFoldIsFullCaseFolding(t *testing.T) {
	t.Parallel()

	pairs := []struct {
		name string
		a, b string
	}{
		{"ordinary ascii", "Alpha.md", "ALPHA.md"},
		{"an accented composed letter", "Caf" + composedE + ".md", "CAF\u00c9.md"},
		{"capital sharp s against its expansion", "Stra" + sharpS + "e.md", "Strasse.md"},
		{"dotted capital I against its expansion", dottedI + "stanbul.md", foldedI + "stanbul.md"},
	}
	for _, tc := range pairs {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if Fold(tc.a) != Fold(tc.b) {
				t.Errorf("Fold(%q) = %q and Fold(%q) = %q, want one key", tc.a, Fold(tc.a), tc.b, Fold(tc.b))
			}
		})
	}
}

// TestFoldExpandsWhereToLowerMaps states the two expansions by hand and states
// what the mapping this package refuses would have produced instead. Without the
// second half, a future switch back to strings.ToLower would still pass every
// ASCII equality above.
func TestFoldExpandsWhereToLowerMaps(t *testing.T) {
	t.Parallel()

	expansions := []struct {
		name          string
		in, fold, low string
	}{
		{"capital sharp s", sharpS, "ss", smallSharp},
		{"dotted capital I", dottedI, foldedI, "i"},
	}
	for _, tc := range expansions {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Fold(tc.in); got != tc.fold {
				t.Errorf("Fold(%q) = %q, want %q", tc.in, got, tc.fold)
			}
			if got := strings.ToLower(tc.in); got != tc.low {
				t.Fatalf("fixture is stale: strings.ToLower(%q) = %q, want %q", tc.in, got, tc.low)
			}
			if tc.fold == tc.low {
				t.Fatalf("fixture proves nothing: fold and lowercase agree on %q", tc.in)
			}
		})
	}
}

// TestFoldDoesNotPreserveNFC is the ordering rule stated as a fact rather than
// as a comment. A caller that normalized only *before* folding would hold a key
// whose bytes depend on which form it was handed, which is the one thing a
// collision key exists to stop depending on.
func TestFoldDoesNotPreserveNFC(t *testing.T) {
	t.Parallel()

	if got := NFC(iotaDialytika); got != iotaDialytika {
		t.Fatalf("fixture is stale: %q is not already NFC (NFC gives %q)", iotaDialytika, got)
	}
	folded := Fold(iotaDialytika)
	if folded != iotaFolded {
		t.Fatalf("Fold(%q) = %q, want %q", iotaDialytika, folded, iotaFolded)
	}
	if folded == NFC(folded) {
		t.Errorf("Fold(%q) = %q is already NFC; the trailing normalization is no longer load-bearing", iotaDialytika, folded)
	}
	if got, want := NFC(folded), iotaDialytika; got != want {
		t.Errorf("NFC(Fold(%q)) = %q, want %q", iotaDialytika, got, want)
	}
}

// TestNormalizeFoldNormalizeIsStable is the composition callers actually build
// keys out of: every spelling of one name reaches one key, and applying the
// composition to its own output changes nothing.
func TestNormalizeFoldNormalizeIsStable(t *testing.T) {
	t.Parallel()

	key := func(s string) string { return NFC(Fold(NFC(s))) }

	spellings := []string{
		"Caf" + composedE + ".md",
		"Caf" + decomposed + ".md",
		"CAF" + composedE + ".md",
		"CAF" + decomposed + ".md",
	}
	first := key(spellings[0])
	for _, s := range spellings[1:] {
		if got := key(s); got != first {
			t.Errorf("key(%q) = %q, want %q", s, got, first)
		}
	}
	idempotent := append([]string{iotaDialytika, sharpS, dottedI}, spellings...)
	for _, s := range idempotent {
		once := key(s)
		if twice := key(once); twice != once {
			t.Errorf("key is not idempotent on %q: %q then %q", s, once, twice)
		}
	}
}
