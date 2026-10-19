package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRename_FzfPickNote(t *testing.T) {
	root := buildRenameFixture(t)

	orig := notePicker
	t.Cleanup(func() { notePicker = orig })
	notePicker = func(items []string, extraArgs []string) ([]string, error) {
		return []string{filepath.Join(root, "Dune.md")}, nil
	}

	out, err := runCLI(t, "", "rename", "Sand Worms")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}

	if _, err := os.Stat(filepath.Join(root, "Dune.md")); !os.IsNotExist(err) {
		t.Fatalf("expected old path gone, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Sand Worms.md")); err != nil {
		t.Fatalf("expected new path present, stat err=%v", err)
	}

	got := readNote(t, filepath.Join(root, "Reviews", "Dune Review.md"))
	if !strings.Contains(got, "[[Sand Worms]]") {
		t.Errorf("expected wikilinks rewritten in:\n%s", got)
	}
}

func TestRename_FzfAbort(t *testing.T) {
	root := buildRenameFixture(t)
	before := snapshotVault(t, root)

	orig := notePicker
	t.Cleanup(func() { notePicker = orig })
	notePicker = func(items []string, extraArgs []string) ([]string, error) {
		return nil, nil
	}

	_, err := runCLI(t, "", "rename", "Sand Worms")
	if !errors.Is(err, ErrNothingSelected) {
		t.Fatalf("err = %v, want ErrNothingSelected", err)
	}

	after := snapshotVault(t, root)
	if !mapsEqual(before, after) {
		t.Fatalf("abort modified files:\nbefore=%v\nafter=%v", before, after)
	}
}

func TestRename_FzfInvalidTitleBeforePicker(t *testing.T) {
	buildRenameFixture(t)

	orig := notePicker
	t.Cleanup(func() { notePicker = orig })
	notePicker = func(items []string, extraArgs []string) ([]string, error) {
		t.Fatal("picker should not be called for invalid title")
		return nil, nil
	}

	_, err := runCLI(t, "", "rename", "Bad/Title")
	if err == nil {
		t.Fatal("expected error for invalid title")
	}
	if !strings.Contains(err.Error(), "may not contain '/'") {
		t.Fatalf("expected slash rejection, got %v", err)
	}
}
