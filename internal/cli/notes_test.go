package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadStdinPaths_NullSeparatedPreservesSpaces verifies that NUL-separated
// stdin input is not whitespace-trimmed: paths containing leading/trailing
// spaces must round-trip exactly so users can pipe arbitrary in-vault paths
// without losing parts. Before the fix, TrimSpace was applied unconditionally
// and a trailing-space path was silently mangled.
func TestReadStdinPaths_NullSeparatedPreservesSpaces(t *testing.T) {
	root := t.TempDir()
	// Filenames containing spaces inside the name; we don't include a
	// trailing space (some filesystems reject those) but we do prepend an
	// internal space to assert TrimSpace is not nuking byte content.
	files := []string{"alpha.md", "bra vo.md"}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(root, f), []byte("body"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	in := strings.NewReader(filepath.Join(root, "alpha.md") + "\x00" + filepath.Join(root, "bra vo.md") + "\x00")
	got, err := readStdinPaths(in, root, true)
	if err != nil {
		t.Fatalf("readStdinPaths: %v", err)
	}
	want := []string{
		filepath.Join(root, "alpha.md"),
		filepath.Join(root, "bra vo.md"),
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestReadStdinPaths_RelativePathResolvesAgainstVault verifies that relative
// paths read from stdin are resolved against the vault root, not the current
// working directory. This matches the behavior of positional-arg paths, which
// go through expandGlob and are joined to vaultRoot.
func TestReadStdinPaths_RelativePathResolvesAgainstVault(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}

	in := strings.NewReader("note.md\n")
	got, err := readStdinPaths(in, root, false)
	if err != nil {
		t.Fatalf("readStdinPaths: %v", err)
	}
	want := []string{filepath.Join(root, "note.md")}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestReadStdinPaths_RelativePathNullSep verifies that NUL-separated relative
// paths from stdin are also resolved against the vault root.
func TestReadStdinPaths_RelativePathNullSep(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "deep.md"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}

	in := strings.NewReader("sub/deep.md\x00")
	got, err := readStdinPaths(in, root, true)
	if err != nil {
		t.Fatalf("readStdinPaths: %v", err)
	}
	want := []string{filepath.Join(root, "sub", "deep.md")}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestReadStdinPaths_LineSeparatedTrimsCRLF verifies that newline-separated
// stdin input still strips trailing \r so a Windows-piped path resolves.
func TestReadStdinPaths_LineSeparatedTrimsCRLF(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "alpha.md"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := strings.NewReader(filepath.Join(root, "alpha.md") + "\r\n")
	got, err := readStdinPaths(in, root, false)
	if err != nil {
		t.Fatalf("readStdinPaths: %v", err)
	}
	if len(got) != 1 || got[0] != filepath.Join(root, "alpha.md") {
		t.Fatalf("got %v, want one alpha.md", got)
	}
}
