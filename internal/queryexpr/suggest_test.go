package queryexpr

import (
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

func TestCompile_UnknownFieldSuggestions(t *testing.T) {
	_, _, env := fixtureVault(t)

	cases := []struct {
		src         string
		wantSpan    string
		wantMsg     string
		wantSuggest []string
	}{
		{"backlnks>3", "backlnks", "unknown field \"backlnks\"", []string{"backlinks"}},
		{"tga:alpha", "tga", "unknown field \"tga\"", []string{"tag", "age", "tags"}},
		{"ratign>3", "ratign", "unknown field \"ratign\"", []string{"rating"}},
		{"statuz:wip", "statuz", "unknown field \"statuz\"", []string{"status"}},
		{"linkz-to:[[Hub]]", "linkz-to", "unknown field \"linkz-to\"", []string{"links-to"}},
		{"tag:alpha and dgree>2", "dgree", "unknown field \"dgree\"", []string{"degree"}},
		{"absolutelynothinglikeit:1", "absolutelynothinglikeit", "unknown field \"absolutelynothinglikeit\"", nil},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			_, err := Compile(tc.src, env)
			perr, ok := err.(*ParseError)
			if !ok {
				t.Fatalf("Compile(%q) error = %v (%T), want *ParseError", tc.src, err, err)
			}
			if perr.Msg != tc.wantMsg {
				t.Errorf("message = %q, want %q", perr.Msg, tc.wantMsg)
			}
			if strings.Join(perr.Suggest, ",") != strings.Join(tc.wantSuggest, ",") {
				t.Errorf("suggestions = %v, want %v", perr.Suggest, tc.wantSuggest)
			}
			// Carets point at the field name, not the whole term.
			if got := tc.src[perr.Span.Start:perr.Span.End]; got != tc.wantSpan {
				t.Errorf("span covers %q, want %q", got, tc.wantSpan)
			}
		})
	}
}

// TestCompile_SuggestionsAreCappedAndSorted pins the three properties the
// suggestion list promises: distance <= 2, at most three entries, and a
// deterministic order.
func TestCompile_SuggestionsAreCappedAndSorted(t *testing.T) {
	env := Env{Frontmatter: &config.FrontmatterConfig{Properties: map[string]config.PropertyDefinition{
		"tar": {Type: config.KindString},
		"tab": {Type: config.KindString},
		"tan": {Type: config.KindString},
		"tap": {Type: config.KindString},
	}}}

	_, err := Compile("tag2:x", env)
	perr, ok := err.(*ParseError)
	if !ok {
		t.Fatalf("error = %v (%T), want *ParseError", err, err)
	}
	if len(perr.Suggest) > 3 {
		t.Fatalf("suggestions = %v, want at most 3", perr.Suggest)
	}
	// Distance 1 (tag) sorts ahead of the distance-2 candidates, which are
	// themselves alphabetical.
	want := []string{"tag", "age", "tab"}
	if strings.Join(perr.Suggest, ",") != strings.Join(want, ",") {
		t.Fatalf("suggestions = %v, want %v", perr.Suggest, want)
	}
	// Map iteration order must not leak into the list: repeat the compile and
	// require the same answer every time.
	for i := 0; i < 20; i++ {
		_, again := Compile("tag2:x", env)
		repeat, ok := again.(*ParseError)
		if !ok {
			t.Fatalf("repeat %d: error = %v (%T), want *ParseError", i, again, again)
		}
		if strings.Join(repeat.Suggest, ",") != strings.Join(want, ",") {
			t.Fatalf("suggestions are not deterministic: %v on repeat %d, want %v", repeat.Suggest, i, want)
		}
	}
}

func TestDamerauLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"tag", "tag", 0},
		{"tag", "", 3},
		{"", "tag", 3},
		{"tga", "tag", 1}, // one transposition, not two edits
		{"backlnks", "backlinks", 1},
		{"statuz", "status", 1},
		{"near", "degree", 4},
		// One rune edit, not two bytes. The accented rune is escaped
		// deliberately: a literal here is easy to mis-normalize.
		{"café", "cafe", 1},
	}
	for _, tc := range cases {
		if got := damerauLevenshtein(tc.a, tc.b); got != tc.want {
			t.Errorf("damerauLevenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if got := damerauLevenshtein(tc.b, tc.a); got != tc.want {
			t.Errorf("damerauLevenshtein(%q, %q) = %d, want %d (not symmetric)", tc.b, tc.a, got, tc.want)
		}
	}
}
