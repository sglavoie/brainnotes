package vault

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// noteSourceFixtures are the byte streams the two constructions must agree on.
// They cover the shapes Parse records state for beyond the front map — BOM,
// CRLF frontmatter, a missing or empty frontmatter block, a body that is itself
// a document — because those live in unexported Document fields that a
// front-map-only comparison would not see.
func noteSourceFixtures(t *testing.T) map[string]string {
	t.Helper()
	fixtures := map[string]string{
		"plain":                     "---\ntitle: A\ntags: [x, y]\n---\nbody\n",
		"no frontmatter":            "just a body\n",
		"empty file":                "",
		"empty frontmatter block":   "---\n---\nbody\n",
		"no trailing newline":       "---\ntitle: A\n---\nbody",
		"crlf frontmatter":          "---\r\ntitle: A\r\n---\r\nbody\r\n",
		"bom":                       "\ufeff---\ntitle: A\n---\nbody\n",
		"unicode nfd key and value": "---\ntitle: Cafe\u0301\n---\nCafe\u0301\n",
		"body holds a delimiter":    "---\ntitle: A\n---\nbody\n---\nnot frontmatter\n",
		"list and nested mapping":   "---\ntags:\n  - a\n  - b\nmeta:\n  k: v\n---\nbody\n",
		"typed scalars":             "---\ncount: 3\nratio: 1.5\ndone: true\nday: 2026-08-25\nat: 2026-08-25T10:00:00Z\n---\nbody\n",
		"wikilink property":         "---\nparent: \"[[Deep Work]]\"\n---\nsee [[Deep Work|it]]\n",
		"only a body newline":       "\n",
	}
	// The repo's own frontmatter fixtures are the authority on what a real note
	// looks like, so they are fixtures here too rather than being approximated by
	// the inline cases above.
	golden, err := filepath.Glob(filepath.Join("..", "frontmatter", "testdata", "*.md"))
	if err != nil {
		t.Fatalf("glob frontmatter testdata: %v", err)
	}
	if len(golden) == 0 {
		t.Fatal("no frontmatter testdata fixtures found; the round-trip check would be weaker than it claims")
	}
	for _, path := range golden {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		fixtures["testdata/"+filepath.Base(path)] = string(data)
	}
	return fixtures
}

// TestNewNoteFromSourceRoundTripsIdenticallyToLoad is the whole contract: a note
// built from bytes must be indistinguishable from one Load produced for the same
// bytes, field for field. Comparing the whole struct rather than a few accessors
// is deliberate — sourceHash and sourcePath are what Save's compare-and-swap
// reads, so a drift in either would turn into a lost update or a spurious
// conflict rather than a visibly wrong document.
func TestNewNoteFromSourceRoundTripsIdenticallyToLoad(t *testing.T) {
	for name, source := range noteSourceFixtures(t) {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "note.md")
			if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}

			loaded := NewNote(root, path)
			if err := loaded.Load(); err != nil {
				t.Fatalf("Load: %v", err)
			}
			built, err := NewNoteFromSource(root, path, []byte(source))
			if err != nil {
				t.Fatalf("NewNoteFromSource: %v", err)
			}

			if !reflect.DeepEqual(built, loaded) {
				t.Fatalf("built note differs from the loaded one:\nbuilt  = %#v\nloaded = %#v", built, loaded)
			}
			if !built.Loaded() {
				t.Fatal("built note reports not loaded")
			}
			if built.SourceHash() != HashBytes([]byte(source)) {
				t.Fatalf("SourceHash() = %q, want the hash of the source bytes", built.SourceHash())
			}
			if built.sourcePath != path {
				t.Fatalf("sourcePath = %q, want %q", built.sourcePath, path)
			}

			// Marshal is the observable end of the parsed document, so agreeing there
			// proves the Document — including the unexported BOM and line-ending state
			// DeepEqual compares — actually round-trips rather than merely matching.
			builtOut, err := built.Marshal(nil)
			if err != nil {
				t.Fatalf("Marshal built: %v", err)
			}
			loadedOut, err := loaded.Marshal(nil)
			if err != nil {
				t.Fatalf("Marshal loaded: %v", err)
			}
			if string(builtOut) != string(loadedOut) {
				t.Fatalf("Marshal differs:\nbuilt  = %q\nloaded = %q", builtOut, loadedOut)
			}
		})
	}
}

// TestNewNoteFromSourceSavesWithoutRereading closes the loop the constructor
// exists for: a note built from cached bytes must be writable, and its
// compare-and-swap precondition must be armed against the revision those bytes
// came from — so a concurrent edit is a conflict, not a silent overwrite.
func TestNewNoteFromSourceSavesWithoutRereading(t *testing.T) {
	root := txnTestRoot(t, map[string]string{"a.md": "---\ntitle: A\n---\nold\n"})
	path := filepath.Join(root, "a.md")
	source := readFileString(t, path)

	note, err := NewNoteFromSource(root, path, []byte(source))
	if err != nil {
		t.Fatalf("NewNoteFromSource: %v", err)
	}
	if err := note.SetBody("new\n"); err != nil {
		t.Fatalf("SetBody: %v", err)
	}
	if err := note.Save(nil); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := readFileString(t, path); !strings.HasSuffix(got, "new\n") {
		t.Fatalf("a.md = %q, want the new body", got)
	}

	// Stale bytes: the file on disk has moved on, so the next Save must refuse.
	stale, err := NewNoteFromSource(root, path, []byte(source))
	if err != nil {
		t.Fatalf("NewNoteFromSource stale: %v", err)
	}
	if err := stale.SetBody("clobber\n"); err != nil {
		t.Fatalf("SetBody stale: %v", err)
	}
	if err := stale.Save(nil); !IsTxnConflict(err) {
		t.Fatalf("stale Save err = %v, want a conflict", err)
	}
	if got := readFileString(t, path); strings.Contains(got, "clobber") {
		t.Fatalf("stale save clobbered the file: %q", got)
	}
}

// TestNewNoteFromSourceRejectsBadInput pins the two guards Load also applies, so
// the constructor cannot produce a note that Save would only reject later.
func TestNewNoteFromSourceRejectsBadInput(t *testing.T) {
	root := t.TempDir()

	if _, err := NewNoteFromSource(root, "", nil); err == nil {
		t.Fatal("empty path accepted")
	}
	if _, err := NewNoteFromSource(root, "relative.md", nil); err == nil {
		t.Fatal("relative path accepted")
	}

	path := filepath.Join(root, "bad.md")
	bad := []byte("---\ntitle: [unterminated\n---\nbody\n")
	if err := os.WriteFile(path, bad, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	built, builtErr := NewNoteFromSource(root, path, bad)
	loadErr := NewNote(root, path).Load()
	if builtErr == nil {
		t.Fatalf("unparseable source accepted: %#v", built)
	}
	if loadErr == nil {
		t.Fatal("Load accepted the same unparseable bytes; the fixture is not testing what it claims")
	}
	if builtErr.Error() != loadErr.Error() {
		t.Fatalf("parse error differs from Load's:\nbuilt = %v\nload  = %v", builtErr, loadErr)
	}
}
