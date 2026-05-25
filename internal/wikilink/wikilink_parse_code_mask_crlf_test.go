package wikilink

import (
	"reflect"
	"testing"
)

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

func TestParseRecognizesCRLFFenceCloser(t *testing.T) {
	t.Parallel()

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
