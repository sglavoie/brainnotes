package wikilink

import "testing"

func TestFenceScanner(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  []bool
	}{
		{
			name:  "plain_text",
			lines: []string{"text", "## heading"},
			want:  []bool{false, false},
		},
		{
			name:  "backtick_block",
			lines: []string{"```go", "code", "```", "after"},
			want:  []bool{true, true, true, false},
		},
		{
			name:  "tilde_does_not_close_backtick",
			lines: []string{"```", "~~~", "still", "```", "after"},
			want:  []bool{true, true, true, true, false},
		},
		{
			name:  "shorter_run_does_not_close",
			lines: []string{"~~~~", "~~~", "still", "~~~~~", "after"},
			want:  []bool{true, true, true, true, false},
		},
		{
			name:  "closer_with_info_string_does_not_close",
			lines: []string{"```", "``` js", "still", "```", "after"},
			want:  []bool{true, true, true, true, false},
		},
		{
			name:  "crlf_lines",
			lines: []string{"```\r", "code\r", "```\r", "after\r"},
			want:  []bool{true, true, true, false},
		},
		{
			name:  "four_space_indent_is_not_a_fence",
			lines: []string{"    ```", "after"},
			want:  []bool{false, false},
		},
		{
			name:  "backtick_in_info_string_is_not_an_opener",
			lines: []string{"``` a`b", "after"},
			want:  []bool{false, false},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f FenceScanner
			for i, line := range tt.lines {
				if got := f.Line(line); got != tt.want[i] {
					t.Fatalf("line %d %q: Line = %v, want %v", i, line, got, tt.want[i])
				}
			}
		})
	}
}
