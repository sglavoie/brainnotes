package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBacklinks_TitleInput(t *testing.T) {
	isolateBnEnv(t)
	root := newBacklinkFixture(t)

	stdout, _, err := runBacklinks(t, root, "backlinks", "Target")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := relSlashLines(t, root, stdout)
	want := []string{"Referrer1.md", "Referrer2.md", "Referrer3.md"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestBacklinks_PathInput(t *testing.T) {
	isolateBnEnv(t)
	root := newBacklinkFixture(t)

	stdout, _, err := runBacklinks(t, root, "backlinks", "Target.md")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := relSlashLines(t, root, stdout)
	want := []string{"Referrer1.md", "Referrer2.md", "Referrer3.md"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestBacklinks_MissingTarget(t *testing.T) {
	isolateBnEnv(t)
	root := newBacklinkFixture(t)

	_, _, err := runBacklinks(t, root, "backlinks", "DoesNotExist")
	if err == nil {
		t.Fatalf("expected error for missing target")
	}
	if !strings.Contains(err.Error(), "DoesNotExist") {
		t.Fatalf("error should mention the title; got %v", err)
	}
}

func TestBacklinks_AmbiguousTitle(t *testing.T) {
	isolateBnEnv(t)
	root := newBacklinkFixture(t)

	dup := filepath.Join(root, "Folder", "Target.md")
	if err := os.MkdirAll(filepath.Dir(dup), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(dup, []byte("---\ntitle: Target\n---\nDup.\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, _, err := runBacklinks(t, root, "backlinks", "Target")
	if err == nil {
		t.Fatalf("expected ambiguity error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "ambiguous") {
		t.Fatalf("error should mention ambiguity; got %v", err)
	}
	if !strings.Contains(msg, "Target.md") || !strings.Contains(msg, filepath.ToSlash(filepath.Join("Folder", "Target.md"))) {
		t.Fatalf("error should list both candidates; got %v", err)
	}
}

func TestBacklinks_ZeroResults(t *testing.T) {
	isolateBnEnv(t)
	root := newBacklinkFixture(t)

	stdout, stderr, err := runBacklinks(t, root, "backlinks", "Unrelated")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if stdout != "" {
		t.Fatalf("expected empty stdout, got %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
}

func TestBacklinks_NullSeparator(t *testing.T) {
	isolateBnEnv(t)
	root := newBacklinkFixture(t)

	cmd := NewRootCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--vault", root, "backlinks", "Target", "-0"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	raw := buf.Bytes()
	if !bytes.Contains(raw, []byte{0}) {
		t.Fatalf("expected NUL separator in output, got %q", raw)
	}
	if bytes.Contains(raw, []byte{'\n'}) {
		t.Fatalf("expected no newline with -0, got %q", raw)
	}
}

func TestBacklinks_SelfReferenceExcluded(t *testing.T) {
	isolateBnEnv(t)
	root := t.TempDir()
	markVaultRoot(t, root, "")
	files := map[string]string{
		"Self.md":  "---\ntitle: Self\n---\nI link to [[Self]] for fun.\n",
		"Other.md": "---\ntitle: Other\n---\nAlso [[Self]] here.\n",
	}
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	stdout, _, err := runBacklinks(t, root, "backlinks", "Self")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := relSlashLines(t, root, stdout)
	want := []string{"Other.md"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
