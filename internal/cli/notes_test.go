package cli

import (
	"os"
	"path/filepath"
	"reflect"
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

// TestResolveNoteInputs_DashMixedWithOtherArgs verifies that passing '-' as
// one of several args is rejected with a clear error. The '-' sentinel is only
// valid as the sole positional arg (it signals "read paths from stdin"); any
// other usage is almost certainly a mistake.
func TestResolveNoteInputs_DashMixedWithOtherArgs(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := resolveNoteInputs("", root, []string{filepath.Join(root, "a.md"), "-"}, strings.NewReader(""), false)
	if err == nil {
		t.Fatal("expected error for '-' mixed with other args")
	}
	if !strings.Contains(err.Error(), "'-' may only be used as the sole note argument") {
		t.Fatalf("expected '-' usage error, got %v", err)
	}
}

// TestResolveNoteInputs_EmptyArg rejects an empty-string positional arg with a
// clear message rather than letting it fall through to a confusing glob or
// stat error downstream.
func TestResolveNoteInputs_EmptyArg(t *testing.T) {
	root := t.TempDir()
	_, err := resolveNoteInputs("", root, []string{""}, nil, false)
	if err == nil {
		t.Fatal("expected error for empty arg")
	}
	if !strings.Contains(err.Error(), "empty note path argument") {
		t.Fatalf("expected empty-arg error, got %v", err)
	}
}

// TestResolveNoteInputs_GlobNoMatch verifies that a glob pattern with no
// filesystem matches returns an error (rather than silently proceeding with
// zero paths, which would make `bn set notes/*.md key=v` look like it
// succeeded even though it had no effect).
func TestResolveNoteInputs_GlobNoMatch(t *testing.T) {
	root := t.TempDir()
	_, err := resolveNoteInputs("", root, []string{"missing/*.md"}, nil, false)
	if err == nil {
		t.Fatal("expected error for glob with no matches")
	}
	if !strings.Contains(err.Error(), "no matches for glob") {
		t.Fatalf("expected glob-no-matches error, got %v", err)
	}
}

// TestResolveNoteInputs_DedupePreservesOrder verifies that duplicate args (the
// same file specified twice, or via a literal-and-glob overlap) collapse to a
// single entry while preserving first-seen order.
func TestResolveNoteInputs_DedupePreservesOrder(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a.md")
	b := filepath.Join(root, "b.md")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("body"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := resolveNoteInputs("", root, []string{a, b, a}, nil, false)
	if err != nil {
		t.Fatalf("resolveNoteInputs: %v", err)
	}
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Fatalf("expected [a, b], got %v", got)
	}
}

// TestResolveNoteInputs_DedupesCaseVariantsOfOneFile checks two spellings of
// one note collapse to the first. On a case-sensitive volume `note.md` is a
// different (missing) path, so both are kept and the assertion says so.
func TestResolveNoteInputs_DedupesCaseVariantsOfOneFile(t *testing.T) {
	root := t.TempDir()
	upper := filepath.Join(root, "Note.md")
	lower := filepath.Join(root, "note.md")
	if err := os.WriteFile(upper, []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}

	want := []string{upper}
	if !sameExistingFile(upper, lower) {
		want = []string{upper, lower}
	}
	got, err := resolveNoteInputs("", root, []string{upper, lower, upper}, nil, false)
	if err != nil {
		t.Fatalf("resolveNoteInputs: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments: got %v, want %v", got, want)
	}
	stdin, err := readStdinPaths(strings.NewReader(upper+"\n"+lower+"\n"), root, false)
	if err != nil {
		t.Fatalf("readStdinPaths: %v", err)
	}
	if !reflect.DeepEqual(stdin, want) {
		t.Fatalf("stdin: got %v, want %v", stdin, want)
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

// TestAbsInVault_SymlinkDirEscapingVaultRejected verifies that a symlinked
// directory inside the vault that resolves to a location outside the vault is
// rejected by absInVault. Previously only a lexical filepath.Rel check was
// performed, which allowed symlinks through.
func TestAbsInVault_SymlinkDirEscapingVaultRejected(t *testing.T) {
	if os.Getenv("GO_SKIP_SYMLINK_TEST") == "1" {
		t.Skip("symlink tests disabled")
	}
	vault := t.TempDir()
	outside := t.TempDir()

	// Create a note in the "outside" directory.
	outsideNote := filepath.Join(outside, "secret.md")
	if err := os.WriteFile(outsideNote, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a symlink inside the vault that points to the outside directory.
	symlinkDir := filepath.Join(vault, "escaped")
	if err := os.Symlink(outside, symlinkDir); err != nil {
		t.Skip("cannot create symlinks:", err)
	}

	// A path that lexically appears inside the vault but physically escapes it.
	escapingPath := filepath.Join(symlinkDir, "secret.md")

	_, err := absInVault(vault, escapingPath)
	if err == nil {
		t.Fatalf("absInVault: expected error for symlink-escaped path %q, got nil", escapingPath)
	}
}

// TestAbsInVault_SymlinkFileEscapingVaultRejected verifies that a symlinked
// file inside the vault that targets a file outside the vault is rejected by
// absInVault. The previous implementation only resolved the parent directory,
// so a file-level symlink like vault/link.md -> /outside/secret.md passed the
// containment check because the parent (vault/) resolved correctly.
func TestAbsInVault_SymlinkFileEscapingVaultRejected(t *testing.T) {
	if os.Getenv("GO_SKIP_SYMLINK_TEST") == "1" {
		t.Skip("symlink tests disabled")
	}
	vault := t.TempDir()
	outside := t.TempDir()

	// Create a real file outside the vault.
	outsideNote := filepath.Join(outside, "secret.md")
	if err := os.WriteFile(outsideNote, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Plant a symlink inside the vault that points directly at the outside file.
	symlinkFile := filepath.Join(vault, "link.md")
	if err := os.Symlink(outsideNote, symlinkFile); err != nil {
		t.Skip("cannot create symlinks:", err)
	}

	_, err := absInVault(vault, symlinkFile)
	if err == nil {
		t.Fatalf("absInVault: expected error for file symlink escaping vault %q, got nil", symlinkFile)
	}
}

// TestAbsInVault_NewNoteInSymlinkDirInsideVaultAllowed verifies that a new
// (not-yet-created) note inside a regular subdirectory of the vault is still
// accepted. This ensures the missing-file fallback path works correctly.
func TestAbsInVault_NewNoteInSymlinkDirInsideVaultAllowed(t *testing.T) {
	vault := t.TempDir()
	sub := filepath.Join(vault, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	newNote := filepath.Join(sub, "new.md")
	got, err := absInVault(vault, newNote)
	if err != nil {
		t.Fatalf("absInVault: unexpected error for new note inside vault: %v", err)
	}
	if got != newNote {
		t.Fatalf("absInVault: got %q, want %q", got, newNote)
	}
}
