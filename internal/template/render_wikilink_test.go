package template

import (
	"testing"
	"time"
)

func TestWikilinkNoDoubleWrap(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"Books", "[[Books]]"},
		{"[[Books]]", "[[Books]]"},
		{"  [[Books]]  ", "  [[Books]]  "},
		{"", ""},
		// "[[ " (just brackets) sanitized to empty inner, but wrap is
		// still emitted because trimmed is non-empty.
		{"[[", "[[]]"},
		// Not fully wrapped — must wrap *and* sanitize inner brackets to
		// avoid emitting a malformed [[...]] span.
		{"[[a]]b", "[[ab]]"},
		// Two separate wikilinks are not "already wrapped" — sanitize and wrap.
		{"[[a]] and [[b]]", "[[a and b]]"},
		// Closing ]] before end means outer pair is not a single wrap.
		{"[[a]]b]]", "[[ab]]"},
		// Single valid wikilink with heading/alias.
		{"[[Page#Heading|Alias]]", "[[Page#Heading|Alias]]"},
		// Empty wikilink [[]] is treated as already wrapped (len==4).
		{"[[]]", "[[]]"},
		// Whitespace-only must not be wrapped into an invalid wikilink.
		{"   ", "   "},
		{"\t", "\t"},
		{" \n ", " \n "},
		// Inner [[ means the outer [[ ]] does NOT form a single wrap —
		// sanitize inner brackets and wrap.
		{"[[a[[b]]", "[[ab]]"},
		// A title containing `]]` must not produce a malformed wikilink.
		{"Foo]]Bar", "[[FooBar]]"},
		// Single `]` and `[` are also stripped because they break parsing.
		{"Foo]Bar", "[[FooBar]]"},
		{"Foo[Bar", "[[FooBar]]"},
		// Line terminators inside the title collapse to a single space.
		{"Foo\nBar", "[[Foo Bar]]"},
		{"Foo\r\nBar", "[[Foo Bar]]"},
		// Leading or trailing `]]` alone.
		{"]]", "[[]]"},
		{"[[Foo", "[[Foo]]"},
		// Surrounding whitespace on a non-wrapped value must be trimmed —
		// Obsidian treats whitespace inside [[ ]] as part of the link target,
		// so "[[  Books  ]]" would not resolve to the "Books" note.
		{"  Books  ", "[[Books]]"},
		{"Books   ", "[[Books]]"},
		{"   Books", "[[Books]]"},
		{"\tBooks\t", "[[Books]]"},
		// Sanitization of brackets followed by trimming: leading/trailing
		// brackets that get stripped leave whitespace that must also go.
		{" [Foo] ", "[[Foo]]"},
		// Newline-bounded value: sanitize newlines to space, then trim.
		{"\nBooks\n", "[[Books]]"},
		{"Foo\nBar\n", "[[Foo Bar]]"},
	}
	ctx := NewContext("x", "", "/v", nil, FixedClock{T: time.Unix(0, 0).UTC()})
	for _, c := range cases {
		body := `{{wikilink .Vars.v}}`
		ctx.Vars = map[string]string{"v": c.in}
		got := mustRender(t, body, ctx)
		if got != c.want {
			t.Errorf("wikilink(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
