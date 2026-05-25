package wikilink

import (
	"reflect"
	"testing"
)

func TestParseDifferentFenceCharsDontClose(t *testing.T) {
	t.Parallel()

	in := "```\n~~~\n[[Inside]]\n~~~\n```\n[[After]]"
	got := collectTargets(Parse(in))
	want := []string{"After"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestParseFenceInfoStringHandling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "backtick fence with backtick in info string is not a fence",
			in:   "[[Before]]\n```foo`bar\n[[Visible]]\n```\n[[After]]",
			want: []string{"Before", "Visible"},
		},
		{
			name: "backtick fence with backtick in info string full round trip",
			in:   "[[A]]\n```x`y\n[[B]]\ntext\n```\n[[C]]\n```\n[[D]]",
			want: []string{"A", "B", "D"},
		},
		{
			name: "tilde fence with backtick in info string is valid",
			in:   "[[Before]]\n~~~foo`bar\n[[Hidden]]\n~~~\n[[After]]",
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

func TestParseHandlesLongFences(t *testing.T) {
	t.Parallel()

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
