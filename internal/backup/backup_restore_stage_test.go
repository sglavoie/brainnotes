package backup

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Restore stages on the vault's own volume, inside the restore's excluded
// state directory, and never in $TMPDIR; a preview stages nothing at all. Both
// leave no staging behind.
func TestRestoreStagesInsideTheVaultAndPreviewStagesNothing(t *testing.T) {
	vaultRoot := t.TempDir()
	writeFiles(t, vaultRoot, map[string]string{
		"Marker.md":            "before\n",
		"backups/snap.tar.gz":  "",
		".bnotes/recall.json":  "{}\n",
		"Folder/Unrelated.md":  "kept\n",
		"Folder/Overwrite.md":  "old\n",
		"Folder/Another.md":    "another\n",
		"Folder/Deep/Below.md": "below\n",
	})
	writeArchive(t, filepath.Join(vaultRoot, "backups", "snap.tar.gz"), []archiveFixtureEntry{
		{name: "Marker.md", body: "after\n"},
		{name: "Folder/Overwrite.md", body: "new\n"},
	})

	// A temp directory that cannot be created into is what a full or missing
	// tmpfs looks like to os.MkdirTemp("").
	t.Setenv("TMPDIR", filepath.Join(vaultRoot, "no-such-tmp"))
	staging := filepath.Join(vaultRoot, filepath.FromSlash(stageRelDir))

	if _, err := InspectRestore(vaultRoot, "backups/snap.tar.gz", RestoreOptions{}); err != nil {
		t.Fatalf("InspectRestore: %v", err)
	}
	if _, err := os.Stat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an inspection created %s: %v", staging, err)
	}
	preview, err := PreviewRestore(vaultRoot, "backups/snap.tar.gz", RestoreOptions{})
	if err != nil {
		t.Fatalf("PreviewRestore with an unusable TMPDIR: %v", err)
	}
	if len(preview.Overwrite) != 2 || preview.Unchanged != 0 {
		t.Fatalf("preview = %+v, want both archived files as overwrites", preview)
	}
	if _, err := os.Stat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a preview created %s: %v", staging, err)
	}

	if _, err := Restore(vaultRoot, "backups/snap.tar.gz", RestoreOptions{}); err != nil {
		t.Fatalf("Restore with an unusable TMPDIR: %v", err)
	}
	assertFileContent(t, filepath.Join(vaultRoot, "Marker.md"), "after\n")
	assertFileContent(t, filepath.Join(vaultRoot, "Folder", "Overwrite.md"), "new\n")
	if _, err := os.Stat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restore left its staging behind at %s: %v", staging, err)
	}
}

// When an outer restore intent already owns the staging parent, cleanup removes
// only the extraction and leaves the intent's files alone.
func TestRestoreStagingCleanupKeepsTheOuterIntentDirectory(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	writeFiles(t, vaultRoot, map[string]string{
		"Marker.md":                "before\n",
		"backups/snap.tar.gz":      "",
		".bnotes/restore/sentinel": "outer intent\n",
	})
	writeArchive(t, filepath.Join(vaultRoot, "backups", "snap.tar.gz"), []archiveFixtureEntry{
		{name: "Marker.md", body: "after\n"},
	})

	if _, err := Restore(vaultRoot, "backups/snap.tar.gz", RestoreOptions{Wipe: true}); err != nil {
		t.Fatalf("Restore --wipe: %v", err)
	}
	assertFileContent(t, filepath.Join(vaultRoot, "Marker.md"), "after\n")
	assertFileContent(t, filepath.Join(vaultRoot, ".bnotes", "restore", "sentinel"), "outer intent\n")
	entries, err := os.ReadDir(filepath.Join(vaultRoot, ".bnotes", "restore"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("staging left %d entries beside the intent, want only the sentinel", len(entries)-1)
	}
}
