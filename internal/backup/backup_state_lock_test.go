package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSynchronizationGuardsAreNeitherArchivedNorRestored covers the two locks
// that live beside the synchronization state rather than under .bnotes/locks.
// The rest of that directory is causal state a checkpoint must carry; the
// guards carry nothing, and putting an archived one back would replace the inode
// a live import or export holds. An archive written before the exclusion still
// names import.lock, so restoring it must skip the entry rather than refuse.
func TestSynchronizationGuardsAreNeitherArchivedNorRestored(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	writeFiles(t, vaultRoot, map[string]string{
		"Daily.md":                     "# Daily\n",
		".bnotes/sync/enrollment.json": "{}\n",
		".bnotes/sync/import.lock":     "",
		".bnotes/sync/export.lock":     "",
	})
	result, err := Create(vaultRoot, CreateOptions{Now: func() time.Time { return time.Unix(1, 0) }})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	entries := readArchiveEntries(t, result.ArchivePath)
	for _, name := range []string{".bnotes/sync/import.lock", ".bnotes/sync/export.lock"} {
		if _, archived := entries[name]; archived {
			t.Fatalf("archive carries %s", name)
		}
	}
	if _, archived := entries[".bnotes/sync/enrollment.json"]; !archived {
		t.Fatal("archive dropped the synchronization state beside the guards")
	}

	live := filepath.Join(vaultRoot, ".bnotes", "sync", "import.lock")
	before, err := os.Stat(live)
	if err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(vaultRoot, "backups", "legacy.tar.gz")
	writeArchive(t, legacy, []archiveFixtureEntry{
		{name: "Daily.md", body: "restored\n"},
		{name: ".bnotes/sync/enrollment.json", body: "{}\n"},
		{name: ".bnotes/sync/import.lock", body: "archived inode\n"},
	})
	restored, err := Restore(vaultRoot, "backups/legacy.tar.gz", RestoreOptions{Wipe: true})
	if err != nil {
		t.Fatalf("restoring an archive that still carries import.lock: %v", err)
	}
	if restored.Files != 2 {
		t.Fatalf("Files = %d, want 2: the guard is not restored content", restored.Files)
	}
	after, err := os.Stat(live)
	if err != nil {
		t.Fatalf("the live import guard did not survive the wipe: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("restore replaced the live import guard's inode")
	}
	assertFileContent(t, live, "")
	assertFileContent(t, filepath.Join(vaultRoot, ".bnotes", "sync", "export.lock"), "")
}
