package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMoveDestinationExistsRefused(t *testing.T) {
	root := buildMoveFixture(t)
	conflict := filepath.Join(root, "References", "Books", "Dune.md")
	if err := os.MkdirAll(filepath.Dir(conflict), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conflict, []byte("conflict\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(root, "Books", "Dune.md")
	_, err := runCLI(t, "", "move", src, "References/Books")
	if err == nil {
		t.Fatal("expected destination-exists error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected already-exists error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Books", "Dune.md")); err != nil {
		t.Fatalf("source should still exist: %v", err)
	}
}

func TestMoveDestinationExistsForce(t *testing.T) {
	root := buildMoveFixture(t)
	conflict := filepath.Join(root, "References", "Books", "Dune.md")
	if err := os.MkdirAll(filepath.Dir(conflict), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conflict, []byte("stomped\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(root, "Books", "Dune.md")
	out, err := runCLI(t, "", "move", src, "References/Books", "--force")
	if err != nil {
		t.Fatalf("execute --force: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root, "Books", "Dune.md")); !os.IsNotExist(err) {
		t.Fatalf("expected source path gone, got %v", err)
	}
	got := readNote(t, filepath.Join(root, "References", "Books", "Dune.md"))
	if strings.Contains(got, "stomped") {
		t.Fatalf("force should have overwritten conflict, got:\n%s", got)
	}
}

func TestMoveByBareTitle(t *testing.T) {
	root := buildMoveFixture(t)

	if _, err := runCLI(t, "", "move", "Dune", "References/Books"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "References", "Books", "Dune.md")); err != nil {
		t.Fatalf("expected moved file, got %v", err)
	}
}

func TestMoveByRelativePathOutsideVaultCWD(t *testing.T) {
	root := buildMoveFixture(t)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	outside := t.TempDir()
	if err := os.Chdir(outside); err != nil {
		t.Fatalf("chdir(%q): %v", outside, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	if _, err := runCLI(t, "", "move", "Books/Dune.md", "References/Books"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "References", "Books", "Dune.md")); err != nil {
		t.Fatalf("expected moved file, got %v", err)
	}
}

func TestMove_FzfPickNote(t *testing.T) {
	root := buildMoveFixture(t)

	orig := notePicker
	t.Cleanup(func() { notePicker = orig })
	notePicker = func(items []string, extraArgs []string) ([]string, error) {
		return []string{filepath.Join(root, "Books", "Dune.md")}, nil
	}

	out, err := runCLI(t, "", "move", "References/Books")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}

	if _, err := os.Stat(filepath.Join(root, "Books", "Dune.md")); !os.IsNotExist(err) {
		t.Fatalf("expected old path gone, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "References", "Books", "Dune.md")); err != nil {
		t.Fatalf("expected new path present, stat err=%v", err)
	}
}

func TestMove_FzfAbort(t *testing.T) {
	root := buildMoveFixture(t)
	before := snapshotVault(t, root)

	orig := notePicker
	t.Cleanup(func() { notePicker = orig })
	notePicker = func(items []string, extraArgs []string) ([]string, error) {
		return nil, nil
	}

	_, err := runCLI(t, "", "move", "References/Books")
	if !errors.Is(err, ErrNothingSelected) {
		t.Fatalf("err = %v, want ErrNothingSelected", err)
	}

	after := snapshotVault(t, root)
	if !mapsEqual(before, after) {
		t.Fatalf("abort modified files:\nbefore=%v\nafter=%v", before, after)
	}
}

func TestMoveToVaultRoot(t *testing.T) {
	root := buildMoveFixture(t)

	src := filepath.Join(root, "Books", "Dune.md")
	if _, err := runCLI(t, "", "move", src, "."); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Dune.md")); err != nil {
		t.Fatalf("expected note at vault root, got %v", err)
	}

	review := readNote(t, filepath.Join(root, "Reviews", "Dune Review.md"))
	if !strings.Contains(review, "Read [[Dune]] last week.") {
		t.Errorf("expected bare rewrite of folder-prefixed body ref, got:\n%s", review)
	}
	if strings.Contains(review, "[[Books/Dune") {
		t.Errorf("expected no [[Books/Dune...]] left, got:\n%s", review)
	}
}

func TestMoveRejectsDirectoryDestination(t *testing.T) {
	root := buildMoveFixture(t)
	destDir := filepath.Join(root, "References", "Books", "Dune.md")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	before := snapshotVault(t, root)

	src := filepath.Join(root, "Books", "Dune.md")
	_, err := runCLI(t, "", "move", src, "References/Books")
	if err == nil {
		t.Fatal("expected directory-destination error without --force")
	}
	if !strings.Contains(err.Error(), "existing directory") {
		t.Fatalf("expected 'existing directory' error, got %v", err)
	}
	if after := snapshotVault(t, root); !mapsEqual(before, after) {
		t.Fatalf("move without --force rewrote files:\nbefore=%v\nafter=%v", before, after)
	}

	_, err = runCLI(t, "", "move", src, "References/Books", "--force")
	if err == nil {
		t.Fatal("expected directory-destination error with --force")
	}
	if !strings.Contains(err.Error(), "existing directory") {
		t.Fatalf("expected 'existing directory' error with --force, got %v", err)
	}
	if after := snapshotVault(t, root); !mapsEqual(before, after) {
		t.Fatalf("move with --force rewrote files:\nbefore=%v\nafter=%v", before, after)
	}

	if _, err := os.Stat(filepath.Join(root, "Books", "Dune.md")); err != nil {
		t.Fatalf("original should still exist: %v", err)
	}
}
