package vault

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestNotesEmptyVault(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{})
	notes, err := v.Notes()
	if err != nil {
		t.Fatalf("Notes on empty vault: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("want 0 notes, got %d", len(notes))
	}
}

func TestWalkCallbackSkipDir(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"a.md":       "# a\n",
		"Sub/b.md":   "# b\n",
		"Sub/c.md":   "# c\n",
		"Other/d.md": "# d\n",
	})
	// Return SkipDir on the first file inside Sub/ to skip the rest of that dir.
	var visited []string
	err := v.Walk(WalkFilter{}, func(n *Note) error {
		rel, _ := filepath.Rel(v.Root, n.Path)
		relSlash := filepath.ToSlash(rel)
		visited = append(visited, relSlash)
		// SkipDir on a file skips remaining files in the same directory.
		if relSlash == "Sub/b.md" {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	// Sub/b.md should appear (it was visited before SkipDir was returned),
	// but Sub/c.md should be skipped. Other/d.md and a.md should still appear.
	for _, p := range visited {
		if p == "Sub/c.md" {
			t.Errorf("Sub/c.md should have been skipped after SkipDir, visited: %v", visited)
		}
	}
}

// TestWalkNilVault verifies that Walk on a nil vault returns an error rather
// than panicking with a nil-pointer dereference.
func TestWalkNilVault(t *testing.T) {
	var v *Vault
	err := v.Walk(WalkFilter{}, func(n *Note) error { return nil })
	if err == nil {
		t.Fatal("expected error from Walk on nil vault")
	}
}

// TestWalkNilCallback verifies that Walk with a nil callback returns a clear
// error rather than panicking inside filepath.WalkDir.
func TestWalkNilCallback(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{"a.md": "# a\n"})
	err := v.Walk(WalkFilter{}, nil)
	if err == nil {
		t.Fatal("expected error from Walk with nil callback")
	}
}

// TestWalkCaseInsensitiveExtension verifies that Walk includes .MD and .Md
// files, not just .md, matching the EqualFold check on the extension.
func TestWalkCaseInsensitiveExtension(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"lower.md": "# lower\n",
		"upper.MD": "# upper\n",
		"mixed.Md": "# mixed\n",
		"skip.txt": "not a note",
	})
	var visited []string
	if err := v.Walk(WalkFilter{}, func(n *Note) error {
		visited = append(visited, filepath.Base(n.Path))
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	sort.Strings(visited)
	want := []string{"lower.md", "mixed.Md", "upper.MD"}
	if !equalStrings(visited, want) {
		t.Errorf("walk visited %v, want %v", visited, want)
	}
}

// TestWalkDeeplyNested verifies Walk descends into arbitrarily deep folder
// hierarchies (no early termination by depth).
func TestWalkDeeplyNested(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"a/b/c/d/e/f/g/h/deep.md": "# deep\n",
		"shallow.md":              "# shallow\n",
	})
	var visited []string
	if err := v.Walk(WalkFilter{}, func(n *Note) error {
		rel, _ := filepath.Rel(v.Root, n.Path)
		visited = append(visited, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	sort.Strings(visited)
	want := []string{"a/b/c/d/e/f/g/h/deep.md", "shallow.md"}
	if !equalStrings(visited, want) {
		t.Errorf("walk visited %v, want %v", visited, want)
	}
}

// TestWalkRegularNonMarkdownExtensions verifies that files with non-.md
// extensions (.txt, .json, no extension, .markdown) are skipped.
func TestWalkRegularNonMarkdownExtensions(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"note.md":       "# n\n",
		"data.json":     "{}",
		"readme":        "no ext",
		"long.markdown": "# m",
		"weird.md.bak":  "# b",
	})
	var visited []string
	if err := v.Walk(WalkFilter{}, func(n *Note) error {
		visited = append(visited, filepath.Base(n.Path))
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	sort.Strings(visited)
	want := []string{"note.md"}
	if !equalStrings(visited, want) {
		t.Errorf("walk visited %v, want %v", visited, want)
	}
}

// TestWalkHandlesNilCfg verifies that Walk does not panic on a vault whose
// Cfg field is nil (defensive — Open always populates Cfg, but a hand-built
// Vault should still behave). ignoreSet(nil) returns an empty set.
func TestWalkHandlesNilCfg(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("# a\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	v := &Vault{Root: dir}
	var count int
	err := v.Walk(WalkFilter{}, func(n *Note) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("walk with nil cfg: %v", err)
	}
	if count != 1 {
		t.Errorf("want 1 note, got %d", count)
	}
}

// TestWalkSkipsNestedHiddenDirs verifies that hidden directories anywhere in
// the tree (not just at the root) are pruned. A note inside `Sub/.cache/` must
// not be visited.
func TestWalkSkipsNestedHiddenDirs(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"keep.md":              "# keep\n",
		"Sub/note.md":          "# n\n",
		"Sub/.cache/hidden.md": "# h\n",
		"Sub/.git/HEAD.md":     "# g\n",
	})
	var visited []string
	if err := v.Walk(WalkFilter{}, func(n *Note) error {
		rel, _ := filepath.Rel(v.Root, n.Path)
		visited = append(visited, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	sort.Strings(visited)
	want := []string{"Sub/note.md", "keep.md"}
	if !equalStrings(visited, want) {
		t.Errorf("walk visited %v, want %v", visited, want)
	}
}
