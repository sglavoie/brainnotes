package wikilink

import "testing"

func TestRewriteTitle_EmptyOldTitleNoMatch(t *testing.T) {
	t.Parallel()

	in := "[[Folder/]] [[A]]"
	got := RewriteTitle(in, "", "New")

	if got != in {
		t.Fatalf("RewriteTitle with empty old\n  got:  %q\n  want: %q (unchanged)", got, in)
	}
}

func TestRewriteDoesNotAlterWhitespaceOnlyHeadingOrAlias(t *testing.T) {
	t.Parallel()

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

func TestRewriteTitleRejectsInvalidNewTitle(t *testing.T) {
	t.Parallel()

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

func TestFirstReservedTargetRune(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want rune
		ok   bool
	}{
		{in: "Plain", ok: false},
		{in: "Foo#Bar", want: '#', ok: true},
		{in: "Foo|Bar", want: '|', ok: true},
		{in: "Foo[Bar", want: '[', ok: true},
		{in: "Foo]Bar", want: ']', ok: true},
		{in: "Foo\nBar", want: '\n', ok: true},
	}
	for _, tc := range tests {
		got, ok := FirstReservedTargetRune(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("FirstReservedTargetRune(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestRewriteMoveRejectsInvalidNewPath(t *testing.T) {
	t.Parallel()

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

func TestRewriteTitleRejectsHashOrPipeInNewTitle(t *testing.T) {
	t.Parallel()

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

func TestRewriteTitleIdentityRenameIsNoOp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
	}{
		{"padded bare", "see [[ Foo ]] here"},
		{"padded with alias", "[[ Foo | the alias ]]"},
		{"padded with heading", "[[ Foo # Section ]]"},
		{"padded folder prefix", "[[ Refs/Foo ]]"},
		{"already canonical", "[[Foo]] and [[Refs/Foo#H|a]]"},
		{"md extension preserved", "[[Foo.md]] and [[Refs/Foo.md]]"},
		{"mixed with non-match", "[[ Foo ]] and [[Other]]"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := RewriteTitle(tc.in, "Foo", "Foo")
			if got != tc.in {
				t.Fatalf("RewriteTitle identity\n  got:  %q\n  want: %q (unchanged)", got, tc.in)
			}
		})
	}
}

func TestRewriteMoveIdentityIsNoOp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
	}{
		{"padded folder/title", "[[ Books/Foo ]]"},
		{"padded with heading", "[[ Books/Foo # Section ]]"},
		{"padded with alias", "[[ Books/Foo | display ]]"},
		{"already canonical", "[[Books/Foo]] and [[Books/Foo#H|a]]"},
		{"md extension preserved", "[[Books/Foo.md]]"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := RewriteMove(tc.in, "Books/Foo", "Books/Foo")
			if got != tc.in {
				t.Fatalf("RewriteMove identity\n  got:  %q\n  want: %q (unchanged)", got, tc.in)
			}
		})
	}
}

func TestRewriteTitleCaseOnlyChangeStillApplies(t *testing.T) {
	t.Parallel()

	in := "[[ Foo ]] and [[Refs/foo]]"
	got := RewriteTitle(in, "Foo", "foo")
	want := "[[foo]] and [[Refs/foo]]"
	if got != want {
		t.Fatalf("RewriteTitle case-only\n  got:  %q\n  want: %q", got, want)
	}
}

func TestRewriteTitleSubstringTitleNotMatched(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "longer title not matched",
			in:   "[[FooBar]] [[Foo]] [[BarFoo]]",
			want: "[[FooBar]] [[NewFoo]] [[BarFoo]]",
		},
		{
			name: "folder-prefixed longer title not matched",
			in:   "[[Refs/FooBar]] [[Refs/Foo]]",
			want: "[[Refs/FooBar]] [[Refs/NewFoo]]",
		},
		{
			name: "case-insensitive substring not matched",
			in:   "[[fooBar]] [[FOO]]",
			want: "[[fooBar]] [[NewFoo]]",
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := RewriteTitle(tc.in, "Foo", "NewFoo")
			if got != tc.want {
				t.Fatalf("RewriteTitle\n  got:  %q\n  want: %q", got, tc.want)
			}
		})
	}
}

func TestRewriteMoveSubstringPathNotMatched(t *testing.T) {
	t.Parallel()

	in := "[[Books/Foo]] [[Books/FooBar]] [[Books/Foo/Sub]]"
	got := RewriteMove(in, "Books/Foo", "Refs/Foo")
	want := "[[Refs/Foo]] [[Books/FooBar]] [[Books/Foo/Sub]]"
	if got != want {
		t.Fatalf("RewriteMove\n  got:  %q\n  want: %q", got, want)
	}
}
