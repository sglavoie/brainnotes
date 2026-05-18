package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// buildFixtureVault writes a small vault under t.TempDir() and returns its root.
// Dates are computed from time.Now so age-based queries stay deterministic at
// the day boundary level.
func buildFixtureVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	now := time.Now()
	old := now.Add(-40 * 24 * time.Hour).Format("2006-01-02")
	recent := now.Add(-2 * 24 * time.Hour).Format("2006-01-02")

	files := map[string]string{
		"References/Apple.md":  "---\ntags:\n  - foo\n  - fruit\ncreated: " + old + "\n---\nApple body links to [[Banana]].\n",
		"References/Banana.md": "---\ntags:\n  - fruit\ncreated: " + recent + "\n---\nBanana body.\n",
		"People/Bob.md":        "---\ntags:\n  - foo\ncreated: " + recent + "\n---\nBob body.\n",
		"Orphan.md":            "---\ntitle: orphan\n---\nUnreferenced.\n",
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

// runFind executes the root command with the given args against vaultRoot and
// returns the produced lines, converted to vault-relative slash paths and
// sorted lexically.
func runFind(t *testing.T, vaultRoot string, args ...string) []string {
	t.Helper()
	// Isolate config lookup: point XDG_CONFIG_HOME at an empty temp dir so we
	// never pick up the user's real ~/.config/bnotes/config.toml.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_CONFIG", "")
	t.Setenv("BNOTES_VAULT", "")

	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs(append([]string{"--vault", vaultRoot}, args...))

	if err := root.Execute(); err != nil {
		t.Fatalf("execute %v: %v\noutput: %s", args, err, out.String())
	}

	var rels []string
	for _, line := range strings.Split(out.String(), "\n") {
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

func readGolden(t *testing.T, name string) []string {
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
	sort.Strings(lines)
	return lines
}

func TestFindByTag(t *testing.T) {
	root := buildFixtureVault(t)
	got := runFind(t, root, "find", "--tag", "foo")
	want := readGolden(t, "find-tag-foo.golden")
	if !equalStrings(got, want) {
		t.Fatalf("find --tag foo:\n got=%v\nwant=%v", got, want)
	}
}

func TestFindByTagAndAge(t *testing.T) {
	root := buildFixtureVault(t)
	got := runFind(t, root, "find", "--tag", "foo", "--created", ">30d")
	want := readGolden(t, "find-tag-foo-age-gt30d.golden")
	if !equalStrings(got, want) {
		t.Fatalf("find --tag foo --created >30d:\n got=%v\nwant=%v", got, want)
	}
}

func TestFindOrphan(t *testing.T) {
	root := buildFixtureVault(t)
	got := runFind(t, root, "find", "--orphan")
	// Only Banana has an inbound link (from Apple's body); everything else is orphan.
	expected := []string{"Orphan.md", "People/Bob.md", "References/Apple.md"}
	sort.Strings(expected)
	if !equalStrings(got, expected) {
		t.Fatalf("find --orphan:\n got=%v\nwant=%v", got, expected)
	}
}

func TestFindNullSeparator(t *testing.T) {
	root := buildFixtureVault(t)

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_CONFIG", "")
	t.Setenv("BNOTES_VAULT", "")

	cmd := NewRootCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--vault", root, "find", "--tag", "foo", "-0"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "\x00") {
		t.Fatalf("expected NUL separator in output, got %q", got)
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("expected no newline separator with -0, got %q", got)
	}
}

func TestFindEmptyResult(t *testing.T) {
	root := buildFixtureVault(t)
	got := runFind(t, root, "find", "--tag", "nonexistent")
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
