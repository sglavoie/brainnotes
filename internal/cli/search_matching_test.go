package cli

import (
	"strings"
	"testing"
)

func TestSearch_BasicMatch(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	stdout, err := runSearch(t, root, "relevant")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := searchRels(t, root, stdout)
	want := []string{"Notes/Gamma.md"}
	if !equalStrings(got, want) {
		t.Fatalf("search relevant:\n got=%v\nwant=%v", got, want)
	}
}

func TestSearch_DoesNotMatchFilename(t *testing.T) {
	isolateBnEnv(t)
	root := buildVaultFromFiles(t, map[string]string{
		"Notes/filename-only-term.md": "---\ntitle: Filename Only\n---\nbody has no requested token\n",
	})

	stdout, err := runSearch(t, root, "filename-only-term")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout != "" {
		t.Fatalf("expected filename-only term not to match, got: %q", stdout)
	}
}

func TestSearch_CaseFold(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	stdout, err := runSearch(t, root, "uppercase")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := searchRels(t, root, stdout)
	want := []string{"Notes/Upper.md"}
	if !equalStrings(got, want) {
		t.Fatalf("case-insensitive search for 'uppercase':\n got=%v\nwant=%v", got, want)
	}
}

func TestSearch_CaseSensitive(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	stdout, err := runSearch(t, root, "--case-sensitive", "uppercase")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := searchRels(t, root, stdout)
	if len(got) != 0 {
		t.Fatalf("case-sensitive search for lowercase 'uppercase' should match nothing, got=%v", got)
	}
}

func TestSearch_NoMatches(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	stdout, err := runSearch(t, root, "xyzzy_no_such_word")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout != "" {
		t.Fatalf("expected empty output, got: %q", stdout)
	}
}

func TestSearch_Regex(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	stdout, err := runSearch(t, root, "--regex", "key(word|phrase)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := searchRels(t, root, stdout)
	want := []string{"Notes/Alpha.md", "Notes/Beta.md"}
	if !equalStrings(got, want) {
		t.Fatalf("regex search:\n got=%v\nwant=%v", got, want)
	}
}

func TestSearch_InvalidRegex(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	_, err := runSearch(t, root, "--regex", "[invalid")
	if err == nil {
		t.Fatal("expected error for invalid regex, got nil")
	}
	if !strings.Contains(err.Error(), "[invalid") {
		t.Fatalf("error should contain pattern, got: %v", err)
	}
}

func TestSearch_EmptyPattern(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	_, err := runSearch(t, root, "   ")
	if err == nil {
		t.Fatal("expected error for empty pattern, got nil")
	}
}

func TestSearch_MultipleMatchingNotes(t *testing.T) {
	isolateBnEnv(t)
	root := buildSearchVault(t)

	stdout, err := runSearch(t, root, "keyword")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := searchRels(t, root, stdout)
	want := readGolden(t, "search-basic.golden")
	if !equalStrings(got, want) {
		t.Fatalf("search keyword:\n got=%v\nwant=%v", got, want)
	}
}

// A decomposed (NFD) pattern must match composed (NFC) note text and vice
// versa, in every matcher mode, the way queryexpr already compares in NFC.
func TestBuildMatcherNormalizesToNFC(t *testing.T) {
	const composed, decomposed = "café crème", "café crème"
	modes := []struct {
		name                   string
		isRegex, caseSensitive bool
	}{
		{"substring", false, false},
		{"case-sensitive", false, true},
		{"regex", true, false},
		{"case-sensitive regex", true, true},
	}
	for _, m := range modes {
		for _, pair := range [][2]string{{decomposed, composed}, {composed, decomposed}} {
			match, err := buildMatcher(pair[0], m.isRegex, m.caseSensitive)
			if err != nil {
				t.Fatalf("%s: buildMatcher(%q): %v", m.name, pair[0], err)
			}
			if !match("a line about " + pair[1] + " here") {
				t.Errorf("%s: pattern %+q did not match text %+q", m.name, pair[0], pair[1])
			}
		}
	}
}
