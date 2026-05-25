package query

import (
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPredicatesRejectErrorFront verifies that predicates which read frontmatter
// degrade gracefully when Front() returns an error or the requested key is
// absent. We use notes that have neither frontmatter nor the target key.
func TestPredicatesAbsentKey(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"empty.md": "no frontmatter at all\n",
		"other.md": "---\ntitle: o\n---\nbody\n",
	})
	for _, name := range []string{"empty.md", "other.md"} {
		n := notes[name]
		if Tag("foo")(n) {
			t.Errorf("Tag(foo) should not match %s (no tags)", name)
		}
		if Category("books")(n) {
			t.Errorf("Category(books) should not match %s (no categories)", name)
		}
		if Status("reading")(n) {
			t.Errorf("Status(reading) should not match %s (no status)", name)
		}
		if Type("goal")(n) {
			t.Errorf("Type(goal) should not match %s (no type/kind)", name)
		}
		if Has("status")(n) {
			t.Errorf("Has(status) should not match %s", name)
		}
		if !Missing("status")(n) {
			t.Errorf("Missing(status) should match %s", name)
		}
		if Age(">", 24*time.Hour, "created")(n) {
			t.Errorf("Age should not match %s (no date)", name)
		}
	}
}

// TestFolderTrimsWhitespace verifies that a folder argument padded with spaces
// (e.g. from a shell that quoted the value with stray padding) is normalized.
// Before the fix, `--folder "  References  "` retained the surrounding spaces
// after path.Clean (which only collapses path syntax, not whitespace), so the
// predicate matched a literal folder named "  References  " and never matched
// the real "References" folder.

// TestHasAllowsNullValue documents that Has reports presence even when the
// value is YAML null. This matches the broader frontmatter model: nil-valued
// keys are present but empty, and `bn unset` is the only operation that
// removes a key entirely. Has and Missing must therefore be exact inverses on
// every note that loads.
func TestHasAllowsNullValue(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags: ~\n---\nbody\n",
		"b.md": "---\ntags: []\n---\nbody\n",
	})
	for _, name := range []string{"a.md", "b.md"} {
		n := notes[name]
		if !Has("tags")(n) {
			t.Errorf("Has(tags) should be true for %s (key is present, value is null/empty)", name)
		}
		if Missing("tags")(n) {
			t.Errorf("Missing(tags) should be false for %s", name)
		}
	}
}

// TestFolderPredicateRejectsNilNote verifies that the Folder predicate returns
// false for nil notes, including when the folder is empty (matches everything
// for non-nil notes). Before the fix, `Folder("", root)(nil)` returned true
// because the early-return for the empty-folder case did not guard against nil.

// TestTagPredicateRejectsNilNote verifies that the Tag predicate returns false
// for a nil note rather than panicking. The frontmatter-reading predicates rely
// on Note.Load() to return an error for a nil note; this asserts that contract.
func TestTagPredicateRejectsNilNote(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Tag should not panic on nil note, got: %v", r)
		}
	}()
	if Tag("foo")(nil) {
		t.Errorf("Tag(foo)(nil) = true, want false")
	}
	if Category("books")(nil) {
		t.Errorf("Category(books)(nil) = true, want false")
	}
	if Status("reading")(nil) {
		t.Errorf("Status(reading)(nil) = true, want false")
	}
	if Type("goal")(nil) {
		t.Errorf("Type(goal)(nil) = true, want false")
	}
	if Has("status")(nil) {
		t.Errorf("Has(status)(nil) = true, want false")
	}
	if Missing("status")(nil) {
		t.Errorf("Missing(status)(nil) = true, want false")
	}
	if Age(">", 24*time.Hour, "created")(nil) {
		t.Errorf("Age(nil) = true, want false")
	}
}

// TestTagPredicateEmptyList asserts that an empty tags list (`tags: []`) never
// matches any tag query. Before the fix this was already correct; the test
// pins down the behaviour against regressions where an empty list might be
// confused for "matches anything".

// TestMissingIsStrictComplementOfHasOnParseError verifies that Missing returns
// true when the note's frontmatter cannot be parsed, so that Has and Missing
// partition every loadable note exactly. Before the fix both returned false on
// a YAML parse error, leaving the note in neither set.
func TestMissingIsStrictComplementOfHasOnParseError(t *testing.T) {
	// Write a file whose frontmatter is a YAML sequence at the document root,
	// which the frontmatter parser rejects ("top-level must be a mapping").
	// Use a bare *vault.Note so we can bypass Vault.Notes (which fails fast on
	// parse errors) and exercise the predicate's own error path.
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.md")
	if err := os.WriteFile(path, []byte("---\n  - x\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	n := &vault.Note{Path: path}
	if Has("anything")(n) {
		t.Errorf("Has(anything) should be false when frontmatter fails to parse")
	}
	if !Missing("anything")(n) {
		t.Errorf("Missing(anything) should be true when frontmatter fails to parse (strict complement of Has)")
	}
}

// TestMissingRejectsNilNote verifies that Missing rejects a nil note rather
// than treating it as "frontmatter failed to load, so the key is absent". A
// nil note isn't a real note and should match no predicate.

// TestMissingRejectsNilNote verifies that Missing rejects a nil note rather
// than treating it as "frontmatter failed to load, so the key is absent". A
// nil note isn't a real note and should match no predicate.
func TestMissingRejectsNilNote(t *testing.T) {
	if Missing("x")(nil) {
		t.Errorf("Missing(x)(nil) = true, want false")
	}
	if Has("x")(nil) {
		t.Errorf("Has(x)(nil) = true, want false")
	}
}

// TestParseAgeRoundTripsAllUnits verifies every documented unit (s, m, h, d)
// is accepted with a non-zero value.

// TestMissingTrueForUnreadableNote pins down the documented contract that
// Missing returns true when frontmatter fails to load (e.g. file disappeared).
// Pair this with TestMissingIsStrictComplementOfHasOnParseError: that test
// covers parse errors, this one covers file-read errors.
func TestMissingTrueForUnreadableNote(t *testing.T) {
	// A note whose path points to a nonexistent file: Front() returns a
	// read error, Missing should still report the property as absent
	// (consistent with the "every loadable property is reported correctly,
	// unloadable notes report all properties absent" contract).
	n := &vault.Note{Path: "/nonexistent/path/to/note.md"}
	if Has("anything")(n) {
		t.Errorf("Has(anything) should be false for an unreadable note")
	}
	if !Missing("anything")(n) {
		t.Errorf("Missing(anything) should be true for an unreadable note")
	}
}

// TestTagPredicateListContainingOnlyNull pins down the behaviour of a tags list
// whose only entry is YAML null (`tags: [~]`). stringifyTag(nil) returns the
// empty string, so the loop compares "" to the (non-empty) query and returns
// false. A `Tag("")` call (which FromFlags filters out before it reaches the
// predicate) would technically match, but the FromFlags guard makes that
// unreachable through normal use — this test pins the predicate-level
// behaviour to guard against regressions in stringifyTag's nil handling.
