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
	// Renaming Foo to Bar inside `[[[Foo]]]` must preserve the surrounding
	// literal brackets — only the inner `[[Foo]]` is the wikilink.
	got := RewriteTitle("see [[[Foo]]] now", "Foo", "Bar")
	want := "see [[[Bar]]] now"
	if got != want {
		t.Fatalf("RewriteTitle\n  got:  %q\n  want: %q", got, want)
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

func TestRewriteTitleRejectsCorruptingNewTitle(t *testing.T) {
	t.Parallel()
	// New titles that would produce a malformed or unparseable link must be
	// rejected so the rewrite is a no-op rather than silently corrupting the
	// vault. These were previously accepted and produced links like `[[]]`,
	// `[[ ]]`, or `[[Folder/]]` that Parse no longer recognizes as wikilinks.
	tests := []struct {
		name     string
		newTitle string
	}{
		{"md ext only", ".md"},
		{"md ext only uppercase", ".MD"},
		{"whitespace only", "   "},
		{"trailing slash", "Bar/"},
		{"tab only", "\t"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, in := range []string{
				"see [[Foo]] here",
				"[[Folder/Foo]]",
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

func TestRewriteTitleRejectsNewTitleThatTrimsToInvalid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		newTitle string
	}{
		{"padded trailing slash", "Bar/ "},
		{"padded md ext only", " .md "},
		{"padded folder md ext only", "Refs/.md\t"},
		{"padded slash only", " / "},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := "[[Foo]] and [[Refs/Foo.md#H|disp]]"
			got := RewriteTitle(in, "Foo", tc.newTitle)
			if got != in {
				t.Fatalf("RewriteTitle(%q, Foo, %q)\n  got:  %q\n  want: %q (unchanged)", in, tc.newTitle, got, in)
			}
		})
	}
}

func TestRewriteMoveRejectsCorruptingNewPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		newPath string
	}{
		{"trailing slash", "Refs/Bar/"},
		{"trailing slash bare", "Bar/"},
		{"md only", ".md"},
		{"folder slash md only", "Refs/.md"},
		{"whitespace only", "  "},
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

func TestRewriteMoveRejectsNewPathThatTrimsToInvalid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		newPath string
	}{
		{"padded trailing slash", "Refs/Bar/ "},
		{"padded md ext only", " .md "},
		{"padded folder md ext only", "Refs/.md\t"},
		{"padded slash only", " / "},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := "[[Books/Foo]] and [[Books/Foo.md#H|disp]]"
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

func TestRewriteSkipsMultilineInlineCodeSpan(t *testing.T) {
	t.Parallel()
	in := "before `[[Foo]]\n[[Foo]]` after [[Foo]]"
	got := RewriteTitle(in, "Foo", "Bar")
	want := "before `[[Foo]]\n[[Foo]]` after [[Bar]]"
	if got != want {
		t.Fatalf("RewriteTitle\n  got:  %q\n  want: %q", got, want)
	}
}

func TestRewriteSkipsBlockquotedFencedCode(t *testing.T) {
	t.Parallel()
	in := "[[Foo]]\n> ```\n> [[Foo]]\n> ```\n[[Foo]]"
	got := RewriteTitle(in, "Foo", "Bar")
	want := "[[Bar]]\n> ```\n> [[Foo]]\n> ```\n[[Bar]]"
	if got != want {
		t.Fatalf("RewriteTitle\n  got:  %q\n  want: %q", got, want)
	}
}
