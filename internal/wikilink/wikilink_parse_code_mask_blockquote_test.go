package wikilink

import (
	"reflect"
	"testing"
)

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

func TestParseSkipsBlockquotedIndentedCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "single blockquote prefix",
			in:   "[[Before]]\n>     [[Hidden]]\n>     still code\n[[After]]",
			want: []string{"Before", "After"},
		},
		{
			name: "nested blockquote prefix",
			in:   "[[Before]]\n> >     [[Hidden]]\n> >     still code\n[[After]]",
			want: []string{"Before", "After"},
		},
		{
			name: "same blockquote paragraph indentation is not code",
			in:   "> intro\n>     [[Visible]]\n[[After]]",
			want: []string{"Visible", "After"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := collectTargets(Parse(tc.in))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Parse(%q) targets = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
