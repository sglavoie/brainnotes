package wikilink

import "testing"

func TestRewriteTitleHandlesWhitespacePadding(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		in         string
		oldT, newT string
		want       string
	}{
		{
			name: "bare target with surrounding spaces",
			in:   "see [[ Foo ]] please",
			oldT: "Foo", newT: "Bar",
			want: "see [[Bar]] please",
		},
		{
			name: "spaces around alias separator",
			in:   "[[Foo |alias]]",
			oldT: "Foo", newT: "Bar",
			want: "[[Bar|alias]]",
		},
		{
			name: "spaces around heading",
			in:   "[[Foo # Section]]",
			oldT: "Foo", newT: "Bar",
			want: "[[Bar#Section]]",
		},
		{
			name: "embed prefix preserved",
			in:   "![[ Foo ]]",
			oldT: "Foo", newT: "Bar",
			want: "![[Bar]]",
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := RewriteTitle(tc.in, tc.oldT, tc.newT)
			if got != tc.want {
				t.Fatalf("RewriteTitle\n  got:  %q\n  want: %q", got, tc.want)
			}
		})
	}
}

func TestRewriteMoveHandlesWhitespacePadding(t *testing.T) {
	t.Parallel()
	in := "[[ Books/Foo ]] and bare [[Foo]]"
	got := RewriteMove(in, "Books/Foo", "References/Books/Foo")
	want := "[[References/Books/Foo]] and bare [[Foo]]"
	if got != want {
		t.Fatalf("RewriteMove\n  got:  %q\n  want: %q", got, want)
	}
}

func TestRewriteMovePreservesHeadingAndAliasWithWhitespace(t *testing.T) {
	t.Parallel()
	in := "[[ Books/Foo # Section | display ]]"
	got := RewriteMove(in, "Books/Foo", "References/Books/Foo")
	want := "[[References/Books/Foo#Section|display]]"
	if got != want {
		t.Fatalf("RewriteMove\n  got:  %q\n  want: %q", got, want)
	}
}

func TestRewriteTitleTrimsRewriteParameters(t *testing.T) {
	t.Parallel()
	in := "[[Foo]] and [[Refs/Foo.md#H|disp]]"
	got := RewriteTitle(in, " Foo\t", "\tBar ")
	want := "[[Bar]] and [[Refs/Bar.md#H|disp]]"
	if got != want {
		t.Fatalf("RewriteTitle\n  got:  %q\n  want: %q", got, want)
	}
}

func TestRewriteMoveTrimsRewriteParameters(t *testing.T) {
	t.Parallel()
	in := "[[Books/Foo]] and [[Books/Foo.md#H|disp]]"
	got := RewriteMove(in, "\tBooks/Foo ", " Refs/Books/Foo\t")
	want := "[[Refs/Books/Foo]] and [[Refs/Books/Foo.md#H|disp]]"
	if got != want {
		t.Fatalf("RewriteMove\n  got:  %q\n  want: %q", got, want)
	}
}

func TestRewriteTripleBrackets(t *testing.T) {
	t.Parallel()

	got := RewriteTitle("see [[[Foo]]] now", "Foo", "Bar")
	want := "see [[[Bar]]] now"
	if got != want {
		t.Fatalf("RewriteTitle\n  got:  %q\n  want: %q", got, want)
	}
}

func TestRewriteConsecutiveLinks(t *testing.T) {
	t.Parallel()

	got := Rewrite("[[A]][[B]]", func(l Link) (string, bool) {
		return "[[" + l.Target + "2]]", true
	})
	want := "[[A2]][[B2]]"
	if got != want {
		t.Fatalf("Rewrite consecutive\n  got:  %q\n  want: %q", got, want)
	}
}

func TestRewriteRoundTripAfterRename(t *testing.T) {
	t.Parallel()

	in := "see [[Foo]] and [[Refs/Foo#H|disp]]"
	out := RewriteTitle(in, "Foo", "Bar")
	links := Parse(out)
	if len(links) != 2 {
		t.Fatalf("expected 2 links after rename, got %d in %q", len(links), out)
	}
	if links[0].Target != "Bar" || links[0].Title() != "Bar" {
		t.Errorf("first link target=%q title=%q, want Bar/Bar", links[0].Target, links[0].Title())
	}
	if links[1].Target != "Refs/Bar" || links[1].Title() != "Bar" || links[1].Heading != "H" || links[1].Alias != "disp" {
		t.Errorf("second link = %+v", links[1])
	}
}
