package wikilink

import (
	"reflect"
	"testing"
)

// Obsidian writes `[[Note\|Alias]]` inside a Markdown table so the `|` does
// not end the cell. The backslash belongs to the separator, not the target.
func TestParseTableEscapedPipe(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want []Link
	}{
		{
			name: "target then escaped alias",
			in:   `| [[Note\|Alias]] |`,
			want: []Link{{
				Raw: `[[Note\|Alias]]`, Target: "Note", Alias: "Alias", EscapedPipe: true,
				Start: 2, End: 17,
			}},
		},
		{
			name: "heading then escaped alias",
			in:   `[[Books/Note#Part\|Alias]]`,
			want: []Link{{
				Raw: `[[Books/Note#Part\|Alias]]`, Target: "Books/Note", Folder: "Books",
				Heading: "Part", Alias: "Alias", EscapedPipe: true,
				Start: 0, End: 26,
			}},
		},
		{
			name: "padding around the escaped separator is trimmed",
			in:   `[[ Note \| Alias ]]`,
			want: []Link{{
				Raw: `[[ Note \| Alias ]]`, Target: "Note", Alias: "Alias", EscapedPipe: true,
				Start: 0, End: 19,
			}},
		},
		{
			name: "only one backslash belongs to the separator",
			in:   `[[Note\\|Alias]]`,
			want: []Link{{
				Raw: `[[Note\\|Alias]]`, Target: `Note\`, Alias: "Alias", EscapedPipe: true,
				Start: 0, End: 16,
			}},
		},
		{
			name: "a trailing backslash without an alias is kept",
			in:   `[[Note\]]`,
			want: []Link{{
				Raw: `[[Note\]]`, Target: `Note\`,
				Start: 0, End: 9,
			}},
		},
		{
			name: "a lone escaped separator is not a link",
			in:   `[[\|Alias]]`,
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Parse(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Parse(%q)\n got: %#v\nwant: %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestRewritePreservesTableEscapedPipe(t *testing.T) {
	t.Parallel()
	in := "| a | [[Old\\|shown]] |\n| b | [[Folder/Old#H\\|x]] | [[Old|plain]] |\n"

	gotTitle := RewriteTitle(in, "Old", "New")
	wantTitle := "| a | [[New\\|shown]] |\n| b | [[Folder/New#H\\|x]] | [[New|plain]] |\n"
	if gotTitle != wantTitle {
		t.Fatalf("RewriteTitle\n got: %q\nwant: %q", gotTitle, wantTitle)
	}

	gotMove := RewriteMove(in, "Folder/Old", "Other/Old")
	wantMove := "| a | [[Old\\|shown]] |\n| b | [[Other/Old#H\\|x]] | [[Old|plain]] |\n"
	if gotMove != wantMove {
		t.Fatalf("RewriteMove\n got: %q\nwant: %q", gotMove, wantMove)
	}
}
