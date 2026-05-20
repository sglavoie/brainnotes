package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildRenameFixture writes a small vault for rename tests. The vault has:
//   - Dune.md  — target note, with a self-reference in the body
//   - Reviews/Dune Review.md — body refs: bare, aliased, headed, folder-prefixed
//   - categorized.md — categories: ["[[Dune]]"]
//   - parent.md — parent: "[[Dune]]"
//   - other.md — no references
//
// It also writes a config and points BNOTES_CONFIG at it.
func buildRenameFixture(t *testing.T) (vaultRoot string) {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"Dune.md":                "---\ntitle: Dune\n---\nThis is the original Dune note.\nSelf reference: [[Dune]] and [[Dune|alias]].\n",
		"Reviews/Dune Review.md": "---\ntitle: Dune Review\n---\nRead [[Dune]] last week.\nAlso see [[Dune|the book]].\nJump to [[Dune#Sequels]].\nFull path: [[Books/Dune]].\nUnrelated: [[Something Else]].\n",
		"categorized.md":         "---\ncategories:\n  - \"[[Dune]]\"\n  - \"[[Books]]\"\n---\nA categorized note.\n",
		"parent.md":              "---\nparent: \"[[Dune]]\"\n---\nA child note.\n",
		"other.md":               "---\ntitle: Other\n---\nNo references here.\n",
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

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")
	cfg := "vault = \"" + root + "\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("BNOTES_CONFIG", cfgPath)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_VAULT", "")
	return root
}

func TestRenameBodyWikilinks(t *testing.T) {
	root := buildRenameFixture(t)

	out, err := runCLI(t, "", "rename", "Dune", "Sand Worms")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}

	// Target file moved.
	if _, err := os.Stat(filepath.Join(root, "Dune.md")); !os.IsNotExist(err) {
		t.Fatalf("expected old path gone, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Sand Worms.md")); err != nil {
		t.Fatalf("expected new path present, stat err=%v", err)
	}

	got := readNote(t, filepath.Join(root, "Reviews", "Dune Review.md"))
	wantSubstrings := []string{
		"Read [[Sand Worms]] last week.",
		"Also see [[Sand Worms|the book]].",
		"Jump to [[Sand Worms#Sequels]].",
		"Full path: [[Books/Sand Worms]].",
		"[[Something Else]]",
	}
	for _, w := range wantSubstrings {
		if !strings.Contains(got, w) {
			t.Errorf("expected %q in:\n%s", w, got)
		}
	}
	if strings.Contains(got, "[[Dune") {
		t.Errorf("expected no remaining [[Dune... refs:\n%s", got)
	}
}

func TestRenameFrontmatterWikilinks(t *testing.T) {
	root := buildRenameFixture(t)

	if _, err := runCLI(t, "", "rename", "Dune", "Sand Worms"); err != nil {
		t.Fatalf("execute: %v", err)
	}

	categorized := readNote(t, filepath.Join(root, "categorized.md"))
	if !strings.Contains(categorized, "[[Sand Worms]]") {
		t.Errorf("expected categories list rewritten to [[Sand Worms]] in:\n%s", categorized)
	}
	if !strings.Contains(categorized, "[[Books]]") {
		t.Errorf("expected unrelated [[Books]] preserved in:\n%s", categorized)
	}
	if strings.Contains(categorized, "[[Dune]]") {
		t.Errorf("expected no [[Dune]] left in:\n%s", categorized)
	}

	parent := readNote(t, filepath.Join(root, "parent.md"))
	if !strings.Contains(parent, "[[Sand Worms]]") {
		t.Errorf("expected parent rewritten in:\n%s", parent)
	}
}

func TestRenameSelfReferences(t *testing.T) {
	root := buildRenameFixture(t)

	if _, err := runCLI(t, "", "rename", "Dune", "Sand Worms"); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := readNote(t, filepath.Join(root, "Sand Worms.md"))
	if !strings.Contains(got, "Self reference: [[Sand Worms]] and [[Sand Worms|alias]].") {
		t.Errorf("self-reference not rewritten in:\n%s", got)
	}
}

func TestRenameDryRunWritesNothing(t *testing.T) {
	root := buildRenameFixture(t)

	before := snapshotVault(t, root)

	out, err := runCLI(t, "", "rename", "Dune", "Sand Worms", "--dry-run")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}

	after := snapshotVault(t, root)
	if !mapsEqual(before, after) {
		t.Fatalf("dry-run modified files:\nbefore=%v\nafter=%v", before, after)
	}

	if !strings.Contains(out, "rename: Dune.md -> Sand Worms.md") {
		t.Errorf("dry-run output missing rename header:\n%s", out)
	}
	if !strings.Contains(out, "Reviews/Dune Review.md:") {
		t.Errorf("dry-run output missing reviewer file change:\n%s", out)
	}
	if !strings.Contains(out, "frontmatter[categories]") {
		t.Errorf("dry-run output missing frontmatter change:\n%s", out)
	}
}

// TestRenameDryRunMasksCodeBlockReferences asserts that wikilinks inside a
// fenced code block do not appear in the rename dry-run output, while bare,
// aliased, and heading references in prose do.
func TestRenameDryRunMasksCodeBlockReferences(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"Dune.md":      "---\ntitle: Dune\n---\nOriginal body.\n",
		"bare.md":      "---\ntitle: Bare\n---\nPlain prose link: [[Dune]] in a sentence.\n",
		"alias.md":     "---\ntitle: Alias\n---\nWith alias: [[Dune|the book]].\n",
		"heading.md":   "---\ntitle: Heading\n---\nAt heading: [[Dune#Sequels]].\n",
		"codeblock.md": "---\ntitle: Code\n---\nDocumentation example:\n\n```\nUse [[Dune]] like this.\n```\n\nDone.\n",
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
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("vault = \""+root+"\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("BNOTES_CONFIG", cfgPath)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BNOTES_VAULT", "")

	beforeMtimes := snapshotMtimes(t, root)

	out, err := runCLI(t, "", "rename", "Dune", "Sand Worms", "--dry-run")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}

	for _, want := range []string{"bare.md:", "alias.md:", "heading.md:"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in dry-run output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "codeblock.md:") {
		t.Errorf("expected codeblock.md to be absent from dry-run output (link is fenced):\n%s", out)
	}

	afterMtimes := snapshotMtimes(t, root)
	if !mtimesEqual(beforeMtimes, afterMtimes) {
		t.Fatalf("dry-run modified file mtimes:\nbefore=%v\nafter=%v", beforeMtimes, afterMtimes)
	}
}

func TestRenameAmbiguousTitle(t *testing.T) {
	root := buildRenameFixture(t)
	// Plant a second note with the same stem.
	dup := filepath.Join(root, "Reviews", "Dune.md")
	if err := os.WriteFile(dup, []byte("---\ntitle: Dune Dup\n---\nDup body.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "", "rename", "Dune", "Sand Worms")
	if err == nil {
		t.Fatalf("expected ambiguous error, got output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous error, got %v", err)
	}
}

func TestRenameDestinationExistsRefused(t *testing.T) {
	root := buildRenameFixture(t)
	conflict := filepath.Join(root, "Sand Worms.md")
	if err := os.WriteFile(conflict, []byte("body\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runCLI(t, "", "rename", "Dune", "Sand Worms")
	if err == nil {
		t.Fatal("expected destination-exists error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected already-exists error, got %v", err)
	}

	// Still present, untouched.
	if _, err := os.Stat(filepath.Join(root, "Dune.md")); err != nil {
		t.Fatalf("original should still exist: %v", err)
	}
}

func TestRenameDestinationExistsForce(t *testing.T) {
	root := buildRenameFixture(t)
	conflict := filepath.Join(root, "Sand Worms.md")
	if err := os.WriteFile(conflict, []byte("stomped\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "", "rename", "Dune", "Sand Worms", "--force")
	if err != nil {
		t.Fatalf("execute --force: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root, "Dune.md")); !os.IsNotExist(err) {
		t.Fatalf("expected old path gone, got %v", err)
	}
	got := readNote(t, filepath.Join(root, "Sand Worms.md"))
	if strings.Contains(got, "stomped") {
		t.Fatalf("force should have overwritten conflict, got:\n%s", got)
	}
}

func TestRenameCaseOnly(t *testing.T) {
	root := buildRenameFixture(t)

	out, err := runCLI(t, "", "rename", "Dune", "dune")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}

	// On any FS the lowercase file should now hold the original content
	// (with self-reference rewritten).
	got := readNote(t, filepath.Join(root, "dune.md"))
	if !strings.Contains(got, "[[dune]]") {
		t.Errorf("expected lowercase self-reference, got:\n%s", got)
	}
}

func TestRenameByExplicitPath(t *testing.T) {
	root := buildRenameFixture(t)

	target := filepath.Join(root, "Dune.md")
	if _, err := runCLI(t, "", "rename", target, "Sand Worms"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Sand Worms.md")); err != nil {
		t.Fatalf("expected renamed file, got %v", err)
	}
}

func TestRenameRejectsSlashInTitle(t *testing.T) {
	buildRenameFixture(t)
	_, err := runCLI(t, "", "rename", "Dune", "Books/Dune")
	if err == nil {
		t.Fatal("expected slash-in-title error")
	}
	if !strings.Contains(err.Error(), "may not contain '/'") {
		t.Fatalf("expected slash rejection, got %v", err)
	}
}

func TestRenameRejectsEmptyTitle(t *testing.T) {
	buildRenameFixture(t)
	_, err := runCLI(t, "", "rename", "Dune", "")
	if err == nil {
		t.Fatal("expected empty-title error")
	}
}

func TestRenameRejectsIdenticalTitle(t *testing.T) {
	buildRenameFixture(t)
	_, err := runCLI(t, "", "rename", "Dune", "Dune")
	if err == nil {
		t.Fatal("expected identical-title error")
	}
}

// TestRenameRejectsWhitespaceTitle locks in that titles consisting only of
// whitespace are rejected before any file is created. Before the fix, a title
// like "   " produced "   .md" on disk.
func TestRenameRejectsWhitespaceTitle(t *testing.T) {
	buildRenameFixture(t)
	cases := []string{"   ", "\t", "\n", " \t "}
	for _, title := range cases {
		_, err := runCLI(t, "", "rename", "Dune", title)
		if err == nil {
			t.Fatalf("expected rejection for whitespace-only title %q", title)
		}
		if !strings.Contains(err.Error(), "whitespace") && !strings.Contains(err.Error(), "control character") {
			t.Fatalf("expected whitespace/control-char rejection for %q, got %v", title, err)
		}
	}
}

// TestRenameRejectsLeadingTrailingWhitespace locks in that titles with
// leading/trailing whitespace are rejected. Before the fix, "  Foo" became
// "  Foo.md" on disk.
func TestRenameRejectsLeadingTrailingWhitespace(t *testing.T) {
	buildRenameFixture(t)
	cases := []string{"  Foo", "Foo  ", " Foo ", "\tFoo"}
	for _, title := range cases {
		_, err := runCLI(t, "", "rename", "Dune", title)
		if err == nil {
			t.Fatalf("expected rejection for padded title %q", title)
		}
	}
}

// TestRenameRejectsControlCharsInTitle locks in that titles containing ASCII
// control characters (newline, NUL, DEL, etc.) are rejected. Before the fix,
// embedded newlines produced files with literal newlines in their names.
func TestRenameRejectsControlCharsInTitle(t *testing.T) {
	buildRenameFixture(t)
	cases := []string{"Foo\nBar", "Foo\rBar", "Foo\x00Bar", "Foo\x7fBar", "A\tB"}
	for _, title := range cases {
		_, err := runCLI(t, "", "rename", "Dune", title)
		if err == nil {
			t.Fatalf("expected rejection for control-char title %q", title)
		}
	}
}

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

	out, err := runCLI(t, "", "rename", "Sand Worms")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
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

// snapshotVault returns a map of relative-path -> content for every regular
// file under root. Used to assert dry-run leaves the tree untouched.
func snapshotVault(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return out
}

// snapshotMtimes maps every regular file under root to its mod-time. Used to
// assert that --dry-run does not so much as touch any file (a stronger check
// than content equality when paired with mapsEqual).
func snapshotMtimes(t *testing.T, root string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[rel] = info.ModTime().UnixNano()
		return nil
	})
	if err != nil {
		t.Fatalf("mtime snapshot: %v", err)
	}
	return out
}

func mtimesEqual(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
