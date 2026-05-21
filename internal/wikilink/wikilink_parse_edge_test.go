package wikilink

import (
	"reflect"
	"testing"
)

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

func TestParseEmptyAndWhitespaceInput(t *testing.T) {
	t.Parallel()
	// Empty/blank inputs must not panic and must return no links.
	for _, in := range []string{"", "\n", "\r\n", "   ", "\t\n\n  "} {
		if got := Parse(in); len(got) != 0 {
			t.Errorf("Parse(%q) = %+v, want no matches", in, got)
		}
	}
}
