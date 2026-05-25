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

func TestParseInsideYAMLFrontmatter(t *testing.T) {
	t.Parallel()
	in := "---\ncategories:\n  - '[[Books]]'\n  - '[[References]]'\nparent: '[[Index]]'\n---\nbody\n"
	got := collectTargets(Parse(in))
	want := []string{"Books", "References", "Index"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
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

	got := RewriteTitle(in, "Image", "Picture")
	want := "before ![[Picture]] after [[Note]]"
	if got != want {
		t.Fatalf("RewriteTitle\n  got:  %q\n  want: %q", got, want)
	}
}

func TestParseConsecutiveLinks(t *testing.T) {
	t.Parallel()

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

	links := Parse("[[Note|a|b]]")
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(links))
	}
	if links[0].Alias != "a|b" {
		t.Errorf("Alias = %q, want %q", links[0].Alias, "a|b")
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

func TestParseTitleAccessorEdgeCases(t *testing.T) {
	t.Parallel()

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
