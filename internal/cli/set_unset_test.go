package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestUnsetRemovesProperty(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")

	out, err := runCLI(t, "", "unset", notePath, "tags")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, notePath+": updated") {
		t.Fatalf("expected updated report, got %q", out)
	}
	got := readNote(t, notePath)
	if strings.Contains(got, "tags:") {
		t.Fatalf("tags should be gone, got:\n%s", got)
	}
}

func TestUnsetIdempotent(t *testing.T) {
	root := buildSetFixture(t)
	notePath := filepath.Join(root, "tags.md")

	if _, err := runCLI(t, "", "unset", notePath, "tags"); err != nil {
		t.Fatalf("first unset: %v", err)
	}
	out, err := runCLI(t, "", "unset", notePath, "tags")
	if err != nil {
		t.Fatalf("second unset: %v\n%s", err, out)
	}
	if out != "" {
		t.Fatalf("second unset should be silent, got %q", out)
	}
}

func TestUnsetMultipleNotesViaDash(t *testing.T) {
	root := buildSetFixture(t)
	aPath := filepath.Join(root, "a.md")
	bPath := filepath.Join(root, "b.md")

	out, err := runCLI(t, "", "unset", aPath, bPath, "--", "title")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, aPath+": updated") || !strings.Contains(out, bPath+": updated") {
		t.Fatalf("expected both updated, got %q", out)
	}
	for _, p := range []string{aPath, bPath} {
		if got := readNote(t, p); strings.Contains(got, "title:") {
			t.Fatalf("%s still has title:\n%s", p, got)
		}
	}
}
