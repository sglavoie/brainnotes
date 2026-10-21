package wikilink

import "testing"

// TestRewriteMoveMatchesGraphSpellings covers the spellings the link graph
// resolves to a note by path, which RewriteMove used to miss, and the ones the
// graph refuses, which it must keep leaving alone.
func TestRewriteMoveMatchesGraphSpellings(t *testing.T) {
	t.Parallel()
	const (
		composed   = "Café"
		decomposed = "Café"
	)
	tests := []struct {
		name, in, oldPath, newPath, want string
	}{
		{"leading slash", "[[/A/Foo]]", "A/Foo.md", "B/Foo.md", "[[B/Foo]]"},
		{"doubled slash", "[[A//Foo]]", "A/Foo.md", "B/Foo.md", "[[B/Foo]]"},
		{"trailing md kept", "[[/A/Foo.md]]", "A/Foo.md", "B/Foo.md", "[[B/Foo.md]]"},
		{"heading and alias kept", "[[A//Foo#Part|see]]", "A/Foo.md", "B/Foo.md", "[[B/Foo#Part|see]]"},
		{"case-insensitive", "[[a/foo]]", "A/Foo.md", "B/Foo.md", "[[B/Foo]]"},
		{"decomposed link, composed path", "[[" + decomposed + "/Foo]]", composed + "/Foo.md", "B/Foo.md", "[[B/Foo]]"},
		{"composed link, decomposed path", "[[" + composed + "/Foo]]", decomposed + "/Foo.md", "B/Foo.md", "[[B/Foo]]"},
		{"dot segment refused", "[[./A/Foo]]", "A/Foo.md", "B/Foo.md", "[[./A/Foo]]"},
		{"interior dot segment refused", "[[A/./Foo]]", "A/Foo.md", "B/Foo.md", "[[A/./Foo]]"},
		{"dotdot segment refused", "[[X/../A/Foo]]", "A/Foo.md", "B/Foo.md", "[[X/../A/Foo]]"},
		{"other folder untouched", "[[C/Foo]]", "A/Foo.md", "B/Foo.md", "[[C/Foo]]"},
		{"bare title untouched", "[[Foo]]", "A/Foo.md", "B/Foo.md", "[[Foo]]"},
		{"explicit root path", "[[/Foo]]", "Foo.md", "B/Foo.md", "[[B/Foo]]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := RewriteMove(tc.in, tc.oldPath, tc.newPath); got != tc.want {
				t.Errorf("RewriteMove(%q, %q, %q) = %q, want %q", tc.in, tc.oldPath, tc.newPath, got, tc.want)
			}
		})
	}
}
