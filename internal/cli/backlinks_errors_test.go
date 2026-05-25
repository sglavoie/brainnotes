package cli

import (
	"strings"
	"testing"
)

// TestBacklinks_OutOfVaultPathError verifies that when the title lookup fails
// and the path fallback also fails (e.g. the path is outside the vault), the
// returned error is the path-resolution error, not the original title-lookup
// error. Before the fix, resolveBacklinkTarget always returned the title-lookup
// error ("no note titled ...") which was misleading.
func TestBacklinks_OutOfVaultPathError(t *testing.T) {
	isolateBnEnv(t)
	root := newBacklinkFixture(t)

	_, _, err := runBacklinks(t, root, "backlinks", "../outside")
	if err == nil {
		t.Fatalf("expected error for out-of-vault path")
	}
	msg := err.Error()
	if strings.Contains(msg, "no note titled") {
		t.Fatalf("error should NOT be the title-lookup error; got %v", err)
	}
	if !strings.Contains(msg, "outside the vault") {
		t.Fatalf("error should mention out-of-vault; got %v", err)
	}
}

// TestBacklinks_NonexistentFileError verifies that when the title lookup fails
// and the path resolves inside the vault but the file does not exist, the
// returned error mentions the missing file rather than "no note titled".
func TestBacklinks_NonexistentFileError(t *testing.T) {
	isolateBnEnv(t)
	root := newBacklinkFixture(t)

	_, _, err := runBacklinks(t, root, "backlinks", "nonexistent.md")
	if err == nil {
		t.Fatalf("expected error for nonexistent file")
	}
	msg := err.Error()
	if strings.Contains(msg, "no note titled") {
		t.Fatalf("error should NOT be the title-lookup error; got %v", err)
	}
	if !strings.Contains(msg, "nonexistent.md") {
		t.Fatalf("error should mention the filename; got %v", err)
	}
}
