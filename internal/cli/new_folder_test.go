package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewFolderOverride(t *testing.T) {
	root := buildNewFixture(t)

	out, err := runNew(t, "new", "book", "Beloved", "--folder", "References", "--var", "author=Morrison")
	if err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, out)
	}
	created := filepath.Join(root, "References", "Beloved.md")
	if _, err := os.Stat(created); err != nil {
		t.Fatalf("expected %s to exist: %v", created, err)
	}
	if _, err := os.Stat(filepath.Join(root, "References", "Books", "Beloved.md")); err == nil {
		t.Fatal("file leaked into routed folder despite --folder override")
	}
}

func TestNewFolderOverrideNormalizesLikeRoute(t *testing.T) {
	root := buildNewFixture(t)

	out, err := runNew(t, "new", "book", "Beloved", "--folder", ` ./References\Books/ `, "--var", "author=Morrison")
	if err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, out)
	}
	created := filepath.Join(root, "References", "Books", "Beloved.md")
	if _, err := os.Stat(created); err != nil {
		t.Fatalf("expected normalized destination %s to exist: %v", created, err)
	}
	if !strings.Contains(out, created) {
		t.Fatalf("stdout %q does not mention normalized path %q", out, created)
	}
}

func TestNewRejectsWhitespaceFolderOverride(t *testing.T) {
	_ = buildNewFixture(t)

	_, err := runNew(t, "new", "book", "Blank", "--folder", "   ")
	if err == nil {
		t.Fatal("expected error for whitespace-only --folder, got nil")
	}
	if !strings.Contains(err.Error(), "whitespace-only") {
		t.Fatalf("expected whitespace-only error, got %v", err)
	}
}

func TestNewRejectsAbsoluteFolderOverride(t *testing.T) {
	_ = buildNewFixture(t)

	_, err := runNew(t, "new", "book", "Abs", "--folder", "/tmp/books")
	if err == nil {
		t.Fatal("expected error for absolute --folder, got nil")
	}
	if !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("expected absolute path error, got %v", err)
	}
}

func TestNewRejectsWindowsAbsoluteFolderOverride(t *testing.T) {
	_ = buildNewFixture(t)

	_, err := runNew(t, "new", "book", "Abs", "--folder", `C:\Books`)
	if err == nil {
		t.Fatal("expected error for Windows absolute --folder, got nil")
	}
	if !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("expected absolute path error, got %v", err)
	}
}
