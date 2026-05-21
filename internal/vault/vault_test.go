package vault

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// newTestVault writes the given files into a temp dir and returns an opened
// Vault. files maps vault-relative paths (forward slashes) to file contents.
// Directories along the way are created automatically; empty content with a
// trailing slash creates an empty directory.
func newTestVault(t *testing.T, ignore []string, files map[string]string) *Vault {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if strings.HasSuffix(rel, "/") {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", full, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir parent of %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	cfg := &config.Config{
		Vault:     dir,
		Ignore:    ignore,
		Templates: config.TemplatesConfig{Dir: "Templates"},
	}
	v, err := Open(cfg)
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	return v
}

func TestOpen(t *testing.T) {
	t.Run("validates directory", func(t *testing.T) {
		dir := t.TempDir()
		v, err := Open(&config.Config{Vault: dir})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v.Root == "" {
			t.Fatalf("expected absolute root, got empty")
		}
	})

	t.Run("rejects non-directory", func(t *testing.T) {
		dir := t.TempDir()
		f := filepath.Join(dir, "file.txt")
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := Open(&config.Config{Vault: f}); err == nil {
			t.Fatalf("expected error opening a file as vault")
		}
	})

	t.Run("rejects missing path", func(t *testing.T) {
		if _, err := Open(&config.Config{Vault: filepath.Join(t.TempDir(), "missing")}); err == nil {
			t.Fatalf("expected error on missing vault path")
		}
	})

	t.Run("rejects nil config", func(t *testing.T) {
		if _, err := Open(nil); err == nil {
			t.Fatalf("expected error on nil config")
		}
	})
}

func TestWalkRespectsIgnore(t *testing.T) {
	files := map[string]string{
		"root.md":                    "# root\n",
		"References/book.md":         "# book\n",
		"References/Nested/deep.md":  "# deep\n",
		".obsidian/workspace.json":   "{}",
		".obsidian/cache.md":         "# hidden parent\n",
		"Archive/old.md":             "# old\n",
		"Attachments/img.md":         "# stray\n",
		"Templates/Bases/notes.base": "filters: []",
		"Templates/Bases/skip.md":    "# nested ignore\n",
		"Templates/regular.md":       "# template\n",
		".hidden/secret.md":          "# hidden\n",
		"notes.txt":                  "ignore me",
	}
	ignore := []string{".obsidian", "Archive", "Attachments"}
	v := newTestVault(t, ignore, files)

	var visited []string
	if err := v.Walk(WalkFilter{}, func(n *Note) error {
		rel, err := filepath.Rel(v.Root, n.Path)
		if err != nil {
			return err
		}
		visited = append(visited, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	sort.Strings(visited)
	want := []string{
		"References/Nested/deep.md",
		"References/book.md",
		"root.md",
	}
	if !equalStrings(visited, want) {
		t.Fatalf("walk visited:\n got=%v\nwant=%v", visited, want)
	}
}

// TestWalkIgnoreNormalizesDotSlash asserts that ignore entries written with a
// leading `./` (or interior `/.`/double slash) still prune their directories.
// Before the fix, `ignore = ["./Trash"]` was stored verbatim in the ignore set
// and never matched the slash-relative path `Trash`, so the directory was
// walked despite the user opting it out.
func TestWalkIgnoreNormalizesDotSlash(t *testing.T) {
	files := map[string]string{
		"keep.md":         "# keep\n",
		"Trash/junk.md":   "# trash\n",
		"Sub/Deep/old.md": "# sub deep\n",
	}
	for _, ignoreEntry := range []string{"./Trash", "Trash/", "./Sub/Deep"} {
		t.Run(ignoreEntry, func(t *testing.T) {
			v := newTestVault(t, []string{ignoreEntry}, files)
			var visited []string
			if err := v.Walk(WalkFilter{}, func(n *Note) error {
				rel, err := filepath.Rel(v.Root, n.Path)
				if err != nil {
					return err
				}
				visited = append(visited, filepath.ToSlash(rel))
				return nil
			}); err != nil {
				t.Fatalf("walk: %v", err)
			}
			sort.Strings(visited)
			for _, p := range visited {
				if strings.HasPrefix(p, "Trash") && ignoreEntry != "./Sub/Deep" {
					t.Errorf("ignore %q failed to prune %s", ignoreEntry, p)
				}
				if strings.HasPrefix(p, "Sub/Deep") && ignoreEntry == "./Sub/Deep" {
					t.Errorf("ignore %q failed to prune nested %s", ignoreEntry, p)
				}
			}
		})
	}
}

func TestNotesSkipsTemplatesDir(t *testing.T) {
	// A template file with unrendered placeholders is not valid YAML; the
	// walker must skip it so commands like `find` aren't broken by it.
	files := map[string]string{
		"a.md":              "---\ntitle: A\n---\nbody\n",
		"Templates/App.md":  "---\ncreated: {{date}}\n---\nbroken\n",
		"Templates/Note.md": "---\ntitle: T\n---\nbody\n",
	}
	v := newTestVault(t, nil, files)

	notes, err := v.Notes()
	if err != nil {
		t.Fatalf("Notes: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("want 1 note, got %d", len(notes))
	}
	if filepath.Base(notes[0].Path) != "a.md" {
		t.Fatalf("want a.md, got %s", notes[0].Path)
	}
}

func TestWalkLazyNote(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"a.md": "---\ntitle: A\n---\nbody\n",
	})

	var seen *Note
	if err := v.Walk(WalkFilter{}, func(n *Note) error {
		seen = n
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	if seen == nil {
		t.Fatalf("expected one note")
	}
	if seen.Loaded() {
		t.Fatalf("note should not be auto-loaded by Walk")
	}
	if _, err := seen.Front(); err != nil {
		t.Fatalf("front: %v", err)
	}
	if !seen.Loaded() {
		t.Fatalf("note should be loaded after Front()")
	}
}

func TestNotesLoadsAll(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"a.md": "---\ntitle: A\n---\nbody A\n",
		"b.md": "---\ntitle: B\n---\nbody B\n",
	})
	notes, err := v.Notes()
	if err != nil {
		t.Fatalf("notes: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("want 2 notes, got %d", len(notes))
	}
	for _, n := range notes {
		if !n.Loaded() {
			t.Fatalf("Notes() must return loaded notes; %s is lazy", n.Path)
		}
	}
}

func TestResolveByTitle(t *testing.T) {
	v := newTestVault(t, nil, map[string]string{
		"Apple.md":             "# apple at root\n",
		"References/banana.md": "# banana\n",
		"People/Banana.md":     "# banana person\n",
		"References/Cherry.md": "# cherry\n",
	})

	t.Run("case-insensitive root match", func(t *testing.T) {
		n, err := v.ResolveByTitle("apple", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if filepath.Base(n.Path) != "Apple.md" {
			t.Fatalf("got %s, want Apple.md", filepath.Base(n.Path))
		}
	})

	t.Run("folder scope disambiguates", func(t *testing.T) {
		n, err := v.ResolveByTitle("banana", "People")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasSuffix(filepath.ToSlash(n.Path), "People/Banana.md") {
			t.Fatalf("got %s, want People/Banana.md", n.Path)
		}
	})

	t.Run("ambiguous without folder", func(t *testing.T) {
		_, err := v.ResolveByTitle("banana", "")
		if err == nil {
			t.Fatalf("expected ambiguity error")
		}
		msg := err.Error()
		if !strings.Contains(msg, "ambiguous") {
			t.Fatalf("error should mention ambiguity: %v", err)
		}
		if !strings.Contains(msg, "References/banana.md") || !strings.Contains(msg, "People/Banana.md") {
			t.Fatalf("error should list both candidates: %v", err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		if _, err := v.ResolveByTitle("durian", ""); err == nil {
			t.Fatalf("expected not-found error")
		}
	})

	t.Run("folder scoped not found", func(t *testing.T) {
		if _, err := v.ResolveByTitle("cherry", "People"); err == nil {
			t.Fatalf("expected not-found error in folder scope")
		}
	})

	t.Run("empty title rejected", func(t *testing.T) {
		if _, err := v.ResolveByTitle("", ""); err == nil {
			t.Fatalf("expected error on empty title")
		}
	})
}

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
