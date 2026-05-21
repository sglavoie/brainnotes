package wikilink

import (
	"reflect"
	"testing"
)

func TestParseVariants(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want []Link
	}{
		{
			name: "bare title",
			in:   "see [[Books]] for more",
			want: []Link{{
				Raw: "[[Books]]", Target: "Books",
				Start: 4, End: 13,
			}},
		},
		{
			name: "with alias",
			in:   "[[Books|reading list]]",
			want: []Link{{
				Raw: "[[Books|reading list]]", Target: "Books", Alias: "reading list",
				Start: 0, End: 22,
			}},
		},
		{
			name: "with heading",
			in:   "[[Books#Favourites]]",
			want: []Link{{
				Raw: "[[Books#Favourites]]", Target: "Books", Heading: "Favourites",
				Start: 0, End: 20,
			}},
		},
		{
			name: "heading and alias",
			in:   "[[Books#Favs|fave]]",
			want: []Link{{
				Raw: "[[Books#Favs|fave]]", Target: "Books", Heading: "Favs", Alias: "fave",
				Start: 0, End: 19,
			}},
		},
		{
			name: "folder prefixed",
			in:   "[[References/Books]]",
			want: []Link{{
				Raw: "[[References/Books]]", Target: "References/Books", Folder: "References",
				Start: 0, End: 20,
			}},
		},
		{
			name: "folder + heading + alias",
			in:   "[[F/G/Note#H|A]]",
			want: []Link{{
				Raw: "[[F/G/Note#H|A]]", Target: "F/G/Note", Folder: "F/G",
				Heading: "H", Alias: "A",
				Start: 0, End: 16,
			}},
		},
		{
			name: "multiple in one line",
			in:   "[[A]] and [[B]]",
			want: []Link{
				{Raw: "[[A]]", Target: "A", Start: 0, End: 5},
				{Raw: "[[B]]", Target: "B", Start: 10, End: 15},
			},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Parse(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Parse(%q)\n  got:  %+v\n  want: %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseTitleAccessor(t *testing.T) {
	t.Parallel()
	links := Parse("[[F/G/Note]] [[Bare]]")
	if len(links) != 2 {
		t.Fatalf("expected 2 links, got %d", len(links))
	}
	if links[0].Title() != "Note" {
		t.Errorf("folder link Title() = %q, want %q", links[0].Title(), "Note")
	}
	if links[1].Title() != "Bare" {
		t.Errorf("bare link Title() = %q, want %q", links[1].Title(), "Bare")
	}
}

func TestParseSkipsCodeBlocks(t *testing.T) {
	t.Parallel()

	t.Run("fenced backticks", func(t *testing.T) {
		t.Parallel()
		in := "Outside [[Yes]]\n```\nInside [[No]]\n```\nAfter [[Also]]"
		got := Parse(in)
		targets := collectTargets(got)
		want := []string{"Yes", "Also"}
		if !reflect.DeepEqual(targets, want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	})

	t.Run("fenced tildes with info string", func(t *testing.T) {
		t.Parallel()
		in := "[[Open]]\n~~~go\n[[Hidden]]\n~~~\n[[Close]]"
		targets := collectTargets(Parse(in))
		want := []string{"Open", "Close"}
		if !reflect.DeepEqual(targets, want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	})

	t.Run("longer fence allows shorter inside", func(t *testing.T) {
		t.Parallel()
		in := "````\n```\n[[NestedFence]]\n```\n````\n[[After]]"
		targets := collectTargets(Parse(in))
		want := []string{"After"}
		if !reflect.DeepEqual(targets, want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	})

	t.Run("inline code", func(t *testing.T) {
		t.Parallel()
		in := "Real [[Yes]] but `[[No]]` not this"
		targets := collectTargets(Parse(in))
		want := []string{"Yes"}
		if !reflect.DeepEqual(targets, want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	})

	t.Run("inline code double backticks", func(t *testing.T) {
		t.Parallel()
		in := "look ``[[Hidden]]`` and [[Shown]]"
		targets := collectTargets(Parse(in))
		want := []string{"Shown"}
		if !reflect.DeepEqual(targets, want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	})

	t.Run("unmatched backtick is not a span", func(t *testing.T) {
		t.Parallel()
		in := "stray ` and a real [[Link]]"
		targets := collectTargets(Parse(in))
		want := []string{"Link"}
		if !reflect.DeepEqual(targets, want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	})

	t.Run("indented code block after blank", func(t *testing.T) {
		t.Parallel()
		in := "paragraph one\n\n    [[Hidden]]\n    more code\n\n[[Visible]]"
		targets := collectTargets(Parse(in))
		want := []string{"Visible"}
		if !reflect.DeepEqual(targets, want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	})

	t.Run("indented continuation not code", func(t *testing.T) {
		t.Parallel()
		// No preceding blank line — this is paragraph continuation, not code.
		in := "lead in\n    [[Visible]]\n"
		targets := collectTargets(Parse(in))
		want := []string{"Visible"}
		if !reflect.DeepEqual(targets, want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	})

	t.Run("tab indented code at doc start", func(t *testing.T) {
		t.Parallel()
		in := "\t[[Hidden]]\n\n[[Visible]]"
		targets := collectTargets(Parse(in))
		want := []string{"Visible"}
		if !reflect.DeepEqual(targets, want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	})

	t.Run("backtick fence with backtick in info string is not a fence", func(t *testing.T) {
		t.Parallel()
		// Per CommonMark, a backtick fence opener's info string may not
		// contain backtick characters. So ```foo`bar is NOT a valid fence.
		// The subsequent bare ``` opens a real fence that masks [[After]].
		in := "[[Before]]\n```foo`bar\n[[Visible]]\n```\n[[After]]"
		targets := collectTargets(Parse(in))
		want := []string{"Before", "Visible"}
		if !reflect.DeepEqual(targets, want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	})

	t.Run("backtick fence with backtick in info string full round trip", func(t *testing.T) {
		t.Parallel()
		// Ensure the invalid fence opener doesn't mask the link after it,
		// and that a real fence still works afterward.
		in := "[[A]]\n```x`y\n[[B]]\ntext\n```\n[[C]]\n```\n[[D]]"
		targets := collectTargets(Parse(in))
		// ```x`y is not a fence (backtick in info); [[B]] is visible.
		// First bare ``` opens a real fence; [[C]] is masked.
		// Second bare ``` closes the fence; [[D]] is visible.
		want := []string{"A", "B", "D"}
		if !reflect.DeepEqual(targets, want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	})

	t.Run("tilde fence with backtick in info string is valid", func(t *testing.T) {
		t.Parallel()
		// Tilde fences have no restriction on backticks in the info string.
		in := "[[Before]]\n~~~foo`bar\n[[Hidden]]\n~~~\n[[After]]"
		targets := collectTargets(Parse(in))
		want := []string{"Before", "After"}
		if !reflect.DeepEqual(targets, want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
	})
}

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

func TestParseInsideYAMLFrontmatter(t *testing.T) {
	t.Parallel()
	in := "---\ncategories:\n  - '[[Books]]'\n  - '[[References]]'\nparent: '[[Index]]'\n---\nbody\n"
	got := collectTargets(Parse(in))
	want := []string{"Books", "References", "Index"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

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

func TestRewriteSkipsCodeBlocks(t *testing.T) {
	t.Parallel()
	in := "[[A]] real\n```\n[[A]] in code\n```\n[[A]] real again"
	got := Rewrite(in, func(l Link) (string, bool) {
		return "[[B]]", true
	})
	want := "[[B]] real\n```\n[[A]] in code\n```\n[[B]] real again"
	if got != want {
		t.Fatalf("Rewrite\n  got:  %q\n  want: %q", got, want)
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

func TestFormatLink(t *testing.T) {
	t.Parallel()
	tests := []struct {
		target, heading, alias, want string
	}{
		{"A", "", "", "[[A]]"},
		{"A", "H", "", "[[A#H]]"},
		{"A", "", "B", "[[A|B]]"},
		{"A", "H", "B", "[[A#H|B]]"},
		{"F/A", "H", "B", "[[F/A#H|B]]"},
	}
	for _, tc := range tests {
		got := FormatLink(tc.target, tc.heading, tc.alias)
		if got != tc.want {
			t.Errorf("FormatLink(%q,%q,%q) = %q, want %q", tc.target, tc.heading, tc.alias, got, tc.want)
		}
	}
}
