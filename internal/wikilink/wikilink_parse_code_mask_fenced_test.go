package wikilink

import (
	"reflect"
	"testing"
)

func TestParseSkipsFencedCodeBlocks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "fenced backticks",
			in:   "Outside [[Yes]]\n```\nInside [[No]]\n```\nAfter [[Also]]",
			want: []string{"Yes", "Also"},
		},
		{
			name: "fenced tildes with info string",
			in:   "[[Open]]\n~~~go\n[[Hidden]]\n~~~\n[[Close]]",
			want: []string{"Open", "Close"},
		},
		{
			name: "longer fence allows shorter inside",
			in:   "````\n```\n[[NestedFence]]\n```\n````\n[[After]]",
			want: []string{"After"},
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

func TestParseUnclosedFenceMasksRest(t *testing.T) {
	t.Parallel()

	in := "[[Before]]\n```\n[[Inside]]\n[[StillInside]]"
	got := collectTargets(Parse(in))
	want := []string{"Before"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestParseLongerFenceAllowsBacktickRunsInside(t *testing.T) {
	t.Parallel()

	in := "````\n```\n[[NotEscaping]]\n```\n````\n[[After]]"
	got := collectTargets(Parse(in))
	want := []string{"After"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestParseFenceContentContainsBlankLine(t *testing.T) {
	t.Parallel()

	in := "```\n[[A]]\n\n[[B]]\n```\n[[After]]"
	got := collectTargets(Parse(in))
	want := []string{"After"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse(%q) = %v, want %v", in, got, want)
	}
}

func TestParseStrayBacktickBeforeFence(t *testing.T) {
	t.Parallel()

	in := "abc `\n```\n[[Hidden]]\n```\nafter [[Visible]]"
	got := collectTargets(Parse(in))
	want := []string{"Visible"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse(%q) = %v, want %v", in, got, want)
	}
}
