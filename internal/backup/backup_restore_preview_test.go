package backup

import (
	"archive/tar"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// An overlay preview sorts every archived file into exactly one of Create,
// Overwrite or Unchanged, ignores directory entries, lists nothing to remove,
// and leaves the vault as it found it.
func TestPreviewRestoreOverlayClassifiesArchivedFiles(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	writeFiles(t, vaultRoot, map[string]string{
		"Same.md":                "same\n",
		"Changed.md":             "old\n",
		"Folder/Changed.md":      "old nested\n",
		"Unrelated.md":           "keep me\n",
		"backups/preview.tar.gz": "",
	})
	archivePath := filepath.Join(vaultRoot, "backups", "preview.tar.gz")
	writeArchive(t, archivePath, []archiveFixtureEntry{
		{name: "Same.md", body: "same\n"},
		{name: "Changed.md", body: "new\n"},
		{name: "Folder/Changed.md", body: "new nested\n"},
		{name: "Zeta/Created.md", body: "created\n"},
		{name: "Alpha.md", body: "created too\n"},
		{name: "Empty/", kind: tar.TypeDir},
	})

	preview, err := PreviewRestore(vaultRoot, "backups/preview.tar.gz", RestoreOptions{})
	if err != nil {
		t.Fatalf("PreviewRestore: %v", err)
	}

	want := &RestorePreview{
		ArchivePath: resolvedPath(t, archivePath),
		Mode:        "overlay",
		Create:      []string{"Alpha.md", "Zeta/Created.md"},
		Overwrite:   []string{"Changed.md", "Folder/Changed.md"},
		Unchanged:   1,
	}
	if !reflect.DeepEqual(preview, want) {
		t.Fatalf("PreviewRestore =\n  %#v\nwant\n  %#v", preview, want)
	}

	assertFileContent(t, filepath.Join(vaultRoot, "Changed.md"), "old\n")
	for _, absent := range []string{"Alpha.md", "Zeta", "Empty"} {
		if _, err := os.Lstat(filepath.Join(vaultRoot, absent)); !os.IsNotExist(err) {
			t.Fatalf("preview created %s (stat err = %v)", absent, err)
		}
	}
}

// A wipe preview lists what the wipe deletes with the same exclusions the wipe
// applies: backups/, .git/ and machine-local .bnotes state stay; .bnotes itself
// is kept and only its other contents are listed; a directory with nothing the
// archive wants is one line, and one the archive writes into is listed by its
// leftover contents. A destination of another shape counts as an overwrite.
func TestPreviewRestoreWipeListsRemovalsAndClassifiesShapes(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	writeFiles(t, vaultRoot, map[string]string{
		"Same.md":                 "same\n",
		"Loose.md":                "wipe me\n",
		"Scratch/a.txt":           "a\n",
		"Scratch/deep/b.txt":      "b\n",
		"Folder/Leftover.md":      "wipe me too\n",
		"Target.md":               "symlink target\n",
		".git/config":             "git data\n",
		".bnotes/locks/live.lock": "held inode\n",
		".bnotes/state.json":      "portable sidecar\n",
		"backups/existing.tar.gz": "existing-backup",
		"backups/wipe.tar.gz":     "",
	})
	if err := os.Symlink("Target.md", filepath.Join(vaultRoot, "Link.md")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	archivePath := filepath.Join(vaultRoot, "backups", "wipe.tar.gz")
	writeArchive(t, archivePath, []archiveFixtureEntry{
		{name: "Same.md", body: "same\n"},
		{name: "Link.md", body: "a real file now\n"},
		{name: "Target.md", body: "symlink target\n"},
		{name: "Folder/Archived.md", body: "archived\n"},
	})

	preview, err := PreviewRestore(vaultRoot, "backups/wipe.tar.gz", RestoreOptions{Wipe: true})
	if err != nil {
		t.Fatalf("PreviewRestore: %v", err)
	}

	want := &RestorePreview{
		ArchivePath: resolvedPath(t, archivePath),
		Mode:        "wipe",
		Create:      []string{"Folder/Archived.md"},
		Overwrite:   []string{"Link.md"},
		Unchanged:   2,
		Remove:      []string{".bnotes/state.json", "Folder/Leftover.md", "Loose.md", "Scratch/"},
	}
	if !reflect.DeepEqual(preview, want) {
		t.Fatalf("PreviewRestore =\n  %#v\nwant\n  %#v", preview, want)
	}

	assertFileContent(t, filepath.Join(vaultRoot, "Loose.md"), "wipe me\n")
	assertFileContent(t, filepath.Join(vaultRoot, "Scratch", "deep", "b.txt"), "b\n")
	if info, err := os.Lstat(filepath.Join(vaultRoot, "Link.md")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("preview replaced Link.md (mode %v, err %v)", info.Mode(), err)
	}
}

// previewWipe on its own: nothing desired means everything outside the
// exclusions goes, and an empty vault has nothing to remove.
func TestPreviewWipeWithNothingDesired(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	empty, err := previewWipe(vaultRoot, map[string]struct{}{})
	if err != nil {
		t.Fatalf("previewWipe(empty vault): %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("previewWipe(empty vault) = %v, want nothing", empty)
	}

	writeFiles(t, vaultRoot, map[string]string{
		"b.md":                     "b\n",
		"A/a.md":                   "a\n",
		".obsidian/workspace.json": "{}",
		".git/HEAD":                "ref\n",
		"BACKUPS/old.tar.gz":       "case-folded backups dir\n",
	})
	got, err := previewWipe(vaultRoot, map[string]struct{}{})
	if err != nil {
		t.Fatalf("previewWipe: %v", err)
	}
	wantRemove := []string{".obsidian/", "A/", "b.md"}
	if !reflect.DeepEqual(got, wantRemove) {
		t.Fatalf("previewWipe = %v, want %v", got, wantRemove)
	}
}
