package wikilink

import (
	"reflect"
	"testing"
)

func TestParseSkipsInlineCodeSpans(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "inline code",
			in:   "Real [[Yes]] but `[[No]]` not this",
			want: []string{"Yes"},
		},
		{
			name: "inline code double backticks",
			in:   "look ``[[Hidden]]`` and [[Shown]]",
			want: []string{"Shown"},
		},
		{
			name: "unmatched backtick is not a span",
			in:   "stray ` and a real [[Link]]",
			want: []string{"Link"},
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

func TestParseInlineCodeSpanDoesNotCrossHeadingBoundary(t *testing.T) {
	t.Parallel()

	in := "`code\n# [[Visible]]`\n[[AlsoVisible]]"
	got := collectTargets(Parse(in))
	want := []string{"Visible", "AlsoVisible"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestParseInlineCodeSpanDoesNotCrossListItemBoundary(t *testing.T) {
	t.Parallel()

	in := "`code\n- [[Visible]]`\n[[AlsoVisible]]"
	got := collectTargets(Parse(in))
	want := []string{"Visible", "AlsoVisible"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}
