package wikilink

import "testing"

func TestRewrite(t *testing.T) {
	t.Parallel()
	in := "see [[A]] and [[B]]"
	got := Rewrite(in, func(l Link) (string, bool) {
		if l.Target == "A" {
			return "[[A2]]", true
		}
		return "", false
	})
	want := "see [[A2]] and [[B]]"
	if got != want {
		t.Fatalf("Rewrite = %q, want %q", got, want)
	}
}

func TestRewriteEmpty(t *testing.T) {
	t.Parallel()
	in := "no links here"
	got := Rewrite(in, func(l Link) (string, bool) { return "x", true })
	if got != in {
		t.Fatalf("Rewrite = %q, want %q (unchanged)", got, in)
	}
}

func TestRewriteTitle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		old  string
		new  string
		want string
	}{
		{
			name: "bare match",
			in:   "see [[Foo]] please",
			old:  "Foo", new: "Bar",
			want: "see [[Bar]] please",
		},
		{
			name: "case insensitive",
			in:   "see [[foo]] and [[FOO]] and [[Foo]]",
			old:  "Foo", new: "Bar",
			want: "see [[Bar]] and [[Bar]] and [[Bar]]",
		},
		{
			name: "preserves alias",
			in:   "[[Foo|the foo]]",
			old:  "Foo", new: "Bar",
			want: "[[Bar|the foo]]",
		},
		{
			name: "preserves heading",
			in:   "[[Foo#Section]]",
			old:  "Foo", new: "Bar",
			want: "[[Bar#Section]]",
		},
		{
			name: "preserves heading and alias",
			in:   "[[Foo#Section|disp]]",
			old:  "Foo", new: "Bar",
			want: "[[Bar#Section|disp]]",
		},
		{
			name: "folder prefix preserved",
			in:   "[[References/Foo]]",
			old:  "Foo", new: "Bar",
			want: "[[References/Bar]]",
		},
		{
			name: "non-match left alone",
			in:   "[[Other]] [[Foo]]",
			old:  "Foo", new: "Bar",
			want: "[[Other]] [[Bar]]",
		},
		{
			name: "no match in code",
			in:   "`[[Foo]]` outside [[Foo]]",
			old:  "Foo", new: "Bar",
			want: "`[[Foo]]` outside [[Bar]]",
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

func TestRewriteMove(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		in               string
		oldPath, newPath string
		want             string
	}{
		{
			name:    "folder prefixed match",
			in:      "[[Books/Foo]] and [[Books/Foo|disp]]",
			oldPath: "Books/Foo", newPath: "References/Books/Foo",
			want: "[[References/Books/Foo]] and [[References/Books/Foo|disp]]",
		},
		{
			name:    "bare title untouched",
			in:      "[[Foo]] is bare and [[Books/Foo]] is prefixed",
			oldPath: "Books/Foo", newPath: "References/Books/Foo",
			want: "[[Foo]] is bare and [[References/Books/Foo]] is prefixed",
		},
		{
			name:    "case insensitive path match",
			in:      "[[books/foo]] [[BOOKS/FOO]]",
			oldPath: "Books/Foo", newPath: "References/Books/Foo",
			want: "[[References/Books/Foo]] [[References/Books/Foo]]",
		},
		{
			name:    "heading preserved",
			in:      "[[Books/Foo#Section|alias]]",
			oldPath: "Books/Foo", newPath: "References/Books/Foo",
			want: "[[References/Books/Foo#Section|alias]]",
		},
		{
			name:    "different path untouched",
			in:      "[[Other/Foo]] [[Books/Foo]]",
			oldPath: "Books/Foo", newPath: "References/Books/Foo",
			want: "[[Other/Foo]] [[References/Books/Foo]]",
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

func TestRewriteInsideFrontmatter(t *testing.T) {
	t.Parallel()
	in := "---\ncategories:\n  - '[[Books]]'\n  - '[[References]]'\nparent: '[[Books]]'\n---\nbody links to [[Books]]\n"
	got := RewriteTitle(in, "Books", "Reading")
	want := "---\ncategories:\n  - '[[Reading]]'\n  - '[[References]]'\nparent: '[[Reading]]'\n---\nbody links to [[Reading]]\n"
	if got != want {
		t.Fatalf("RewriteTitle in frontmatter\n  got:  %q\n  want: %q", got, want)
	}
}
