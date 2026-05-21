package cli

import (
	"os"
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
		{
			name:      "single arg no dash, treated as prop",
			args:      []string{"status"},
			dashAt:    -1,
			wantNotes: nil,
			wantProps: []string{"status"},
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

// TestUnset_SingleArgNoDashTreatsAsProp verifies that 'bn unset status' (single
// arg, no '--') treats the arg as a property name and triggers the fzf picker
// for note selection, instead of treating it as a note path.
func TestUnset_SingleArgNoDashTreatsAsProp(t *testing.T) {
	root := buildSetFixture(t)
	aPath := filepath.Join(root, "a.md")

	if _, err := runCLI(t, "", "set", aPath, "status=reading"); err != nil {
		t.Fatalf("setup set: %v", err)
	}

	orig := notePicker
	t.Cleanup(func() { notePicker = orig })
	notePicker = func(items []string, extraArgs []string) ([]string, error) {
		return []string{aPath}, nil
	}

	out, err := runCLI(t, "", "unset", "status")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	got := readNote(t, aPath)
	if strings.Contains(got, "status:") {
		t.Fatalf("expected status removed, got:\n%s", got)
	}
}

func TestUnset_FzfPickNotes(t *testing.T) {
	root := buildSetFixture(t)

	aPath := filepath.Join(root, "a.md")
	bPath := filepath.Join(root, "b.md")

	if _, err := runCLI(t, "", "set", aPath, "status=reading"); err != nil {
		t.Fatalf("setup set a: %v", err)
	}
	if _, err := runCLI(t, "", "set", bPath, "status=reading"); err != nil {
		t.Fatalf("setup set b: %v", err)
	}

	orig := notePicker
	t.Cleanup(func() { notePicker = orig })
	notePicker = func(items []string, extraArgs []string) ([]string, error) {
		return []string{aPath, bPath}, nil
	}

	out, err := runCLI(t, "", "unset", "--", "status")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	for _, p := range []string{aPath, bPath} {
		got := readNote(t, p)
		if strings.Contains(got, "status:") {
			t.Fatalf("%s should not have status, got:\n%s", p, got)
		}
	}
}

func TestUnset_FzfAbort(t *testing.T) {
	root := buildSetFixture(t)

	tagsPath := filepath.Join(root, "tags.md")
	beforeBytes, err := os.ReadFile(tagsPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	orig := notePicker
	t.Cleanup(func() { notePicker = orig })
	notePicker = func(items []string, extraArgs []string) ([]string, error) {
		return nil, nil
	}

	out, err := runCLI(t, "", "unset", "--", "tags")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}

	afterBytes, err := os.ReadFile(tagsPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(afterBytes) != string(beforeBytes) {
		t.Fatalf("abort should not modify files")
	}
}
