package vault

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

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

	t.Run("preserves surrounding spaces in valid path", func(t *testing.T) {
		parent := t.TempDir()
		dir := filepath.Join(parent, " vault with spaces ")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		v, err := Open(&config.Config{Vault: dir})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v.Root != dir {
			t.Fatalf("root: got %q want %q", v.Root, dir)
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

func TestWalkIgnoreNormalizesBackslashes(t *testing.T) {
	v := newTestVault(t, []string{`Trash\Nested`}, map[string]string{
		"keep.md":               "# keep\n",
		"Trash/Nested/junk.md":  "# nested trash\n",
		"Trash/visible-note.md": "# still visible\n",
	})

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
	want := []string{"Trash/visible-note.md", "keep.md"}
	if !equalStrings(visited, want) {
		t.Fatalf("walk visited:\n got=%v\nwant=%v", visited, want)
	}
}

func TestWalkRespectsIgnoredFiles(t *testing.T) {
	files := map[string]string{
		"keep.md":             "# keep\n",
		"Drafts/private.md":   "# private\n",
		"Drafts/public.md":    "# public\n",
		"Nested/also-keep.md": "# keep\n",
	}
	v := newTestVault(t, []string{"Drafts/private.md"}, files)

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
		"Drafts/public.md",
		"Nested/also-keep.md",
		"keep.md",
	}
	if !equalStrings(visited, want) {
		t.Fatalf("walk visited:\n got=%v\nwant=%v", visited, want)
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

func TestWalkTemplatesDirNormalizesBackslashes(t *testing.T) {
	dir := t.TempDir()
	tmplDir := filepath.Join(dir, "Meta", "Templates")
	if err := os.MkdirAll(tmplDir, 0o755); err != nil {
		t.Fatalf("mkdir templates: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmplDir, "tpl.md"), []byte("---\ncreated: {{date}}\n---\nbroken\n"), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Meta", "real.md"), []byte("---\ntitle: Real\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	v, err := Open(&config.Config{
		Vault:     dir,
		Templates: config.TemplatesConfig{Dir: `Meta\Templates`},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

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
	want := []string{"Meta/real.md"}
	if !equalStrings(visited, want) {
		t.Fatalf("walk visited:\n got=%v\nwant=%v", visited, want)
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

func TestNotesLenientSkipsUnparsableFrontmatter(t *testing.T) {
	files := map[string]string{
		"a.md":   "---\ntitle: A\n---\nbody A\n",
		"bad.md": "---\ntitle: [unclosed\n---\nbody\n",
		"c.md":   "---\ntitle: C\n---\nbody C\n",
	}
	v := newTestVault(t, nil, files)

	if _, err := v.Notes(); err == nil {
		t.Fatal("Notes() must stay strict and fail on unparsable frontmatter")
	} else {
		var parseErr *ParseError
		if !errors.As(err, &parseErr) {
			t.Fatalf("Notes() error = %v, want a *ParseError", err)
		}
	}

	notes, skipped, err := v.NotesLenient()
	if err != nil {
		t.Fatalf("NotesLenient: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("want 2 parsed notes, got %d", len(notes))
	}
	if len(skipped) != 1 || filepath.Base(skipped[0].Path) != "bad.md" || skipped[0].Err == nil {
		t.Fatalf("skipped = %+v, want only bad.md with its parse error", skipped)
	}
	if strings.Contains(skipped[0].Err.Error(), skipped[0].Path) {
		t.Fatalf("skipped error should not repeat the path: %v", skipped[0].Err)
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
