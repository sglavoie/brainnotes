package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewRefusesOverwrite(t *testing.T) {
	root := buildNewFixture(t)
	target := filepath.Join(root, "References", "Books", "Dune.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runNew(t, "new", "book", "Dune")
	if err == nil {
		t.Fatal("expected error on existing file without --force")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected already-exists error, got %v", err)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "existing" {
		t.Fatalf("file was overwritten: %q", data)
	}
}

func TestNewForceOverwrite(t *testing.T) {
	root := buildNewFixture(t)
	target := filepath.Join(root, "References", "Books", "Dune.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := runNew(t, "new", "book", "Dune", "--var", "author=H", "--force"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, _ := os.ReadFile(target)
	if !strings.Contains(string(data), "title: Dune") {
		t.Fatalf("expected rendered content, got %q", data)
	}
}

func TestNewEdit_EditorArgsSupported(t *testing.T) {
	root := buildNewFixture(t)
	t.Setenv("EDITOR", "go env")
	captured := stubExecReplace(t)

	out, err := runNew(t, "new", "book", "Dune", "--var", "author=Herbert", "--edit")
	if err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, out)
	}

	created := filepath.Join(root, "References", "Books", "Dune.md")
	if _, err := os.Stat(created); err != nil {
		t.Fatalf("expected created note at %s: %v", created, err)
	}
	if got, want := *captured, []string{"go", "env", created}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("execReplace argv = %v, want %v", got, want)
	}
}

func TestNewCreatesNoteWithReadableMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits not meaningful on Windows")
	}
	root := buildNewFixture(t)

	if _, err := runNew(t, "new", "book", "Dune", "--var", "author=Herbert"); err != nil {
		t.Fatalf("execute: %v", err)
	}

	created := filepath.Join(root, "References", "Books", "Dune.md")
	info, err := os.Stat(created)
	if err != nil {
		t.Fatalf("stat created note: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("created note mode = %o, want 0644", got)
	}
}

func TestNewForcePreservesExistingMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits not meaningful on Windows")
	}
	root := buildNewFixture(t)
	target := filepath.Join(root, "References", "Books", "Dune.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("existing"), 0o640); err != nil {
		t.Fatal(err)
	}

	if _, err := runNew(t, "new", "book", "Dune", "--var", "author=H", "--force"); err != nil {
		t.Fatalf("execute: %v", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat overwritten note: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("overwritten note mode = %o, want 0640", got)
	}
}

func TestNewRejectsFolderOutsideVault(t *testing.T) {
	root := buildNewFixture(t)
	_, err := runNew(t, "new", "book", "Pwn", "--folder", "../escape", "--var", "author=x")
	if err == nil {
		t.Fatal("expected error for --folder ../escape, got nil")
	}
	if !strings.Contains(err.Error(), "outside the vault") {
		t.Fatalf("expected 'outside the vault' error, got: %v", err)
	}
	parent := filepath.Dir(root)
	if _, statErr := os.Stat(filepath.Join(parent, "escape", "Pwn.md")); statErr == nil {
		t.Fatalf("file leaked outside vault at %s", filepath.Join(parent, "escape", "Pwn.md"))
	}
}
