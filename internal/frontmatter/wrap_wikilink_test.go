package frontmatter

import (
	"strings"
	"testing"
)

// TestWrapWikilink_DoesNotDoubleWrapPartialBrackets is a regression guard for a
// bug where wrapWikilink only checked that a string both starts with `[[` AND
// ends with `]]`. A stored value of `[[X` (prefix only) or `X]]` (suffix only)
// would slip past that check and be wrapped into `[[[[X]]` / `[[X]]]]` — visibly
// malformed strings that no consumer (Obsidian, the rename command, etc.) can
// parse. The fix is to skip wrapping whenever the input already contains any
// `[[` or `]]` bracket pair.
func TestWrapWikilink_DoesNotDoubleWrapPartialBrackets(t *testing.T) {
	cases := map[string]string{
		"prefix only":         "[[X",
		"suffix only":         "X]]",
		"wrapped plus suffix": "[[X]] B",
		"prefix plus wrapped": "A [[X]]",
		"two wikilinks":       "[[X]] [[Y]]",
		"double-wrapped":      "[[[[X]]]]",
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			got := wrapWikilink(s)
			if got != s {
				t.Errorf("wrapWikilink(%q) = %q; want %q (no change)", s, got, s)
			}
		})
	}
}

// TestWrapWikilink_BareStringsStillWrap ensures the defensive check only skips
// strings that already have brackets — bare strings must still be wrapped.
func TestWrapWikilink_BareStringsStillWrap(t *testing.T) {
	cases := map[string]string{
		"plain":          "X",
		"with spaces":    "Some Note",
		"unicode":        "Café Note",
		"alias-shape":    "X|Alias",
		"heading-shape":  "X#Heading",
		"path-separator": "Folder/Note",
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			want := "[[" + s + "]]"
			got := wrapWikilink(s)
			if got != want {
				t.Errorf("wrapWikilink(%q) = %q; want %q", s, got, want)
			}
		})
	}
}

// TestWrapWikilink_EmptyStringUnchanged confirms the empty-string fast-path.
func TestWrapWikilink_EmptyStringUnchanged(t *testing.T) {
	if got := wrapWikilink(""); got != "" {
		t.Errorf("wrapWikilink(``) = %q; want empty", got)
	}
}

// TestMarshalDoesNotProduceQuadrupleBrackets is the end-to-end regression test:
// for any of the partial-bracket inputs above, Marshal must NOT emit a string
// containing `[[[[` or `]]]]` in the output.
func TestMarshalDoesNotProduceQuadrupleBrackets(t *testing.T) {
	inputs := []string{"[[X", "X]]", "[[X]] B", "A [[X]]"}
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			// Scalar wikilink property.
			doc := Document{Front: NewMap(), Body: ""}
			doc.Front.Set("parent", in)
			out, err := Marshal(doc, defaultCfg())
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			s := string(out)
			if strings.Contains(s, "[[[[") || strings.Contains(s, "]]]]") {
				t.Errorf("quadruple bracket leaked into output for %q:\n%s", in, s)
			}

			// Same value inside a wikilink-typed list.
			doc2 := Document{Front: NewMap(), Body: ""}
			doc2.Front.Set("categories", []any{in})
			out2, err := Marshal(doc2, defaultCfg())
			if err != nil {
				t.Fatalf("Marshal list: %v", err)
			}
			s2 := string(out2)
			if strings.Contains(s2, "[[[[") || strings.Contains(s2, "]]]]") {
				t.Errorf("quadruple bracket leaked into list output for %q:\n%s", in, s2)
			}
		})
	}
}

// TestMarshalWikilinkAliasAndHeadingShapesRoundTrip locks down that the wrap
// guard does not strip or alter wikilink syntax variants Obsidian supports —
// aliases (`[[X|Y]]`), headings (`[[X#H]]`), and combined (`[[X#H|Y]]`).
func TestMarshalWikilinkAliasAndHeadingShapesRoundTrip(t *testing.T) {
	cases := []string{"[[X]]", "[[X|Alias]]", "[[X#Heading]]", "[[X#Heading|Alias]]"}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			doc := Document{Front: NewMap(), Body: ""}
			doc.Front.Set("parent", c)
			out, err := Marshal(doc, defaultCfg())
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			doc2, err := Parse(out)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got, _ := doc2.Front.GetString("parent")
			if got != c {
				t.Errorf("round-trip lost wikilink form: got %q want %q", got, c)
			}
		})
	}
}
