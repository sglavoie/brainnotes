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

func TestParseTrimsWhitespace(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                                           string
		in                                             string
		wantTarget, wantFolder, wantHeading, wantAlias string
		wantTitle                                      string
	}{
		{
			name:       "leading and trailing space in bare target",
			in:         "[[ Foo ]]",
			wantTarget: "Foo", wantTitle: "Foo",
		},
		{
			name:       "space before alias separator",
			in:         "[[Foo |alias]]",
			wantTarget: "Foo", wantAlias: "alias", wantTitle: "Foo",
		},
		{
			name:       "space after alias separator",
			in:         "[[Foo| alias ]]",
			wantTarget: "Foo", wantAlias: "alias", wantTitle: "Foo",
		},
		{
			name:       "space around heading",
			in:         "[[Foo # Section ]]",
			wantTarget: "Foo", wantHeading: "Section", wantTitle: "Foo",
		},
		{
			name:       "space around heading and alias",
			in:         "[[ Foo # Section | disp ]]",
			wantTarget: "Foo", wantHeading: "Section", wantAlias: "disp", wantTitle: "Foo",
		},
		{
			name:       "tabs trimmed too",
			in:         "[[\tFoo\t]]",
			wantTarget: "Foo", wantTitle: "Foo",
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Parse(tc.in)
			if len(got) != 1 {
				t.Fatalf("Parse(%q) returned %d links, want 1", tc.in, len(got))
			}
			l := got[0]
			if l.Target != tc.wantTarget {
				t.Errorf("Target = %q, want %q", l.Target, tc.wantTarget)
			}
			if l.Folder != tc.wantFolder {
				t.Errorf("Folder = %q, want %q", l.Folder, tc.wantFolder)
			}
			if l.Heading != tc.wantHeading {
				t.Errorf("Heading = %q, want %q", l.Heading, tc.wantHeading)
			}
			if l.Alias != tc.wantAlias {
				t.Errorf("Alias = %q, want %q", l.Alias, tc.wantAlias)
			}
			if l.Title() != tc.wantTitle {
				t.Errorf("Title() = %q, want %q", l.Title(), tc.wantTitle)
			}
		})
	}
}

func TestParseRejectsAllWhitespaceTarget(t *testing.T) {
	t.Parallel()
	// `[[   ]]` and `[[ \t ]]` are not real wikilinks once trimmed.
	for _, in := range []string{"[[   ]]", "[[\t\t]]", "[[ | alias]]"} {
		if got := Parse(in); len(got) != 0 {
			t.Errorf("Parse(%q) = %+v, want no matches", in, got)
		}
	}
}

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

func TestParseEmbedPreservesPrefix(t *testing.T) {
	t.Parallel()
	in := "before ![[Image]] after [[Note]]"
	links := Parse(in)
	if len(links) != 2 {
		t.Fatalf("expected 2 links, got %d", len(links))
	}
	if links[0].Target != "Image" || links[1].Target != "Note" {
		t.Fatalf("targets = %q,%q want Image,Note", links[0].Target, links[1].Target)
	}
	// The leading `!` is not part of the Link span; rewriting must leave it.
	got := RewriteTitle(in, "Image", "Picture")
	want := "before ![[Picture]] after [[Note]]"
	if got != want {
		t.Fatalf("RewriteTitle\n  got:  %q\n  want: %q", got, want)
	}
}

func TestParseSkipsFencedCodeWithCRLF(t *testing.T) {
	t.Parallel()
	in := "[[Before]]\r\n```\r\n[[Inside]]\r\n```\r\n[[After]]"
	got := collectTargets(Parse(in))
	want := []string{"Before", "After"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestParseSkipsIndentedCodeAfterCRLFBlank(t *testing.T) {
	t.Parallel()
	in := "para\r\n\r\n    [[Hidden]]\r\n\r\n[[Visible]]"
	got := collectTargets(Parse(in))
	want := []string{"Visible"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestParseUnclosedFenceMasksRest(t *testing.T) {
	t.Parallel()
	// An unclosed fence should mask everything that follows it; we err on the
	// side of not rewriting documentation samples.
	in := "[[Before]]\n```\n[[Inside]]\n[[StillInside]]"
	got := collectTargets(Parse(in))
	want := []string{"Before"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestParseLongerFenceAllowsBacktickRunsInside(t *testing.T) {
	t.Parallel()
	// A 4-backtick fence is closed only by 4+ backticks; an internal 3-backtick
	// run must not prematurely close it.
	in := "````\n```\n[[NotEscaping]]\n```\n````\n[[After]]"
	got := collectTargets(Parse(in))
	want := []string{"After"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestParseDifferentFenceCharsDontClose(t *testing.T) {
	t.Parallel()
	// A tilde fence inside a backtick fence is not a closer.
	in := "```\n~~~\n[[Inside]]\n~~~\n```\n[[After]]"
	got := collectTargets(Parse(in))
	want := []string{"After"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
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

func TestParseTripleBracketsDoesNotConsumeBracket(t *testing.T) {
	t.Parallel()
	// `[[[Foo]]]` is a literal `[` followed by a `[[Foo]]` wikilink and a
	// trailing `]`. The previous regex would have matched `[[[Foo]]` with
	// target `[Foo`, which is not a valid Obsidian title.
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

func TestRewriteTripleBrackets(t *testing.T) {
	t.Parallel()
	// Renaming Foo to Bar inside `[[[Foo]]]` must preserve the surrounding
	// literal brackets — only the inner `[[Foo]]` is the wikilink.
	got := RewriteTitle("see [[[Foo]]] now", "Foo", "Bar")
	want := "see [[[Bar]]] now"
	if got != want {
		t.Fatalf("RewriteTitle\n  got:  %q\n  want: %q", got, want)
	}
}

func TestParseRejectsTrailingSlashTarget(t *testing.T) {
	t.Parallel()
	// Targets ending with "/" have an empty title component after the last
	// slash. Obsidian does not allow empty note names, so these are invalid.
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

func TestParseConsecutiveLinks(t *testing.T) {
	t.Parallel()
	// Two links with no gap between them must both be parsed.
	links := Parse("[[A]][[B]]")
	if len(links) != 2 {
		t.Fatalf("expected 2 links, got %d", len(links))
	}
	if links[0].Target != "A" || links[0].Start != 0 || links[0].End != 5 {
		t.Errorf("link 0: got %+v", links[0])
	}
	if links[1].Target != "B" || links[1].Start != 5 || links[1].End != 10 {
		t.Errorf("link 1: got %+v", links[1])
	}
}

func TestParseLinksAtBoundaries(t *testing.T) {
	t.Parallel()
	// Link at the very start and very end of the string.
	in := "[[Start]]end"
	links := Parse(in)
	if len(links) != 1 || links[0].Start != 0 || links[0].End != 9 {
		t.Fatalf("start boundary: got %+v", links)
	}

	in = "begin[[End]]"
	links = Parse(in)
	if len(links) != 1 || links[0].Start != 5 || links[0].End != 12 {
		t.Fatalf("end boundary: got %+v", links)
	}
}

func TestRewriteTitle_EmptyOldTitleNoMatch(t *testing.T) {
	t.Parallel()
	// After fixing the trailing-slash rejection, RewriteTitle with an empty
	// oldTitle must not match anything (no valid link has an empty Title()).
	in := "[[Folder/]] [[A]]"
	got := RewriteTitle(in, "", "New")
	// [[Folder/]] is now rejected by Parse, so it's not a link.
	// [[A]] has Title()="A" which != "", so no match.
	if got != in {
		t.Fatalf("RewriteTitle with empty old\n  got:  %q\n  want: %q (unchanged)", got, in)
	}
}

func TestParseLeadingSlash(t *testing.T) {
	t.Parallel()
	// [[/Note]] has folder="" (since idx==0 means empty prefix) and
	// Title()="/Note". This is an unusual link but Obsidian does resolve it.
	links := Parse("[[/Note]]")
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(links))
	}
	l := links[0]
	if l.Target != "/Note" {
		t.Errorf("Target = %q, want %q", l.Target, "/Note")
	}
	if l.Folder != "" {
		t.Errorf("Folder = %q, want %q", l.Folder, "")
	}
	if l.Title() != "/Note" {
		t.Errorf("Title() = %q, want %q", l.Title(), "/Note")
	}
}

func TestParseHashInHeading(t *testing.T) {
	t.Parallel()
	// The heading capture group allows # characters inside it, so
	// [[Note#H1#H2]] parses as Target="Note", Heading="H1#H2".
	links := Parse("[[Note#H1#H2]]")
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(links))
	}
	if links[0].Target != "Note" {
		t.Errorf("Target = %q, want %q", links[0].Target, "Note")
	}
	if links[0].Heading != "H1#H2" {
		t.Errorf("Heading = %q, want %q", links[0].Heading, "H1#H2")
	}
}

func TestParsePipeInAlias(t *testing.T) {
	t.Parallel()
	// The alias capture group allows | characters inside it, so
	// [[Note|a|b]] parses as Target="Note", Alias="a|b".
	links := Parse("[[Note|a|b]]")
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(links))
	}
	if links[0].Alias != "a|b" {
		t.Errorf("Alias = %q, want %q", links[0].Alias, "a|b")
	}
}

func TestParseEmptyHeadingOrAlias(t *testing.T) {
	t.Parallel()
	// [[Foo#]] and [[Foo|]] and [[Foo#|Bar]] should all be rejected since
	// the regex requires at least one character in each capture group.
	// Whitespace-only heading/alias (e.g. [[Foo#   ]]) are also rejected
	// because the separator was present but the content was blank — FormatLink
	// would silently drop the separator on rewrite, changing the link form.
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

func TestParseUnicodeTarget(t *testing.T) {
	t.Parallel()
	links := Parse("[[Café]]")
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(links))
	}
	if links[0].Target != "Café" {
		t.Errorf("Target = %q, want %q", links[0].Target, "Café")
	}
}

func TestRewriteDoesNotAlterWhitespaceOnlyHeadingOrAlias(t *testing.T) {
	t.Parallel()
	// Links with whitespace-only heading/alias are rejected by Parse, so
	// Rewrite must leave them completely untouched. Before the fix, an
	// identity rename would silently drop the # or | separator.
	tests := []struct {
		name string
		in   string
	}{
		{"whitespace heading identity rename", "[[Foo#   ]]"},
		{"whitespace alias identity rename", "[[Foo|   ]]"},
		{"whitespace heading and alias", "[[Foo#   |   ]]"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := RewriteTitle(tc.in, "Foo", "Bar")
			if got != tc.in {
				t.Fatalf("RewriteTitle(%q, Foo, Bar)\n  got:  %q\n  want: %q (unchanged)", tc.in, got, tc.in)
			}
		})
	}
}

func TestRewriteConsecutiveLinks(t *testing.T) {
	t.Parallel()
	// Rewriting consecutive links with no space between them.
	got := Rewrite("[[A]][[B]]", func(l Link) (string, bool) {
		return "[[" + l.Target + "2]]", true
	})
	want := "[[A2]][[B2]]"
	if got != want {
		t.Fatalf("Rewrite consecutive\n  got:  %q\n  want: %q", got, want)
	}
}

func TestParseEmptyAndWhitespaceInput(t *testing.T) {
	t.Parallel()
	// Empty/blank inputs must not panic and must return no links.
	for _, in := range []string{"", "\n", "\r\n", "   ", "\t\n\n  "} {
		if got := Parse(in); len(got) != 0 {
			t.Errorf("Parse(%q) = %+v, want no matches", in, got)
		}
	}
}

func TestRewriteTitleRejectsInvalidNewTitle(t *testing.T) {
	t.Parallel()
	// An empty newTitle would collapse the link to `[[]]` or `[[Folder/]]`,
	// neither of which round-trips through Parse. Brackets and line breaks
	// in newTitle similarly corrupt the `[[...]]` span. RewriteTitle must
	// leave the text untouched in these cases.
	tests := []struct {
		name     string
		newTitle string
	}{
		{"empty", ""},
		{"contains close bracket", "Bar]Bad"},
		{"contains open bracket", "Bar[Bad"},
		{"contains LF", "Bar\nBad"},
		{"contains CR", "Bar\rBad"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, in := range []string{
				"see [[Foo]] here",
				"folder [[Refs/Foo]] case",
				"[[Foo#Section|alias]]",
			} {
				got := RewriteTitle(in, "Foo", tc.newTitle)
				if got != in {
					t.Errorf("RewriteTitle(%q, Foo, %q)\n  got:  %q\n  want: %q (unchanged)", in, tc.newTitle, got, in)
				}
			}
		})
	}
}

func TestRewriteMoveRejectsInvalidNewPath(t *testing.T) {
	t.Parallel()
	// Same invariant as RewriteTitle: empty/bracket/newline newPath would
	// produce a malformed link, so RewriteMove must leave the input as-is.
	tests := []struct {
		name    string
		newPath string
	}{
		{"empty", ""},
		{"contains close bracket", "X]Y/Z"},
		{"contains open bracket", "X[Y/Z"},
		{"contains LF", "X\nY/Z"},
		{"contains CR", "X\rY/Z"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := "[[Books/Foo]] and [[Books/Foo#H|a]]"
			got := RewriteMove(in, "Books/Foo", tc.newPath)
			if got != in {
				t.Errorf("RewriteMove(%q, ..., %q)\n  got:  %q\n  want: %q (unchanged)", in, tc.newPath, got, in)
			}
		})
	}
}

func TestRewriteRoundTripAfterRename(t *testing.T) {
	t.Parallel()
	// After a valid rename, the output must re-parse to a link with the new
	// target. This guards against accidental corruption of link structure.
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

func TestRewriteTitleHandlesMDExtension(t *testing.T) {
	t.Parallel()
	// Obsidian treats `[[Note]]` and `[[Note.md]]` as the same link target.
	// Rename must rewrite both forms and preserve the explicit extension.
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

func TestRewriteTitleRejectsHashOrPipeInNewTitle(t *testing.T) {
	t.Parallel()
	// `#` and `|` in newTitle would silently introduce a heading or alias on
	// re-parse, breaking the rewrite invariant. RewriteTitle must reject these
	// and leave the input unchanged.
	tests := []struct {
		name     string
		newTitle string
	}{
		{"contains hash", "Bar#Section"},
		{"contains pipe", "Bar|alias"},
		{"contains hash and pipe", "Bar#a|b"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := "see [[Foo]] and [[Refs/Foo|disp]]"
			got := RewriteTitle(in, "Foo", tc.newTitle)
			if got != in {
				t.Fatalf("RewriteTitle(%q, Foo, %q)\n  got:  %q\n  want: %q (unchanged)", in, tc.newTitle, got, in)
			}
		})
	}
}

func TestRewriteMoveRejectsHashOrPipeInNewPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		newPath string
	}{
		{"contains hash", "Refs/Bar#H"},
		{"contains pipe", "Refs/Bar|a"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := "[[Books/Foo]] and [[Books/Foo#H|a]]"
			got := RewriteMove(in, "Books/Foo", tc.newPath)
			if got != in {
				t.Fatalf("RewriteMove(%q, ..., %q)\n  got:  %q\n  want: %q (unchanged)", in, tc.newPath, got, in)
			}
		})
	}
}

func TestRewriteMDExtensionRoundTrip(t *testing.T) {
	t.Parallel()
	// After a rename that touches a `.md`-suffixed link, the output must
	// re-parse to a link whose Title() reflects the new name (with .md
	// preserved), not a heading/alias drift.
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

func collectTargets(links []Link) []string {
	out := make([]string, 0, len(links))
	for _, l := range links {
		out = append(out, l.Target)
	}
	return out
}
