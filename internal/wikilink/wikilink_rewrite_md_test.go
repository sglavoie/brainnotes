package wikilink

import "testing"

func TestRewriteTitleHandlesMDExtension(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		old  string
		new  string
		want string
	}{
		{
			name: "bare with md extension",
			in:   "[[Foo.md]] and [[Foo]]",
			old:  "Foo", new: "Bar",
			want: "[[Bar.md]] and [[Bar]]",
		},
		{
			name: "folder prefixed with md extension",
			in:   "[[Refs/Foo.md]] and [[Refs/Foo]]",
			old:  "Foo", new: "Bar",
			want: "[[Refs/Bar.md]] and [[Refs/Bar]]",
		},
		{
			name: "uppercase md extension preserved",
			in:   "[[Foo.MD]]",
			old:  "Foo", new: "Bar",
			want: "[[Bar.MD]]",
		},
		{
			name: "md extension with heading and alias",
			in:   "[[Foo.md#Section|disp]]",
			old:  "Foo", new: "Bar",
			want: "[[Bar.md#Section|disp]]",
		},
		{
			name: "old title supplied with md suffix",
			in:   "[[Foo.md]] and [[Foo]]",
			old:  "Foo.md", new: "Bar",
			want: "[[Bar.md]] and [[Bar]]",
		},
		{
			name: "new title supplied with md suffix is normalized",
			in:   "[[Foo]] and [[Foo.md]]",
			old:  "Foo", new: "Bar.md",
			want: "[[Bar]] and [[Bar.md]]",
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := RewriteTitle(tc.in, tc.old, tc.new)
			if got != tc.want {
				t.Fatalf("RewriteTitle\n  got:  %q\n  want: %q", got, tc.want)
			}
		})
	}
}

func TestRewriteMoveHandlesMDExtension(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		in               string
		oldPath, newPath string
		want             string
	}{
		{
			name:    "md extension preserved on move",
			in:      "[[Books/Foo.md]]",
			oldPath: "Books/Foo", newPath: "Refs/Books/Foo",
			want: "[[Refs/Books/Foo.md]]",
		},
		{
			name:    "bare and md form both rewritten",
			in:      "[[Books/Foo.md]] and [[Books/Foo]]",
			oldPath: "Books/Foo", newPath: "Refs/Books/Foo",
			want: "[[Refs/Books/Foo.md]] and [[Refs/Books/Foo]]",
		},
		{
			name:    "uppercase MD extension preserved",
			in:      "[[Books/Foo.MD]]",
			oldPath: "Books/Foo", newPath: "Refs/Books/Foo",
			want: "[[Refs/Books/Foo.MD]]",
		},
		{
			name:    "newPath supplied with md suffix is normalized",
			in:      "[[Books/Foo]] and [[Books/Foo.md]]",
			oldPath: "Books/Foo", newPath: "Refs/Books/Foo.md",
			want: "[[Refs/Books/Foo]] and [[Refs/Books/Foo.md]]",
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := RewriteMove(tc.in, tc.oldPath, tc.newPath)
			if got != tc.want {
				t.Fatalf("RewriteMove\n  got:  %q\n  want: %q", got, tc.want)
			}
		})
	}
}

func TestRewriteMDExtensionRoundTrip(t *testing.T) {
	t.Parallel()

	in := "[[Foo.md]] and [[Refs/Foo.md#H|disp]]"
	out := RewriteTitle(in, "Foo", "Bar")
	links := Parse(out)
	if len(links) != 2 {
		t.Fatalf("expected 2 links after rename, got %d in %q", len(links), out)
	}
	if links[0].Target != "Bar.md" || links[0].Heading != "" || links[0].Alias != "" {
		t.Errorf("first link = %+v, want Target=Bar.md", links[0])
	}
	if links[1].Target != "Refs/Bar.md" || links[1].Heading != "H" || links[1].Alias != "disp" {
		t.Errorf("second link = %+v", links[1])
	}
}
