package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newLinksFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"Hub.md":       "---\ntitle: Hub\ncategory: \"[[Alpha]]\"\n---\nLinks to [[Alpha]], [[Beta]], and [[Gamma]].\n",
		"Alpha.md":     "---\ntitle: Alpha\n---\nAlpha body.\n",
		"Beta.md":      "---\ntitle: Beta\n---\nBeta body.\n",
		"Gamma.md":     "---\ntitle: Gamma\n---\nGamma body.\n",
		"Orphan.md":    "---\ntitle: Orphan\n---\nNo links here.\n",
		"SelfLink.md":  "---\ntitle: SelfLink\n---\n[[SelfLink]] and [[Alpha]].\n",
		"BrokenRef.md": "---\ntitle: BrokenRef\n---\n[[NonExistent]] and [[Alpha]].\n",
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

func runLinks(t *testing.T, vaultRoot string, args ...string) (string, string, error) {
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

func TestLinks_TitleInput(t *testing.T) {
	isolateBnEnv(t)
	root := newLinksFixture(t)

	stdout, _, err := runLinks(t, root, "links", "Hub")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := relSlashLines(t, root, stdout)
	want := []string{"Alpha.md", "Beta.md", "Gamma.md"}
	if !equalStrings(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestLinks_PathInput(t *testing.T) {
	isolateBnEnv(t)
	root := newLinksFixture(t)

	stdout, _, err := runLinks(t, root, "links", "Hub.md")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := relSlashLines(t, root, stdout)
	want := []string{"Alpha.md", "Beta.md", "Gamma.md"}
	if !equalStrings(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestLinks_MissingTarget(t *testing.T) {
	isolateBnEnv(t)
	root := newLinksFixture(t)

	_, _, err := runLinks(t, root, "links", "DoesNotExist")
	if err == nil {
		t.Fatalf("expected error for missing target")
	}
	if !strings.Contains(err.Error(), "DoesNotExist") {
		t.Fatalf("error should mention the title; got %v", err)
	}
}

func TestLinks_NoForwardLinks(t *testing.T) {
	isolateBnEnv(t)
	root := newLinksFixture(t)

	stdout, stderr, err := runLinks(t, root, "links", "Orphan")
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

func TestLinks_NullSeparator(t *testing.T) {
	isolateBnEnv(t)
	root := newLinksFixture(t)

	cmd := NewRootCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--vault", root, "links", "Hub", "-0"})
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

func TestLinks_VerboseBasic(t *testing.T) {
	isolateBnEnv(t)
	root := newLinksFixture(t)

	stdout, _, err := runLinks(t, root, "links", "Hub", "--verbose")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := stripVaultLines(t, root, stdout)
	want := readGoldenLines(t, "links_verbose_basic.golden")
	if !equalStrings(got, want) {
		t.Fatalf("verbose output mismatch:\n got=%v\nwant=%v", got, want)
	}
}

func TestLinks_VerboseFrontmatter(t *testing.T) {
	isolateBnEnv(t)
	root := t.TempDir()
	files := map[string]string{
		"Source.md": "---\ntitle: Source\ncategory: \"[[Target]]\"\n---\nBody without links.\n",
		"Target.md": "---\ntitle: Target\n---\nThe target note.\n",
	}
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	stdout, _, err := runLinks(t, root, "links", "Source", "--verbose")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := stripVaultLines(t, root, stdout)
	want := readGoldenLines(t, "links_verbose_frontmatter.golden")
	if !equalStrings(got, want) {
		t.Fatalf("verbose frontmatter mismatch:\n got=%v\nwant=%v", got, want)
	}
}

func TestUnquotedFrontmatterWikilinksAcrossCLIQueries(t *testing.T) {
	isolateBnEnv(t)
	root := t.TempDir()
	files := map[string]string{
		"Andrew Huberman.md": "---\ncategories:\n  - [[People]]\ntags:\n  - [[Podcasts]]\n---\nNo body links.\n",
		"People.md":          "People.\n",
		"Podcasts.md":        "Podcasts.\n",
	}
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	assertQueryPaths := func(args []string, want []string) {
		t.Helper()
		stdout, _, err := runLinks(t, root, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if got := relSlashLines(t, root, stdout); !equalStrings(got, want) {
			t.Fatalf("%v returned %v, want %v", args, got, want)
		}
	}

	assertQueryPaths([]string{"links", "Andrew Huberman"}, []string{"People.md", "Podcasts.md"})
	assertQueryPaths([]string{"backlinks", "People"}, []string{"Andrew Huberman.md"})
	assertQueryPaths([]string{"backlinks", "Podcasts"}, []string{"Andrew Huberman.md"})
	assertQueryPaths([]string{"find", "--category", "People"}, []string{"Andrew Huberman.md"})
	assertQueryPaths([]string{"find", "--tag", "Podcasts"}, []string{"Andrew Huberman.md"})
}

func TestLinks_VerboseIncludesFolderQualifiedLinks(t *testing.T) {
	isolateBnEnv(t)
	root := t.TempDir()
	files := map[string]string{
		"Source.md":        "See [[Folder/Target|folder alias]].\n",
		"Folder/Target.md": "Target body.\n",
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

	stdout, _, err := runLinks(t, root, "links", "Source", "--verbose")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := stripVaultLines(t, root, stdout)
	want := []string{"Source.md:1\tSee [[Folder/Target|folder alias]]."}
	if !equalStrings(got, want) {
		t.Fatalf("folder-qualified verbose mismatch:\n got=%v\nwant=%v", got, want)
	}
}

func TestLinks_SelfReferenceExcluded(t *testing.T) {
	isolateBnEnv(t)
	root := newLinksFixture(t)

	stdout, _, err := runLinks(t, root, "links", "SelfLink")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := relSlashLines(t, root, stdout)
	want := []string{"Alpha.md"}
	if !equalStrings(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestLinks_BrokenLinksExcluded(t *testing.T) {
	isolateBnEnv(t)
	root := newLinksFixture(t)

	stdout, _, err := runLinks(t, root, "links", "BrokenRef")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := relSlashLines(t, root, stdout)
	want := []string{"Alpha.md"}
	if !equalStrings(got, want) {
		t.Fatalf("got %v, want %v (broken links should be excluded)", got, want)
	}
}

func TestLinks_FzfPick(t *testing.T) {
	isolateBnEnv(t)
	root := newLinksFixture(t)

	orig := notePicker
	t.Cleanup(func() { notePicker = orig })
	notePicker = func(items []string, extraArgs []string) ([]string, error) {
		return []string{filepath.Join(root, "Hub.md")}, nil
	}

	stdout, _, err := runLinks(t, root, "links")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := relSlashLines(t, root, stdout)
	want := []string{"Alpha.md", "Beta.md", "Gamma.md"}
	if !equalStrings(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestLinks_FzfAbort(t *testing.T) {
	isolateBnEnv(t)
	root := newLinksFixture(t)

	orig := notePicker
	t.Cleanup(func() { notePicker = orig })
	notePicker = func(items []string, extraArgs []string) ([]string, error) {
		return nil, nil
	}

	stdout, stderr, err := runLinks(t, root, "links")
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
