package vault

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

func TestSaveAtomic(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"a.md": "---\ntitle: A\n---\nbody\n",
	})

	n, err := v.ResolveByTitle("a", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := n.SetBody("changed\n"); err != nil {
		t.Fatalf("setbody: %v", err)
	}
	if err := n.Save(&v.Cfg.Frontmatter); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Verify content round-trips.
	data, err := os.ReadFile(n.Path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "changed") {
		t.Fatalf("saved body missing 'changed': %q", string(data))
	}

	// Verify no .note-*.tmp temp files linger in the directory.
	entries, err := os.ReadDir(filepath.Dir(n.Path))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".note-") && strings.HasSuffix(name, ".tmp") {
			t.Fatalf("temp file lingered: %s", name)
		}
	}
}

func TestSaveRefusesUnloaded(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"a.md": "---\ntitle: A\n---\nbody\n",
	})
	var n *Note
	if err := v.Walk(WalkFilter{}, func(x *Note) error {
		n = x
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	if n.Loaded() {
		t.Fatalf("expected lazy note")
	}
	if err := n.Save(&v.Cfg.Frontmatter); err == nil {
		t.Fatalf("expected error saving an unloaded note")
	}
}

func TestNoteAccessors(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"References/book.md": "---\ntitle: Book\n---\nbook body\n",
	})
	n, err := v.ResolveByTitle("book", "References")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if n.Title() != "book" {
		t.Fatalf("title: got %q want %q", n.Title(), "book")
	}
	folder, err := n.Folder(v.Root)
	if err != nil {
		t.Fatalf("folder: %v", err)
	}
	if folder != "References" {
		t.Fatalf("folder: got %q want %q", folder, "References")
	}

	body, err := n.Body()
	if err != nil {
		t.Fatalf("body: %v", err)
	}
	if !strings.Contains(body, "book body") {
		t.Fatalf("body content unexpected: %q", body)
	}

	front, err := n.Front()
	if err != nil {
		t.Fatalf("front: %v", err)
	}
	if v, _ := front.GetString("title"); v != "Book" {
		t.Fatalf("title fm: got %q want %q", v, "Book")
	}
}

func TestFolderForRootNote(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"root.md": "# root\n",
	})
	n, err := v.ResolveByTitle("root", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	folder, err := n.Folder(v.Root)
	if err != nil {
		t.Fatalf("folder: %v", err)
	}
	if folder != "" {
		t.Fatalf("root folder should be empty, got %q", folder)
	}
}

func TestNoteFolderRejectsEmptyPath(t *testing.T) {
	n := &Note{Path: ""}
	if _, err := n.Folder(t.TempDir()); err == nil {
		t.Fatal("Folder with empty note path should return an error")
	}
}

func TestNoteFolderRejectsOutOfVaultPath(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	n := &Note{Path: filepath.Join(outside, "note.md")}
	_, err := n.Folder(root)
	if err == nil {
		t.Fatal("Folder for out-of-vault note should return an error")
	}
	if !strings.Contains(err.Error(), "outside root") {
		t.Errorf("error should mention outside root: %v", err)
	}
}

func TestNilNoteMethods(t *testing.T) {
	var n *Note

	t.Run("Title returns empty", func(t *testing.T) {
		if got := n.Title(); got != "" {
			t.Errorf("nil Title() = %q, want empty", got)
		}
	})

	t.Run("Folder returns error", func(t *testing.T) {
		_, err := n.Folder("/some/root")
		if err == nil {
			t.Error("nil Folder() should return an error")
		}
	})

	t.Run("Load returns error", func(t *testing.T) {
		if err := n.Load(); err == nil {
			t.Error("nil Load() should return an error")
		}
	})

	t.Run("Loaded returns false", func(t *testing.T) {
		if n.Loaded() {
			t.Error("nil Loaded() should return false")
		}
	})

	t.Run("Save returns error", func(t *testing.T) {
		if err := n.Save(nil); err == nil {
			t.Error("nil Save() should return an error")
		}
	})
}

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

func TestOpenRejectsEmptyVaultPath(t *testing.T) {
	_, err := Open(&config.Config{Vault: ""})
	if err == nil {
		t.Fatal("expected error on empty vault path")
	}
	if !strings.Contains(err.Error(), "empty vault path") {
		t.Errorf("error should mention empty vault path: %v", err)
	}
}

func TestResolveByTitleDeepFolders(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"A/B/C/deep.md": "# deep\n",
		"deep.md":       "# shallow\n",
	})

	// Resolve in deep folder.
	n, err := v.ResolveByTitle("deep", "A/B/C")
	if err != nil {
		t.Fatalf("deep folder resolve: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(n.Path), "A/B/C/deep.md") {
		t.Errorf("got %s, want A/B/C/deep.md", n.Path)
	}

	// Root resolve should be ambiguous.
	_, err = v.ResolveByTitle("deep", "")
	if err == nil {
		t.Fatal("expected ambiguity error for deep.md")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error should mention ambiguity: %v", err)
	}
}

// TestTitleEmptyPath verifies that Title returns "" when Path is empty, rather
// than the "." that filepath.Base("") produces. An empty-path note should
// behave like a nil note for callers that branch on an empty title.
func TestTitleEmptyPath(t *testing.T) {
	n := &Note{Path: ""}
	if got := n.Title(); got != "" {
		t.Errorf("Title() on empty path = %q, want empty", got)
	}
}

// TestResolveByTitleSlashOnlyFolder verifies that a folder argument that
// normalizes to "" (e.g. "/" or "//") produces a vault-wide search and, on
// no-match, an error that does NOT claim the search was scoped to a folder.
// Before the fix, the not-found branch checked the raw folder argument,
// producing a misleading "no note titled X in /" message even though the
// actual search was vault-wide.
func TestResolveByTitleSlashOnlyFolder(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"People/Alice.md": "# alice\n",
	})
	// A slash-only folder normalizes to "" -> vault-wide search.
	n, err := v.ResolveByTitle("Alice", "/")
	if err != nil {
		t.Fatalf("expected vault-wide match for folder %q, got error: %v", "/", err)
	}
	if filepath.Base(n.Path) != "Alice.md" {
		t.Fatalf("got %s, want Alice.md", filepath.Base(n.Path))
	}

	// Not-found with slash-only folder: error must NOT include the raw "/" folder.
	_, err = v.ResolveByTitle("ZZZmissing", "/")
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if strings.Contains(err.Error(), `in "/"`) || strings.Contains(err.Error(), "in `/`") {
		t.Errorf("error should not mention slash-only folder: %v", err)
	}
}

func TestResolveByTitleRejectsTraversalFolder(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"People/Alice.md": "# alice\n",
	})
	for _, folder := range []string{"..", "../People", "People/../../Secrets"} {
		t.Run(folder, func(t *testing.T) {
			_, err := v.ResolveByTitle("Alice", folder)
			if err == nil {
				t.Fatal("expected traversal folder to be rejected")
			}
			if !strings.Contains(err.Error(), "escapes vault") {
				t.Errorf("error should mention escaping vault: %v", err)
			}
		})
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

// TestNilNoteAccessors verifies that Front, Body, and SetBody on a nil *Note
// return errors rather than panicking.
func TestNilNoteAccessors(t *testing.T) {
	var n *Note

	if _, err := n.Front(); err == nil {
		t.Error("nil Front() should return an error")
	}
	if _, err := n.Body(); err == nil {
		t.Error("nil Body() should return an error")
	}
	if err := n.SetBody("x"); err == nil {
		t.Error("nil SetBody() should return an error")
	}
}

// TestSaveCreatesParentDirs verifies that atomicWrite creates intermediate
// directories when saving a note to a path whose parent does not yet exist.
func TestSaveCreatesParentDirs(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"a.md": "---\ntitle: A\n---\nbody\n",
	})
	n, err := v.ResolveByTitle("a", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// Load the note before changing the path, so the content is in memory.
	if err := n.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	// Move the note to a not-yet-existing subdirectory.
	newPath := filepath.Join(v.Root, "NewDir", "SubDir", "moved.md")
	n.Path = newPath
	if err := n.Save(&v.Cfg.Frontmatter); err != nil {
		t.Fatalf("save to new dir: %v", err)
	}
	data, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("read moved note: %v", err)
	}
	if !strings.Contains(string(data), "title: A") {
		t.Errorf("moved note should contain 'title: A', got: %q", string(data))
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

// TestLoadEmptyPath verifies that Load on a Note with an empty Path returns
// an error rather than producing a confusing `open : no such file` from
// os.ReadFile. Callers that set Path lazily can otherwise leak the call site
// into a downstream error.
func TestLoadEmptyPath(t *testing.T) {
	n := &Note{Path: ""}
	err := n.Load()
	if err == nil {
		t.Fatal("Load with empty Path should return an error")
	}
	if !strings.Contains(err.Error(), "empty path") {
		t.Errorf("error should mention empty path: %v", err)
	}
}

// TestSaveEmptyPath verifies that Save on a loaded Note with an empty Path
// errors instead of writing temp files to the process cwd and then failing the
// rename. atomicWrite is given a destination, so an empty Path used to call
// os.CreateTemp(".", ...) and dump a sibling next to whatever the cwd was.
func TestSaveEmptyPath(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"a.md": "---\ntitle: A\n---\nbody\n",
	})
	n, err := v.ResolveByTitle("a", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := n.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	n.Path = ""
	if err := n.Save(&v.Cfg.Frontmatter); err == nil {
		t.Fatal("Save with empty Path should return an error")
	}
}

// TestTitleMultiDotFilename verifies that Title strips only the final
// extension, preserving interior dots in the stem. A file named
// "archive.tar.md" must have title "archive.tar", not "archive".
func TestTitleMultiDotFilename(t *testing.T) {
	cases := map[string]string{
		"archive.tar.md": "archive.tar",
		"v1.2.3.md":      "v1.2.3",
		"plain.md":       "plain",
		// filepath.Ext(".md") returns ".md" (no non-dot before the extension),
		// so TrimSuffix yields "". The file ".md" is also a hidden file that
		// Walk skips entirely, so this path is mostly for completeness.
		".md": "",
	}
	for path, wantTitle := range cases {
		n := &Note{Path: filepath.Join("/vault", path)}
		if got := n.Title(); got != wantTitle {
			t.Errorf("Title(%q) = %q, want %q", path, got, wantTitle)
		}
	}
}

// TestResolveByTitleMultiDotStem verifies that a note named "archive.tar.md"
// is resolvable by its full multi-dot stem.
func TestResolveByTitleMultiDotStem(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"archive.tar.md": "# a\n",
	})
	n, err := v.ResolveByTitle("archive.tar", "")
	if err != nil {
		t.Fatalf("ResolveByTitle: %v", err)
	}
	if filepath.Base(n.Path) != "archive.tar.md" {
		t.Errorf("got %s, want archive.tar.md", filepath.Base(n.Path))
	}
	// Resolving by just "archive" must not match a stem of "archive.tar".
	if _, err := v.ResolveByTitle("archive", ""); err == nil {
		t.Errorf("partial stem 'archive' should not match 'archive.tar.md'")
	}
}

// TestResolveByTitleSlashInTitle verifies that a title containing a path
// separator cannot match (filename stems never contain slashes), and the
// error message is the regular not-found form.
func TestResolveByTitleSlashInTitle(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"People/Alice.md": "# a\n",
	})
	if _, err := v.ResolveByTitle("People/Alice", ""); err == nil {
		t.Fatal("title with slash should not match a note's stem")
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
	v := &Vault{Root: dir, Cfg: nil}
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

// TestCleanSlashRelEdgeCases exercises the unexported cleanSlashRel directly
// for inputs Walk/ResolveByTitle/ignoreSet rely on: whitespace, dot segments,
// duplicate slashes, backslashes, and traversal forms.
func TestCleanSlashRelEdgeCases(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"   ", ""},
		{".", ""},
		{"./", ""},
		{"/", ""},
		{"//", ""},
		{"/.", ""},
		{"/./", ""},
		{"a", "a"},
		{"a/", "a"},
		{"/a", "a"},
		{"/a/", "a"},
		{"a//b", "a/b"},
		{"a/./b", "a/b"},
		{`a\b`, "a/b"},
		{`a\b\c`, "a/b/c"},
		{"a/b/..", "a"},
		{"..", ".."},
		{"../foo", "../foo"},
		{"a/../..", ".."},
		{"/..", ".."},
	}
	for _, tc := range cases {
		got := cleanSlashRel(tc.in)
		if got != tc.want {
			t.Errorf("cleanSlashRel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestRelEscapesRoot verifies the escape predicate for outputs of
// cleanSlashRel: anything matching ".." or "../*" escapes.
func TestRelEscapesRoot(t *testing.T) {
	escape := []string{"..", "../foo", "../../bar"}
	safe := []string{"", "a", "a/b", "...", "....", "a/..b"}
	for _, p := range escape {
		if !relEscapesRoot(p) {
			t.Errorf("relEscapesRoot(%q) = false, want true", p)
		}
	}
	for _, p := range safe {
		if relEscapesRoot(p) {
			t.Errorf("relEscapesRoot(%q) = true, want false", p)
		}
	}
}

// TestIgnoreSetSkipsEmptyAndEscaping verifies that empty/whitespace ignore
// entries are dropped, and entries that normalize to a traversal form are
// rejected rather than added to the set.
func TestIgnoreSetSkipsEmptyAndEscaping(t *testing.T) {
	cfg := &config.Config{
		Ignore: []string{"", "  ", "Archive", "../escape", "Trash/"},
		Templates: config.TemplatesConfig{
			Dir: "../../bad", // also escaping; must not land in the set.
		},
	}
	set := ignoreSet(cfg)
	if _, ok := set["Archive"]; !ok {
		t.Error("Archive should be in ignore set")
	}
	if _, ok := set["Trash"]; !ok {
		t.Error("Trash (from Trash/) should be in ignore set")
	}
	if _, ok := set[""]; ok {
		t.Error("empty string should not be in ignore set")
	}
	if _, ok := set["../escape"]; ok {
		t.Error("escaping entry should not be in ignore set")
	}
	if _, ok := set["../../bad"]; ok {
		t.Error("escaping templates dir should not be in ignore set")
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

// TestResolveByTitleEmptyVault verifies that ResolveByTitle returns a
// not-found error (not a nil/panic) on an empty vault.
func TestResolveByTitleEmptyVault(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{})
	_, err := v.ResolveByTitle("anything", "")
	if err == nil {
		t.Fatal("expected not-found error on empty vault")
	}
	if !strings.Contains(err.Error(), "no note titled") {
		t.Errorf("error should mention not-found: %v", err)
	}
}

// TestSaveWritesNewFileMode verifies that a fresh save (no existing file at
// dest) lands with 0o644 rather than leaking the temp file's 0o600 default
// from os.CreateTemp.
func TestSaveWritesNewFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits not meaningful on Windows")
	}
	v := newTestVault(t, nil, map[string]string{
		"a.md": "---\ntitle: A\n---\nbody\n",
	})
	n, err := v.ResolveByTitle("a", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := n.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	newPath := filepath.Join(v.Root, "brand-new.md")
	n.Path = newPath
	if err := n.Save(&v.Cfg.Frontmatter); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(newPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("new file mode: got %o, want %o", got, 0o644)
	}
}
