package query

import (
	"testing"
	"time"
)

// TestFromFlagsAgeAndCreatedComposeOnDifferentFields verifies that --age and
// --created produce predicates over different frontmatter keys (`last` vs
// `created`) so they compose correctly in an AND query. A note with the right
// `created` but missing `last` should not match a combined query.
func TestFromFlagsAgeAndCreatedComposeOnDifferentFields(t *testing.T) {
	recent := time.Now().AddDate(0, 0, -2).Format("2006-01-02")
	_, notes := newVault(t, map[string]string{
		"both.md":         "---\ncreated: " + recent + "\nlast: " + recent + "\n---\n",
		"created-only.md": "---\ncreated: " + recent + "\n---\n",
	})
	pred, err := FromFlags(Flags{
		Age:          "<30d",
		Created:      "<30d",
		MinBacklinks: -1, MaxBacklinks: -1, MinLinks: -1, MaxLinks: -1,
	})
	if err != nil {
		t.Fatalf("FromFlags: %v", err)
	}
	if !pred(notes["both.md"]) {
		t.Errorf("a note with both `last` and `created` recent should match --age<30d AND --created<30d")
	}
	if pred(notes["created-only.md"]) {
		t.Errorf("a note missing `last` should not match --age<30d (which queries `last`) even if `created` is recent")
	}
}

// TestBuildGraphCaseDifferingTitlesShareKey documents the behaviour when two
// notes with case-differing filenames (e.g. "Foo.md" and "foo.md") would
// produce the same lowercased title key. The graph keys by lowercased stem,
// so the second note's entry clobbers the first in Titles/PathsByTitle.
// In practice case-insensitive filesystems prevent this, but this test pins
// the contract for case-sensitive filesystems (e.g. Linux): the latter note
// wins, and consumers must accept a single PathsByTitle entry per key.
