package query

import (
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func TestFromFlags(t *testing.T) {
	v, notes := newVault(t, map[string]string{
		"References/a.md": "---\ntags: [foo]\ncategories:\n  - \"[[Books]]\"\nstatus: Reading\n---\nLinks to [[b]].\n",
		"b.md":            "---\ntags: [foo, bar]\n---\nbody\n",
		"c.md":            "---\ntitle: c\n---\nbody\n",
	})

	all := []*vault.Note{notes["References/a.md"], notes["b.md"], notes["c.md"]}

	t.Run("multi-term AND", func(t *testing.T) {
		pred, err := FromFlags(Flags{Tags: []string{"foo"}, Status: "Reading", MinBacklinks: -1, MaxBacklinks: -1, MinLinks: -1, MaxLinks: -1})
		if err != nil {
			t.Fatalf("FromFlags: %v", err)
		}
		if !pred(notes["References/a.md"]) {
			t.Errorf("a should match")
		}
		if pred(notes["b.md"]) {
			t.Errorf("b should not match (no status)")
		}
	})

	t.Run("folder requires root", func(t *testing.T) {
		pred, err := FromFlags(Flags{Folder: "References", Root: v.Root, MinBacklinks: -1, MaxBacklinks: -1, MinLinks: -1, MaxLinks: -1})
		if err != nil {
			t.Fatalf("FromFlags: %v", err)
		}
		if !pred(notes["References/a.md"]) {
			t.Errorf("a should match folder")
		}
		if pred(notes["b.md"]) {
			t.Errorf("b should not match folder")
		}
	})

	t.Run("orphan needs graph", func(t *testing.T) {
		if _, err := FromFlags(Flags{Orphan: true, MinBacklinks: -1, MaxBacklinks: -1, MinLinks: -1, MaxLinks: -1}); err == nil {
			t.Errorf("expected error: orphan without graph")
		}
		g := BuildGraph(all)
		pred, err := FromFlags(Flags{Orphan: true, Graph: g, MinBacklinks: -1, MaxBacklinks: -1, MinLinks: -1, MaxLinks: -1})
		if err != nil {
			t.Fatalf("FromFlags: %v", err)
		}
		if pred(notes["b.md"]) {
			t.Errorf("b is linked from a, not orphan")
		}
		if !pred(notes["c.md"]) {
			t.Errorf("c should be orphan")
		}
	})

	t.Run("age error", func(t *testing.T) {
		if _, err := FromFlags(Flags{Age: "bogus", MinBacklinks: -1, MaxBacklinks: -1, MinLinks: -1, MaxLinks: -1}); err == nil {
			t.Errorf("expected error from bogus age")
		}
	})

	t.Run("folder without root errors", func(t *testing.T) {
		_, err := FromFlags(Flags{Folder: "People", MinBacklinks: -1, MaxBacklinks: -1, MinLinks: -1, MaxLinks: -1})
		if err == nil {
			t.Errorf("expected error: folder without root")
		}
	})

	t.Run("empty flags matches all", func(t *testing.T) {
		pred, err := FromFlags(Flags{MinBacklinks: -1, MaxBacklinks: -1, MinLinks: -1, MaxLinks: -1})
		if err != nil {
			t.Fatalf("FromFlags: %v", err)
		}
		for path, n := range notes {
			if !pred(n) {
				t.Errorf("empty flags should match %s", path)
			}
		}
	})

	t.Run("min-backlinks zero is no-op without graph", func(t *testing.T) {
		pred, err := FromFlags(Flags{MinBacklinks: 0, MinLinks: 0, MaxBacklinks: -1, MaxLinks: -1})
		if err != nil {
			t.Fatalf("FromFlags with MinBacklinks=0 should not require a graph: %v", err)
		}
		for path, n := range notes {
			if !pred(n) {
				t.Errorf("MinBacklinks=0 is trivially true, should match %s", path)
			}
		}
	})
}

// TestFromFlagsZeroValueIntFieldsRequireGraph documents the sentinel convention:
// the Go zero value (0) for MaxBacklinks/MaxLinks activates the filter, so
// callers must set them to -1 to mean "not set". This test pins down the
// expected error when a zero-valued Flags struct is used without a graph.
func TestFromFlagsZeroValueIntFieldsRequireGraph(t *testing.T) {
	// Zero-valued Flags has MaxBacklinks=0 and MaxLinks=0, both of which
	// activate filters that require a graph.
	_, err := FromFlags(Flags{})
	if err == nil {
		t.Errorf("FromFlags(Flags{}) should error because MaxBacklinks=0 and MaxLinks=0 require a graph")
	}

	// With sentinel values of -1, no graph is needed and all notes match.
	pred, err := FromFlags(Flags{MinBacklinks: -1, MaxBacklinks: -1, MinLinks: -1, MaxLinks: -1})
	if err != nil {
		t.Fatalf("FromFlags with sentinel values: %v", err)
	}
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntitle: a\n---\n",
	})
	if !pred(notes["a.md"]) {
		t.Errorf("all-sentinel Flags should match every note")
	}
}

// TestFromFlagsSkipsEmptyTagsAndCategories verifies that empty strings in the
// Tags/Categories slices are silently dropped rather than producing a
// match-nothing predicate. This matters when callers build slices from raw user
// input where an empty entry signals "no value given".
func TestFromFlagsSkipsEmptyTagsAndCategories(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags: [foo]\ncategories:\n  - \"[[Books]]\"\n---\n",
	})
	pred, err := FromFlags(Flags{
		Tags:         []string{"", "foo", ""},
		Categories:   []string{"", "books"},
		MinBacklinks: -1, MaxBacklinks: -1, MinLinks: -1, MaxLinks: -1,
	})
	if err != nil {
		t.Fatalf("FromFlags: %v", err)
	}
	if !pred(notes["a.md"]) {
		t.Errorf("empty entries in Tags/Categories should be skipped, leaving the real terms to match")
	}
}
