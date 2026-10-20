package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	setCafeComposed   = "Café"
	setCafeDecomposed = "Café"
)

// writeDecomposedTagNote writes a note whose tags list stores the decomposed
// spelling, and returns its path and original bytes.
func writeDecomposedTagNote(t *testing.T) (string, string) {
	t.Helper()
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "unicode.md")
	original := "---\ntags:\n  - " + setCafeDecomposed + "\n  - bar\ntitle: U\n---\nBody.\n"
	if err := os.WriteFile(notePath, []byte(original), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return notePath, original
}

// TestSetRemove_NFCPreviewAndApplyAgree removes a decomposed tag with the
// composed spelling: the dry run must describe the removal the apply performs.
func TestSetRemove_NFCPreviewAndApplyAgree(t *testing.T) {
	notePath, original := writeDecomposedTagNote(t)

	preview, err := runCLI(t, "", "set", "--dry-run", notePath, "tags-="+setCafeComposed)
	if err != nil {
		t.Fatalf("dry-run: %v\n%s", err, preview)
	}
	if !strings.Contains(preview, "frontmatter[tags]") {
		t.Fatalf("dry-run should describe the tags change, got %q", preview)
	}
	if got := readNote(t, notePath); got != original {
		t.Fatalf("dry-run wrote the note:\n%s", got)
	}
	if out, err := runCLI(t, "", "set", notePath, "tags-="+setCafeComposed); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	want := "---\ntags:\n  - bar\ntitle: U\n---\nBody.\n"
	if got := readNote(t, notePath); got != want {
		t.Fatalf("after apply got:\n%s\nwant:\n%s", got, want)
	}
}

// TestSetAdd_NFCEqualTagIsNoOpInPreviewAndApply adds the composed spelling to a
// list holding the decomposed one: neither the dry run nor the apply changes it.
func TestSetAdd_NFCEqualTagIsNoOpInPreviewAndApply(t *testing.T) {
	notePath, original := writeDecomposedTagNote(t)

	preview, err := runCLI(t, "", "set", "--dry-run", notePath, "tags+="+setCafeComposed)
	if err != nil {
		t.Fatalf("dry-run add: %v\n%s", err, preview)
	}
	if preview != "" {
		t.Errorf("adding an NFC-equal tag should preview no change, got %q", preview)
	}
	if out, err := runCLI(t, "", "set", notePath, "tags+="+setCafeComposed); err != nil {
		t.Fatalf("apply add: %v\n%s", err, out)
	}
	if got := readNote(t, notePath); got != original {
		t.Errorf("adding an NFC-equal tag rewrote the note:\n%s", got)
	}
}
