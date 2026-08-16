package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBacklinks_VerboseBasic(t *testing.T) {
	isolateBnEnv(t)
	root := newBacklinkFixture(t)

	stdout, _, err := runBacklinks(t, root, "backlinks", "Target", "--verbose")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := stripVaultLines(t, root, stdout)
	want := readGoldenLines(t, "backlinks_verbose_basic.golden")
	if !equalStrings(got, want) {
		t.Fatalf("verbose output mismatch:\n got=%v\nwant=%v", got, want)
	}
}

func TestBacklinks_VerboseFrontmatter(t *testing.T) {
	isolateBnEnv(t)
	root := t.TempDir()
	markVaultRoot(t, root, "")
	files := map[string]string{
		"Target.md":    "---\ntitle: Target\n---\nThe target note.\n",
		"Referrer3.md": "---\ntitle: Referrer3\ncategory: \"[[Target]]\"\n---\nBody without links.\n",
	}
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	stdout, _, err := runBacklinks(t, root, "backlinks", "Target", "--verbose")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := stripVaultLines(t, root, stdout)
	want := readGoldenLines(t, "backlinks_verbose_frontmatter.golden")
	if !equalStrings(got, want) {
		t.Fatalf("verbose frontmatter mismatch:\n got=%v\nwant=%v", got, want)
	}
}

func TestBacklinks_VerboseMultiOccurrence(t *testing.T) {
	isolateBnEnv(t)
	root := t.TempDir()
	markVaultRoot(t, root, "")
	files := map[string]string{
		"Target.md":   "---\ntitle: Target\n---\nTarget body.\n",
		"MultiRef.md": "---\ntitle: MultiRef\n---\nFirst [[Target]] here.\nAnd second [[Target]] there.\n",
	}
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	stdout, _, err := runBacklinks(t, root, "backlinks", "Target", "--verbose")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := stripVaultLines(t, root, stdout)
	want := []string{
		"MultiRef.md:1\tFirst [[Target]] here.",
		"MultiRef.md:2\tAnd second [[Target]] there.",
	}
	if !equalStrings(got, want) {
		t.Fatalf("multi-occurrence mismatch:\n got=%v\nwant=%v", got, want)
	}
}

func TestBacklinks_VerboseTruncation(t *testing.T) {
	isolateBnEnv(t)
	root := t.TempDir()
	markVaultRoot(t, root, "")
	prefix := strings.Repeat("x", 10)
	suffix := strings.Repeat("y", 300-len(prefix)-len("[[Target]] "))
	bodyLine := prefix + " [[Target]] " + suffix
	files := map[string]string{
		"Target.md": "---\ntitle: Target\n---\nbody.\n",
		"BigRef.md": "---\ntitle: BigRef\n---\n" + bodyLine + "\n",
	}
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	stdout, _, err := runBacklinks(t, root, "backlinks", "Target", "--verbose")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := stripVaultLines(t, root, stdout)
	if len(got) != 1 {
		t.Fatalf("expected 1 row, got %d: %v", len(got), got)
	}
	parts := strings.SplitN(got[0], "\t", 2)
	if len(parts) != 2 {
		t.Fatalf("expected tab-separated row, got %q", got[0])
	}
	snippet := parts[1]
	runes := []rune(snippet)
	if len(runes) != 120 {
		t.Fatalf("expected snippet truncated to 120 runes, got %d: %q", len(runes), snippet)
	}
	if runes[119] != '…' {
		t.Fatalf("expected `…` as final rune, got %q", string(runes[119]))
	}
}

func TestBacklinks_VerboseCodeSpanIgnored(t *testing.T) {
	isolateBnEnv(t)
	root := newBacklinkFixture(t)

	stdout, _, err := runBacklinks(t, root, "backlinks", "Target", "--verbose")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := stripVaultLines(t, root, stdout)
	for _, line := range got {
		if strings.Contains(line, "Mentions.md") {
			t.Fatalf("Mentions.md (inline-code link) should not appear in verbose output; got %q", line)
		}
	}
}

func TestBacklinks_VerboseOnlyShowsLinksToSelectedDuplicateTarget(t *testing.T) {
	isolateBnEnv(t)
	root := t.TempDir()
	markVaultRoot(t, root, "")
	files := map[string]string{
		"Folder/Target.md": "Folder target.\n",
		"Other/Target.md":  "Other target.\n",
		"Ref.md":           "One [[Folder/Target]].\nTwo [[Other/Target]].\n",
	}
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	stdout, _, err := runBacklinks(t, root, "backlinks", "Folder/Target.md", "--verbose")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := stripVaultLines(t, root, stdout)
	want := []string{"Ref.md:1\tOne [[Folder/Target]]."}
	if !equalStrings(got, want) {
		t.Fatalf("duplicate-target verbose mismatch:\n got=%v\nwant=%v", got, want)
	}
}
