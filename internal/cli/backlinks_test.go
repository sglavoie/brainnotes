package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// newBacklinkFixture writes a small vault used by the backlinks tests and
// returns its root. Layout:
//   - Target.md           — the queried note.
//   - Referrer1.md        — body has [[Target]].
//   - Referrer2.md        — body has [[Folder/Target|alias]].
//   - Referrer3.md        — frontmatter category: "[[Target]]".
//   - Unrelated.md        — no link to Target.
//   - Mentions.md         — `[[Target]]` inside a code span (must NOT match).
func newBacklinkFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"Target.md":    "---\ntitle: Target\n---\nI am the target.\n",
		"Referrer1.md": "---\ntitle: Referrer1\n---\nSee [[Target]] for more.\n",
		"Referrer2.md": "---\ntitle: Referrer2\n---\nAlso [[Folder/Target|alias]] here.\n",
		"Referrer3.md": "---\ntitle: Referrer3\ncategory: \"[[Target]]\"\n---\nBody.\n",
		"Unrelated.md": "---\ntitle: Unrelated\n---\nNothing to see.\n",
		"Mentions.md":  "---\ntitle: Mentions\n---\nInline `[[Target]]` should be ignored.\n",
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
	return root
}

// runBacklinks executes the root command with the given args and returns
// stdout, stderr, and the execute error.
func runBacklinks(t *testing.T, vaultRoot string, args ...string) (string, string, error) {
	t.Helper()
	root := NewRootCmd()
	out := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(errBuf)
	root.SetArgs(append([]string{"--vault", vaultRoot}, args...))
	err := root.Execute()
	return out.String(), errBuf.String(), err
}

func relSlashLines(t *testing.T, vaultRoot, s string) []string {
	t.Helper()
	var rels []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		rel, err := filepath.Rel(vaultRoot, line)
		if err != nil {
			t.Fatalf("rel %q: %v", line, err)
		}
		rels = append(rels, filepath.ToSlash(rel))
	}
	sort.Strings(rels)
	return rels
}

func TestBacklinks_TitleInput(t *testing.T) {
	isolateBnEnv(t)
	root := newBacklinkFixture(t)

	stdout, _, err := runBacklinks(t, root, "backlinks", "Target")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := relSlashLines(t, root, stdout)
	want := []string{"Referrer1.md", "Referrer2.md", "Referrer3.md"}
	if !equalStrings(got, want) {
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
	if !equalStrings(got, want) {
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

// stripVaultLines splits raw output by newline, drops empty lines, and replaces
// the absolute vault-root prefix with the relative remainder (slash-separated)
// so verbose-mode output can be compared against goldens stored with relative
// paths. Order is preserved — verbose output is sorted by the command itself.
func stripVaultLines(t *testing.T, vaultRoot, s string) []string {
	t.Helper()
	var out []string
	prefix := vaultRoot + string(os.PathSeparator)
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, prefix) {
			line = filepath.ToSlash(line[len(prefix):])
		}
		out = append(out, line)
	}
	return out
}

func readGoldenLines(t *testing.T, name string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	var lines []string
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimRight(l, "\r")
		if l == "" {
			continue
		}
		lines = append(lines, l)
	}
	return lines
}

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
	// Build a 300-char body line that contains [[Target]]. The link sits near
	// the front so tail-truncation lops off the back half with `…`.
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

func TestBacklinks_SelfReferenceExcluded(t *testing.T) {
	isolateBnEnv(t)
	root := t.TempDir()
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
	if !equalStrings(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
