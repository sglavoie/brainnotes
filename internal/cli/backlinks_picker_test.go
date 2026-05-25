package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBacklinks_FzfPickNote(t *testing.T) {
	isolateBnEnv(t)
	root := newBacklinkFixture(t)

	orig := notePicker
	t.Cleanup(func() { notePicker = orig })
	notePicker = func(items []string, extraArgs []string) ([]string, error) {
		return []string{filepath.Join(root, "Target.md")}, nil
	}

	stdout, _, err := runBacklinks(t, root, "backlinks")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := relSlashLines(t, root, stdout)
	want := []string{"Referrer1.md", "Referrer2.md", "Referrer3.md"}
	if !equalStrings(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestBacklinks_FzfAbort(t *testing.T) {
	isolateBnEnv(t)
	root := newBacklinkFixture(t)

	orig := notePicker
	t.Cleanup(func() { notePicker = orig })
	notePicker = func(items []string, extraArgs []string) ([]string, error) {
		return nil, nil
	}

	stdout, stderr, err := runBacklinks(t, root, "backlinks")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if stdout != "" {
		t.Fatalf("expected empty stdout on abort, got %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr on abort, got %q", stderr)
	}
}

func TestBacklinks_FzfEmptyVault(t *testing.T) {
	isolateBnEnv(t)
	root := t.TempDir()

	orig := notePicker
	t.Cleanup(func() { notePicker = orig })
	pickerCalled := false
	notePicker = func(items []string, extraArgs []string) ([]string, error) {
		pickerCalled = true
		return nil, nil
	}

	stdout, stderr, err := runBacklinks(t, root, "backlinks")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if pickerCalled {
		t.Fatalf("picker should not be called on empty vault")
	}
	if stdout != "" {
		t.Fatalf("expected empty stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "no notes in vault") {
		t.Fatalf("expected stderr to mention 'no notes in vault'; got %q", stderr)
	}
}
