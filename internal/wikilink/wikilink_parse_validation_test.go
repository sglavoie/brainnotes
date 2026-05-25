package wikilink

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseNoFalsePositives(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
	}{
		{"unclosed double brackets", "this is [[ but no close"},
		{"single brackets around text", "[A]"},
		{"single brackets around bracket", "[ [A] ]"},
		{"only one bracket", "[[A]"},
		{"empty body", "[[]]"},
		{"plain text", "no wikilinks here"},
		{"opening brackets one line, close on next", "stray [[ open\nlater ]] close"},
		{"opener and close separated by paragraph", "[[Foo\n\nBar]]"},
		{"wikilink target spans multiple lines (LF)", "[[Foo\nBar]]"},
		{"wikilink target spans multiple lines (CRLF)", "[[Foo\r\nBar]]"},
		{"wikilink with newline in alias", "[[Foo|with\nalias]]"},
		{"wikilink with newline in heading", "[[Foo#hd\nline]]"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Parse(tc.in)
			if len(got) != 0 {
				t.Fatalf("Parse(%q) = %+v, want no matches", tc.in, got)
			}
		})
	}
}

func TestParseRejectsAllWhitespaceTarget(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"[[   ]]", "[[\t\t]]", "[[ | alias]]"} {
		if got := Parse(in); len(got) != 0 {
			t.Errorf("Parse(%q) = %+v, want no matches", in, got)
		}
	}
}

func TestParseTripleBracketsDoesNotConsumeBracket(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		targets []string
	}{
		{"triple open, triple close", "[[[Foo]]]", []string{"Foo"}},
		{"quadruple open and close", "[[[[Foo]]]]", []string{"Foo"}},
		{"triple open mid-text", "see [[[Foo]]] please", []string{"Foo"}},
		{"two links, second triple", "[[A]] [[[B]]", []string{"A", "B"}},
		{"bracket inside alias rejected", "[[Foo|al[ias]]", nil},
		{"bracket inside heading rejected", "[[Foo#h[d]]", nil},
		{"bracket inside target rejected", "[[F[oo]]", nil},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := collectTargets(Parse(tc.in))
			if len(tc.targets) == 0 && len(got) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.targets) {
				t.Fatalf("Parse(%q) targets = %v, want %v", tc.in, got, tc.targets)
			}
		})
	}
}

func TestParseRejectsTrailingSlashTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
	}{
		{"bare folder slash", "[[Folder/]]"},
		{"nested folder slash", "[[A/B/]]"},
		{"folder slash with spaces", "[[ Folder/ ]]"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Parse(tc.in)
			if len(got) != 0 {
				t.Fatalf("Parse(%q) = %+v, want no matches", tc.in, got)
			}
		})
	}
}

func TestParseEmptyHeadingOrAlias(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
	}{
		{"empty heading", "[[Foo#]]"},
		{"empty alias", "[[Foo|]]"},
		{"empty heading with alias", "[[Foo#|Bar]]"},
		{"heading-only no target", "[[#Heading]]"},
		{"alias-only no target", "[[|Alias]]"},
		{"whitespace-only heading", "[[Foo#   ]]"},
		{"whitespace-only alias", "[[Foo|   ]]"},
		{"whitespace-only heading with real alias", "[[Foo#   |alias]]"},
		{"whitespace heading and alias", "[[Foo#   |   ]]"},
		{"tab-only heading", "[[Foo#\t]]"},
		{"tab-only alias", "[[Foo|\t]]"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Parse(tc.in)
			if len(got) != 0 {
				t.Fatalf("Parse(%q) = %+v, want no matches", tc.in, got)
			}
		})
	}
}

func TestParseEmptyAndWhitespaceInput(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"", "\n", "\r\n", "   ", "\t\n\n  "} {
		if got := Parse(in); len(got) != 0 {
			t.Errorf("Parse(%q) = %+v, want no matches", in, got)
		}
	}
}

func TestParseDoesNotPanicOnExoticInputs(t *testing.T) {
	t.Parallel()

	inputs := []string{
		"",
		"\x00",
		"[[",
		"]]",
		"[[]]",
		"[[Foo\x00Bar]]",
		"\r\r\r",
		"\n\n\n",
		"```",
		"~~~",
		"> > > [[",
		strings.Repeat("[[Foo]] ", 100),
	}
	for _, in := range inputs {
		_ = Parse(in)
		_ = Rewrite(in, func(Link) (string, bool) { return "X", true })
		_ = RewriteTitle(in, "Foo", "Bar")
		_ = RewriteMove(in, "A/B", "C/D")
	}
}
