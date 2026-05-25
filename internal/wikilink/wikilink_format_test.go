package wikilink

import "testing"

func TestFormatLink(t *testing.T) {
	t.Parallel()
	tests := []struct {
		target, heading, alias, want string
	}{
		{"A", "", "", "[[A]]"},
		{"A", "H", "", "[[A#H]]"},
		{"A", "", "B", "[[A|B]]"},
		{"A", "H", "B", "[[A#H|B]]"},
		{"F/A", "H", "B", "[[F/A#H|B]]"},
	}
	for _, tc := range tests {
		got := FormatLink(tc.target, tc.heading, tc.alias)
		if got != tc.want {
			t.Errorf("FormatLink(%q,%q,%q) = %q, want %q", tc.target, tc.heading, tc.alias, got, tc.want)
		}
	}
}
