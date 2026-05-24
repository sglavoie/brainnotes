package wikilink

import (
	"reflect"
	"strings"
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

func TestParseSkipsBlockquotedFencedCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "backtick fence",
			in:   "[[Before]]\n> ```\n> [[Hidden]]\n> ```\n[[After]]",
			want: []string{"Before", "After"},
		},
		{
			name: "nested blockquote tilde fence",
			in:   "[[Before]]\n> > ~~~md\n> > [[Hidden]]\n> > ~~~\n[[After]]",
			want: []string{"Before", "After"},
		},
		{
			name: "indented blockquote fence",
			in:   "[[Before]]\n  > ```\n  > [[Hidden]]\n  > ```\n[[After]]",
			want: []string{"Before", "After"},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := collectTargets(Parse(tc.in))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("targets = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseQuotedCloserDoesNotCloseUnquotedFence(t *testing.T) {
	t.Parallel()
	// Per CommonMark, a fence opened outside a blockquote is not closed by a
	// blockquote-prefixed `> ```` line — the prefix is content, not structure.
	// Before the fix, isFenceCloser stripped blockquote prefixes unconditionally
	// so `> ```` closed an unquoted fence, exposing wikilinks that are actually
	// still inside the code block per CommonMark.
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "quoted closer does not close plain fence",
			in:   "[[Before]]\n```\n[[InsideA]]\n> ```\n[[InsideB]]",
			want: []string{"Before"},
		},
		{
			name: "quoted closer does not close plain tilde fence",
			in:   "[[Before]]\n~~~\n[[InsideA]]\n> ~~~\n[[InsideB]]",
			want: []string{"Before"},
		},
		{
			name: "plain closer still closes plain fence",
			in:   "[[Before]]\n```\n[[Inside]]\n```\n[[After]]",
			want: []string{"Before", "After"},
		},
		{
			name: "quoted opener accepts plain closer (conservative)",
			in:   "[[Before]]\n> ```\n> [[Inside]]\n```\n[[After]]",
			want: []string{"Before", "After"},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := collectTargets(Parse(tc.in))
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Parse(%q) targets = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestRewriteQuotedCloserDoesNotCloseUnquotedFence(t *testing.T) {
	t.Parallel()
	// Rewrite must not touch wikilinks that remain inside a code block when a
	// blockquoted `> ```` falsely "closes" an unquoted fence.
	in := "[[Foo]]\n```\n[[Foo]]\n> ```\n[[Foo]]"
	got := RewriteTitle(in, "Foo", "Bar")
	want := "[[Bar]]\n```\n[[Foo]]\n> ```\n[[Foo]]"
	if got != want {
		t.Fatalf("RewriteTitle\n  got:  %q\n  want: %q", got, want)
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

func TestParseSkipsMultilineInlineCodeSpan(t *testing.T) {
	t.Parallel()
	in := "before `[[Hidden]]\n[[StillHidden]]` after [[Visible]]"
	got := collectTargets(Parse(in))
	want := []string{"Visible"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestParseInlineCodeSpanDoesNotCrossBlankLine(t *testing.T) {
	t.Parallel()
	in := "`[[Visible]]\n\n[[AlsoVisible]]`"
	got := collectTargets(Parse(in))
	want := []string{"Visible", "AlsoVisible"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestParseHandlesLongFences(t *testing.T) {
	t.Parallel()
	// A fence longer than 3 backticks/tildes is closed only by a run of equal
	// or greater length. A shorter inner run must not terminate it.
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "10-backtick fence",
			in:   "``````````\n[[Hidden]]\n``````````\n[[After]]",
			want: []string{"After"},
		},
		{
			name: "opener 4 closer 5 (longer closer ok)",
			in:   "````\n[[Hidden]]\n`````\n[[After]]",
			want: []string{"After"},
		},
		{
			name: "opener 4 closer 3 (too short) then opener 4 closer 4",
			in:   "````\n[[Hidden]]\n```\n[[StillHidden]]\n````\n[[After]]",
			want: []string{"After"},
		},
		{
			name: "tilde long fence",
			in:   "~~~~~\n[[Hidden]]\n~~~\n[[StillHidden]]\n~~~~~\n[[After]]",
			want: []string{"After"},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := collectTargets(Parse(tc.in))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Parse(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseMixedFenceCharsDoNotCloseEachOther(t *testing.T) {
	t.Parallel()
	// Backtick and tilde fences are independent — a tilde line cannot close a
	// backtick fence and vice versa.
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "tilde does not close backticks",
			in:   "```\n[[Hidden]]\n~~~\n[[StillHidden]]\n```\n[[After]]",
			want: []string{"After"},
		},
		{
			name: "backtick does not close tildes",
			in:   "~~~\n[[Hidden]]\n```\n[[StillHidden]]\n~~~\n[[After]]",
			want: []string{"After"},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := collectTargets(Parse(tc.in))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Parse(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseFenceInfoStringEdgeCases(t *testing.T) {
	t.Parallel()
	// Backtick fences with backticks in the info string are not valid fences;
	// tilde fences allow backticks in the info string. Trailing-whitespace info
	// strings are valid for both. Info strings containing wikilinks must not
	// expose those wikilinks (the whole opener line is masked when it is a
	// valid fence opener).
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "fence opener line with wikilink-shaped info string",
			in:   "```[[InOpener]]\n[[Hidden]]\n```\n[[After]]",
			want: []string{"After"},
		},
		{
			name: "tilde opener with backtick in info",
			in:   "~~~ go`info\n[[Hidden]]\n~~~\n[[After]]",
			want: []string{"After"},
		},
		{
			name: "opener with only trailing whitespace info",
			in:   "```   \n[[Hidden]]\n```\n[[After]]",
			want: []string{"After"},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := collectTargets(Parse(tc.in))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Parse(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseFenceCloserIndentedUpToThreeSpaces(t *testing.T) {
	t.Parallel()
	// Per CommonMark, a fence closer may be indented up to 3 spaces. Four or
	// more spaces of indentation make it part of the code, not a closer.
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "closer indented 2 spaces",
			in:   "```\n[[Hidden]]\n  ```\n[[After]]",
			want: []string{"After"},
		},
		{
			name: "closer indented 3 spaces",
			in:   "```\n[[Hidden]]\n   ```\n[[After]]",
			want: []string{"After"},
		},
		{
			name: "closer indented 4 spaces is NOT a closer (still inside fence)",
			in:   "```\n[[Hidden]]\n    ```\n[[StillHidden]]",
			want: nil,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := collectTargets(Parse(tc.in))
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Parse(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseFenceContentContainsBlankLine(t *testing.T) {
	t.Parallel()
	// Blank lines inside a fenced block are still inside the block.
	in := "```\n[[A]]\n\n[[B]]\n```\n[[After]]"
	got := collectTargets(Parse(in))
	want := []string{"After"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse(%q) = %v, want %v", in, got, want)
	}
}

func TestParseStrayBacktickBeforeFence(t *testing.T) {
	t.Parallel()
	// An unmatched inline backtick on a preceding line must not prevent the
	// next line's fence opener from being recognized. The fence opens normally
	// and masks the inner wikilink.
	in := "abc `\n```\n[[Hidden]]\n```\nafter [[Visible]]"
	got := collectTargets(Parse(in))
	want := []string{"Visible"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse(%q) = %v, want %v", in, got, want)
	}
}

func TestParseInlineCodeAdjacentToWikilink(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "wikilink follows inline code with no space",
			in:   "`code`[[Visible]]",
			want: []string{"Visible"},
		},
		{
			name: "wikilink precedes inline code with no space",
			in:   "[[Visible]]`code`",
			want: []string{"Visible"},
		},
		{
			name: "back-to-back inline code spans",
			in:   "`a``b` outside [[Visible]]",
			want: []string{"Visible"},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := collectTargets(Parse(tc.in))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Parse(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseTitleAccessorEdgeCases(t *testing.T) {
	t.Parallel()
	// The Title() helper returns the slice after the last `/` in Target, or
	// the whole target when there is no slash. Pathological-looking targets
	// must not panic and must produce a sensible last segment.
	tests := []struct {
		name      string
		in        string
		wantTitle string
	}{
		{"leading slash treated as bare path", "[[/Note]]", "/Note"},
		{"double slash mid-path", "[[Folder//Sub]]", "Sub"},
		{"unicode segment", "[[Refs/Café]]", "Café"},
		{"dotted segment", "[[Refs/.foo]]", ".foo"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			links := Parse(tc.in)
			if len(links) != 1 {
				t.Fatalf("Parse(%q): got %d links, want 1", tc.in, len(links))
			}
			if links[0].Title() != tc.wantTitle {
				t.Errorf("Title() = %q, want %q", links[0].Title(), tc.wantTitle)
			}
		})
	}
}

func TestParseRecognizesCRLFFenceCloser(t *testing.T) {
	t.Parallel()
	// Fence open and close lines may carry CRLF terminators; the trailing \r
	// must be stripped from each line before fence detection.
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "CRLF backtick fence",
			in:   "[[Before]]\r\n```\r\n[[Hidden]]\r\n```\r\n[[After]]",
			want: []string{"Before", "After"},
		},
		{
			name: "CRLF tilde fence",
			in:   "[[Before]]\r\n~~~go\r\n[[Hidden]]\r\n~~~\r\n[[After]]",
			want: []string{"Before", "After"},
		},
		{
			name: "CRLF long fence",
			in:   "[[Before]]\r\n````\r\n```\r\n[[Hidden]]\r\n```\r\n````\r\n[[After]]",
			want: []string{"Before", "After"},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := collectTargets(Parse(tc.in))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Parse(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseDoesNotPanicOnExoticInputs(t *testing.T) {
	t.Parallel()
	// None of these should panic; behaviour is not asserted, only safety.
	inputs := []string{
		"",
		"\x00",
		"[[",
		"]]",
		"[[]]",
		"[[Foo\x00Bar]]",
		"\r\r\r",
		"\n\n\n",
		"```",
		"~~~",
		"> > > [[",
		strings.Repeat("[[Foo]] ", 100),
	}
	for _, in := range inputs {
		_ = Parse(in)
		_ = Rewrite(in, func(Link) (string, bool) { return "X", true })
		_ = RewriteTitle(in, "Foo", "Bar")
		_ = RewriteMove(in, "A/B", "C/D")
	}
}
