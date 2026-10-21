package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

// A *noteops.Error keeps a short public message for web responses; on the
// terminal its cause has to come back, or "frontmatter: load note" names
// neither the note nor the reason.
func TestDescribeErrorRestoresNoteopsCause(t *testing.T) {
	cause := errors.New("open /v/a.md: no such file or directory")
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"bare", noteops.WrapError(noteops.CodeConflict, "frontmatter: load note", cause),
			"frontmatter: load note: open /v/a.md: no such file or directory"},
		{"wrapped", fmt.Errorf("set: %w", noteops.WrapError(noteops.CodeConflict, "frontmatter: load note", cause)),
			"set: frontmatter: load note: open /v/a.md: no such file or directory"},
		{"nested", noteops.WrapError(noteops.CodeInternal, "outer", noteops.WrapError(noteops.CodeConflict, "inner", cause)),
			"outer: inner: open /v/a.md: no such file or directory"},
		{"cause repeats message", noteops.WrapError(noteops.CodeForbidden, "refused: storage", errors.New("refused: storage")),
			"refused: storage"},
		{"cause contains message", noteops.WrapError(noteops.CodeForbidden, "mutation refused", errors.New("vault: mutation refused: File Provider")),
			"vault: mutation refused: File Provider"},
		{"no cause", noteops.Errorf(noteops.CodeInvalidRequest, "frontmatter: nil map"),
			"frontmatter: nil map"},
		{"plain", errors.New("plain"), "plain"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := describeError(tc.err); got != tc.want {
				t.Fatalf("describeError = %q, want %q", got, tc.want)
			}
		})
	}
}

// End to end: a set on a note that cannot be read names the path and the
// reason, not only the operation that failed ("bn: frontmatter: load note").
func TestRenderErrorNamesTheUnreadableNote(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "note.md")
	if err := os.Remove(notePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(notePath, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := runCLI(t, "", "set", "note.md", "status=active")
	if err == nil {
		t.Fatal("set on a directory named note.md: want an error")
	}
	var rendered bytes.Buffer
	RenderError(&rendered, err)
	for _, want := range []string{"frontmatter: load note", notePath, "is a directory"} {
		if !strings.Contains(rendered.String(), want) {
			t.Fatalf("rendered error %q does not mention %q", rendered.String(), want)
		}
	}
}
