package cli

import "testing"

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"top-10":     "top-10",
		"Recent WIP": `"Recent WIP"`,
		"":           `""`,
		"it's":       `"it's"`,
		"cost $5":    `'cost $5'`,
		"don't $do":  `'don'\''t $do'`,
		"a;b":        `"a;b"`,
		"Été":        `"Été"`,
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}
