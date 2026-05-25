package wikilink

import (
	"reflect"
	"testing"
)

func TestParseSkipsIndentedCodeBlocks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "indented code block after blank",
			in:   "paragraph one\n\n    [[Hidden]]\n    more code\n\n[[Visible]]",
			want: []string{"Visible"},
		},
		{
			name: "indented continuation not code",
			in:   "lead in\n    [[Visible]]\n",
			want: []string{"Visible"},
		},
		{
			name: "tab indented code at doc start",
			in:   "\t[[Hidden]]\n\n[[Visible]]",
			want: []string{"Visible"},
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
