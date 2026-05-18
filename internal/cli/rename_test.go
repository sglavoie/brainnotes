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
