package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildMoveFixture writes a small vault for move tests. The vault has:
//   - Books/Dune.md — target note, with body self-references (folder-prefixed
//     and bare) plus an unrelated link.
//   - Reviews/Dune Review.md — body refs: bare, folder-prefixed, aliased+folder,
//     heading+folder, plus an unrelated link.
//   - categorized.md — categories: ["[[Books/Dune]]", "[[Books/Other]]"].
//   - parent.md — parent: "[[Books/Dune]]".
//   - other.md — no references.
//
// It also writes a config and points BNOTES_CONFIG at it.
func buildMoveFixture(t *testing.T) (vaultRoot string) {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"Books/Dune.md":          "---\ntitle: Dune\n---\nOriginal body.\nFolder self: [[Books/Dune]] and [[Books/Dune|alias]].\nBare self: [[Dune]].\n",
		"Reviews/Dune Review.md": "---\ntitle: Dune Review\n---\nRead [[Books/Dune]] last week.\nAlso see [[Books/Dune|the book]].\nJump to [[Books/Dune#Sequels]].\nBare: [[Dune]].\nUnrelated: [[Books/Other]].\n",
		"categorized.md":         "---\ncategories:\n  - \"[[Books/Dune]]\"\n  - \"[[Books/Other]]\"\n---\nA categorized note.\n",
		"parent.md":              "---\nparent: \"[[Books/Dune]]\"\n---\nA child note.\n",
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

func TestMoveFolderPrefixedBodyWikilinks(t *testing.T) {
	root := buildMoveFixture(t)

	src := filepath.Join(root, "Books", "Dune.md")
	out, err := runCLI(t, "", "move", src, "References/Books")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}

	if _, err := os.Stat(filepath.Join(root, "Books", "Dune.md")); !os.IsNotExist(err) {
		t.Fatalf("expected old path gone, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "References", "Books", "Dune.md")); err != nil {
		t.Fatalf("expected new path present, stat err=%v", err)
	}

	got := readNote(t, filepath.Join(root, "Reviews", "Dune Review.md"))
	want := []string{
		"Read [[References/Books/Dune]] last week.",
		"Also see [[References/Books/Dune|the book]].",
		"Jump to [[References/Books/Dune#Sequels]].",
		"Bare: [[Dune]].", // untouched
		"[[Books/Other]]", // unrelated, untouched
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("expected %q in:\n%s", w, got)
		}
	}
	if strings.Contains(got, "[[Books/Dune") {
		t.Errorf("expected no remaining [[Books/Dune... refs in:\n%s", got)
	}
}

func TestMoveFolderPrefixedFrontmatter(t *testing.T) {
	root := buildMoveFixture(t)

	src := filepath.Join(root, "Books", "Dune.md")
	if _, err := runCLI(t, "", "move", src, "References/Books"); err != nil {
		t.Fatalf("execute: %v", err)
	}

	categorized := readNote(t, filepath.Join(root, "categorized.md"))
	if !strings.Contains(categorized, "[[References/Books/Dune]]") {
		t.Errorf("expected categories rewritten, got:\n%s", categorized)
	}
	if !strings.Contains(categorized, "[[Books/Other]]") {
		t.Errorf("expected unrelated [[Books/Other]] preserved, got:\n%s", categorized)
	}
	if strings.Contains(categorized, "[[Books/Dune]]") {
		t.Errorf("expected no [[Books/Dune]] left in:\n%s", categorized)
	}

	parent := readNote(t, filepath.Join(root, "parent.md"))
	if !strings.Contains(parent, "[[References/Books/Dune]]") {
		t.Errorf("expected parent rewritten, got:\n%s", parent)
	}
}

func TestMoveBareWikilinksUntouched(t *testing.T) {
	root := buildMoveFixture(t)

	src := filepath.Join(root, "Books", "Dune.md")
	if _, err := runCLI(t, "", "move", src, "References/Books"); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// In the review note, the bare [[Dune]] reference must survive.
	review := readNote(t, filepath.Join(root, "Reviews", "Dune Review.md"))
	if !strings.Contains(review, "Bare: [[Dune]].") {
		t.Errorf("bare reference removed from review note:\n%s", review)
	}

	// In the moved note itself, the bare self-reference must also survive.
	moved := readNote(t, filepath.Join(root, "References", "Books", "Dune.md"))
	if !strings.Contains(moved, "Bare self: [[Dune]].") {
		t.Errorf("bare self-reference removed from moved note:\n%s", moved)
	}
	// And its folder-prefixed self-reference must have been rewritten.
	if !strings.Contains(moved, "Folder self: [[References/Books/Dune]] and [[References/Books/Dune|alias]].") {
		t.Errorf("folder-prefixed self-references not rewritten in moved note:\n%s", moved)
	}
}

func TestMoveCreatesIntermediateFolders(t *testing.T) {
	root := buildMoveFixture(t)

	deep := "A/B/C"
	if _, err := os.Stat(filepath.Join(root, "A")); !os.IsNotExist(err) {
		t.Fatalf("precondition: A should not exist, got %v", err)
	}

	src := filepath.Join(root, "Books", "Dune.md")
	if _, err := runCLI(t, "", "move", src, deep); err != nil {
		t.Fatalf("execute: %v", err)
	}

	info, err := os.Stat(filepath.Join(root, "A", "B", "C"))
	if err != nil {
		t.Fatalf("intermediate folders not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected directory at A/B/C, got %v", info.Mode())
	}
	// Permission bits; mask out umask noise by checking the owner+group+world
	// rwx triplet equals 0o755 when present.
	mode := info.Mode().Perm()
	if mode&0o755 != 0o755 {
		t.Errorf("expected mode 0755 bits set, got %o", mode)
	}

	if _, err := os.Stat(filepath.Join(root, "A", "B", "C", "Dune.md")); err != nil {
		t.Fatalf("expected moved note inside new folder, got %v", err)
	}
}

func TestMoveRejectsOutsideVault(t *testing.T) {
	root := buildMoveFixture(t)
	src := filepath.Join(root, "Books", "Dune.md")
	_, err := runCLI(t, "", "move", src, "../escape")
	if err == nil {
		t.Fatal("expected error for out-of-vault destination")
	}
	if !strings.Contains(err.Error(), "outside the vault") {
		t.Fatalf("expected outside-the-vault error, got %v", err)
	}
}

func TestMoveDryRunWritesNothing(t *testing.T) {
	root := buildMoveFixture(t)

	before := snapshotVault(t, root)

	src := filepath.Join(root, "Books", "Dune.md")
	out, err := runCLI(t, "", "move", src, "References/Books", "--dry-run")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}

	after := snapshotVault(t, root)
	if !mapsEqual(before, after) {
		t.Fatalf("dry-run modified files:\nbefore=%v\nafter=%v", before, after)
	}
	if !strings.Contains(out, "move:") {
		t.Errorf("dry-run output missing move header:\n%s", out)
	}
	if !strings.Contains(out, "Reviews/Dune Review.md:") {
		t.Errorf("dry-run output missing reviewer file change:\n%s", out)
	}
	if !strings.Contains(out, "frontmatter[categories]") {
		t.Errorf("dry-run output missing frontmatter change:\n%s", out)
	}
}

// TestMoveDryRunOmitsBareReferences asserts that for a note referenced via
// both folder-prefixed and bare wikilink forms, only the folder-prefixed
// entries appear in the dry-run output (bare [[Title]] is intentionally
// untouched), and that no file on disk is modified.
func TestMoveDryRunOmitsBareReferences(t *testing.T) {
	root := buildMoveFixture(t)

	beforeMtimes := snapshotMtimes(t, root)

	src := filepath.Join(root, "Books", "Dune.md")
	out, err := runCLI(t, "", "move", src, "References/Books", "--dry-run")
	if err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}

	// Folder-prefixed body refs in Reviews/Dune Review.md must appear in the
	// preview alongside the folder-prefixed self-references in the moved note.
	wantSubstrings := []string{
		"Read [[Books/Dune]] last week. -> Read [[References/Books/Dune]] last week.",
		"Also see [[Books/Dune|the book]]. -> Also see [[References/Books/Dune|the book]].",
		"Jump to [[Books/Dune#Sequels]]. -> Jump to [[References/Books/Dune#Sequels]].",
	}
	for _, w := range wantSubstrings {
		if !strings.Contains(out, w) {
			t.Errorf("expected %q in dry-run output:\n%s", w, out)
		}
	}

	// The bare [[Dune]] occurrences in Reviews/Dune Review.md (line "Bare:
	// [[Dune]].") and Books/Dune.md ("Bare self: [[Dune]].") must NOT appear
	// in the preview, since `move` leaves bare links alone by design.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Bare:") || strings.Contains(line, "Bare self:") {
			t.Errorf("bare-only line leaked into move dry-run output: %q\nfull output:\n%s", line, out)
		}
	}

	afterMtimes := snapshotMtimes(t, root)
	if !mtimesEqual(beforeMtimes, afterMtimes) {
		t.Fatalf("dry-run modified file mtimes:\nbefore=%v\nafter=%v", beforeMtimes, afterMtimes)
	}
}

func TestMoveRejectsSameFolder(t *testing.T) {
	root := buildMoveFixture(t)

	src := filepath.Join(root, "Books", "Dune.md")
	_, err := runCLI(t, "", "move", src, "Books")
	if err == nil {
		t.Fatal("expected error when destination folder equals source folder")
	}
	if !strings.Contains(err.Error(), "identical to current path") && !strings.Contains(err.Error(), "same as source") {
		t.Fatalf("expected identical/same-as-source error, got %v", err)
	}
}

func TestMoveDestinationExistsRefused(t *testing.T) {
	root := buildMoveFixture(t)
	// Plant a conflicting file at the destination.
	conflict := filepath.Join(root, "References", "Books", "Dune.md")
	if err := os.MkdirAll(filepath.Dir(conflict), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conflict, []byte("conflict\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(root, "Books", "Dune.md")
	_, err := runCLI(t, "", "move", src, "References/Books")
	if err == nil {
		t.Fatal("expected destination-exists error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected already-exists error, got %v", err)
	}
	// Source still in place.
	if _, err := os.Stat(filepath.Join(root, "Books", "Dune.md")); err != nil {
		t.Fatalf("source should still exist: %v", err)
	}
}

func TestMoveDestinationExistsForce(t *testing.T) {
	root := buildMoveFixture(t)
	conflict := filepath.Join(root, "References", "Books", "Dune.md")
	if err := os.MkdirAll(filepath.Dir(conflict), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conflict, []byte("stomped\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(root, "Books", "Dune.md")
	out, err := runCLI(t, "", "move", src, "References/Books", "--force")
	if err != nil {
		t.Fatalf("execute --force: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root, "Books", "Dune.md")); !os.IsNotExist(err) {
		t.Fatalf("expected source path gone, got %v", err)
	}
	got := readNote(t, filepath.Join(root, "References", "Books", "Dune.md"))
	if strings.Contains(got, "stomped") {
		t.Fatalf("force should have overwritten conflict, got:\n%s", got)
	}
}

func TestMoveByBareTitle(t *testing.T) {
	root := buildMoveFixture(t)

	if _, err := runCLI(t, "", "move", "Dune", "References/Books"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "References", "Books", "Dune.md")); err != nil {
		t.Fatalf("expected moved file, got %v", err)
	}
}

func TestMoveToVaultRoot(t *testing.T) {
	root := buildMoveFixture(t)

	src := filepath.Join(root, "Books", "Dune.md")
	if _, err := runCLI(t, "", "move", src, "."); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Dune.md")); err != nil {
		t.Fatalf("expected note at vault root, got %v", err)
	}

	// Folder-prefixed wikilinks should rewrite to bare links.
	review := readNote(t, filepath.Join(root, "Reviews", "Dune Review.md"))
	if !strings.Contains(review, "Read [[Dune]] last week.") {
		t.Errorf("expected bare rewrite of folder-prefixed body ref, got:\n%s", review)
	}
	if strings.Contains(review, "[[Books/Dune") {
		t.Errorf("expected no [[Books/Dune...]] left, got:\n%s", review)
	}
}
