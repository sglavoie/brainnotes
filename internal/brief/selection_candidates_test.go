package brief

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
)

func TestRepresentativeExcerptSelectionAndBounds(t *testing.T) {
	t.Parallel()
	longProse := "  " + strings.Repeat("é", DefaultCardExcerptRunes+20) + "  "
	tests := []struct {
		name     string
		document recall.Document
		want     string
	}{
		{
			name: "prose before heading and table",
			document: recall.Document{Title: "Title", Blocks: []recall.MarkdownBlock{
				{Kind: recall.BlockHeading, Text: " Heading "},
				{Kind: recall.BlockTable, Text: " Table "},
				{Kind: recall.BlockProse, Text: " Prose "},
			}},
			want: "Prose",
		},
		{
			name: "heading before table",
			document: recall.Document{Title: "Title", Blocks: []recall.MarkdownBlock{
				{Kind: recall.BlockTable, Text: "Table"},
				{Kind: recall.BlockHeading, Text: "Heading"},
			}},
			want: "Heading",
		},
		{
			name: "table before title",
			document: recall.Document{Title: "Title", Blocks: []recall.MarkdownBlock{
				{Kind: recall.BlockTable, Text: "Table"},
			}},
			want: "Table",
		},
		{name: "title fallback", document: recall.Document{Title: " Title "}, want: "Title"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := RepresentativeExcerpt(test.document); got != test.want {
				t.Fatalf("RepresentativeExcerpt() = %q, want %q", got, test.want)
			}
		})
	}

	got := RepresentativeExcerpt(recall.Document{
		Title:  "fallback",
		Blocks: []recall.MarkdownBlock{{Kind: recall.BlockProse, Text: longProse}},
	})
	want := BoundCardExcerpt(longProse)
	if got != want {
		t.Fatalf("bounded excerpt differs from Daily Brief behavior\ngot:  %q\nwant: %q", got, want)
	}
	if utf8.RuneCountInString(got) != DefaultCardExcerptRunes {
		t.Fatalf("bounded excerpt rune count = %d, want %d", utf8.RuneCountInString(got), DefaultCardExcerptRunes)
	}
}
