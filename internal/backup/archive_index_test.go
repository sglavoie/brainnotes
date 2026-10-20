package backup

import (
	"fmt"
	"strings"
	"testing"
)

// The implied-directory set must name every ancestor of every entry, including
// ancestors first reached through a sibling branch, so a later file entry on
// any of them is refused as a lookup rather than a scan.
func TestArchiveIndexFindsDescendantsThroughSiblingBranches(t *testing.T) {
	idx := newArchiveIndex()
	for _, key := range []string{"a/c/x.md", "a/d/z.md"} {
		if err := checkArchiveConflicts(key, entryFile, idx); err != nil {
			t.Fatalf("checkArchiveConflicts(%q): %v", key, err)
		}
		idx.add(key, entryFile)
	}

	for _, key := range []string{"a", "a/c", "a/d"} {
		err := checkArchiveConflicts(key, entryFile, idx)
		if err == nil || !strings.Contains(err.Error(), "conflicts with descendant") {
			t.Fatalf("file %q over existing descendants = %v, want a descendant conflict", key, err)
		}
		if err := checkArchiveConflicts(key, entryDir, idx); err != nil {
			t.Fatalf("directory %q over its own descendants = %v, want no conflict", key, err)
		}
	}
	if err := checkArchiveConflicts("a/b", entryFile, idx); err != nil {
		t.Fatalf("an unrelated sibling = %v, want no conflict", err)
	}
}

// Validation stays linear in the number of entries: a wide archive with many
// files under one directory must not rescan every earlier entry per file.
func TestArchiveIndexScalesWithWideDirectories(t *testing.T) {
	idx := newArchiveIndex()
	for i := 0; i < 50000; i++ {
		key := fmt.Sprintf("notes/sub%03d/note-%05d.md", i%500, i)
		if err := checkArchiveConflicts(key, entryFile, idx); err != nil {
			t.Fatalf("checkArchiveConflicts(%q): %v", key, err)
		}
		idx.add(key, entryFile)
	}
	if got := len(idx.implied); got != 501 {
		t.Fatalf("implied directories = %d, want 501", got)
	}
}
