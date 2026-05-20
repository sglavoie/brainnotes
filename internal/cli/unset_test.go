package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestUnsetRejectsEmptyPropertyName verifies that an empty property name (a
// stray `""` slot that could happen via shell expansion of an unset variable)
// produces a clear error instead of being silently swallowed by Front.Get.
func TestUnsetRejectsEmptyPropertyName(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")

	_, err := runCLI(t, "", "unset", notePath, "")
	if err == nil {
		t.Fatal("expected error for empty property name, got nil")
	}
	if !strings.Contains(err.Error(), "empty property name") {
		t.Fatalf("expected 'empty property name' error, got %v", err)
	}
}

// TestUnsetSplitArgs locks in the partitioning rules:
//   - Without '--', only the first arg is a note path.
//   - With '--', everything before is notes and everything after is props.
//   - dashAt at position 0 means no notes were given.
func TestUnsetSplitArgs(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		dashAt    int
		wantNotes []string
		wantProps []string
	}{
		{
			name:      "no dash, one note one prop",
			args:      []string{"note.md", "prop"},
			dashAt:    -1,
			wantNotes: []string{"note.md"},
			wantProps: []string{"prop"},
		},
		{
			name:      "no dash, one note many props",
			args:      []string{"note.md", "p1", "p2", "p3"},
			dashAt:    -1,
			wantNotes: []string{"note.md"},
			wantProps: []string{"p1", "p2", "p3"},
		},
		{
			name:      "dash separates notes from props",
			args:      []string{"a.md", "b.md", "p1"},
			dashAt:    2,
			wantNotes: []string{"a.md", "b.md"},
			wantProps: []string{"p1"},
		},
		{
			name:      "dash at start, no notes",
			args:      []string{"p1", "p2"},
			dashAt:    0,
			wantNotes: []string{},
			wantProps: []string{"p1", "p2"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			notes, props := splitUnsetArgs(c.args, c.dashAt)
			if !equalStrings(notes, c.wantNotes) {
				t.Errorf("notes: got %v, want %v", notes, c.wantNotes)
			}
			if !equalStrings(props, c.wantProps) {
				t.Errorf("props: got %v, want %v", props, c.wantProps)
			}
		})
	}
}
