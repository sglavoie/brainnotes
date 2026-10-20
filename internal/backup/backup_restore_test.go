package backup

import (
	"archive/tar"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRestoreRejectsUnsafeArchiveEntries(t *testing.T) {
	t.Parallel()

	for _, test := range append(unsafeArchiveCases(), normalizationConflictCases()...) {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			vaultRoot := t.TempDir()
			writeFiles(t, vaultRoot, map[string]string{
				"Marker.md":             "unchanged\n",
				"backups/unsafe.tar.gz": "",
			})

			archivePath := filepath.Join(vaultRoot, "backups", "unsafe.tar.gz")
			writeArchive(t, archivePath, test.entries)

			_, err := Restore(vaultRoot, "backups/unsafe.tar.gz", RestoreOptions{})
			if err == nil {
				t.Fatalf("Restore returned nil error")
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Restore error = %q, want substring %q", err, test.wantErr)
			}

			assertFileContent(t, filepath.Join(vaultRoot, "Marker.md"), "unchanged\n")
		})
	}
}

type unsafeArchiveCase struct {
	name    string
	entries []archiveFixtureEntry
	wantErr string
}

func unsafeArchiveCases() []unsafeArchiveCase {
	return []unsafeArchiveCase{
		{
			name: "absolute path",
			entries: []archiveFixtureEntry{
				{name: "/tmp/evil.md", body: "nope"},
			},
			wantErr: "absolute",
		},
		{
			name: "path traversal",
			entries: []archiveFixtureEntry{
				{name: "../evil.md", body: "nope"},
			},
			wantErr: "escapes the vault",
		},
		{
			name: "backup target",
			entries: []archiveFixtureEntry{
				{name: "backups/evil.md", body: "nope"},
			},
			wantErr: "reserved machine-local state",
		},
		{
			name: "git target",
			entries: []archiveFixtureEntry{
				{name: ".Git/config", body: "nope"},
			},
			wantErr: "reserved machine-local state",
		},
		{
			name: "checkpoint lock target",
			entries: []archiveFixtureEntry{
				{name: ".bnotes/LOCKS/checkpoint.lock", body: "nope"},
			},
			wantErr: "reserved machine-local state",
		},
		{
			name: "checkpoint record target",
			entries: []archiveFixtureEntry{
				{name: ".bnotes/CHECKPOINT.json", body: "nope"},
			},
			wantErr: "reserved machine-local state",
		},
		{
			name: "duplicate entry",
			entries: []archiveFixtureEntry{
				{name: "Daily.md", body: "one"},
				{name: "Daily.md", body: "two"},
			},
			wantErr: "duplicate entry",
		},
		{
			name: "file over earlier descendant",
			entries: []archiveFixtureEntry{
				{name: "Notes/Inbox/Daily.md", body: "one"},
				{name: "Notes", body: "two"},
			},
			wantErr: `conflicts with descendant "notes/inbox/daily.md"`,
		},
		{
			name: "descendant under earlier file",
			entries: []archiveFixtureEntry{
				{name: "Notes", body: "one"},
				{name: "Notes/Daily.md", body: "two"},
			},
			wantErr: `conflicts with file "notes"`,
		},
		{
			name: "symlink entry",
			entries: []archiveFixtureEntry{
				{name: "Daily.md", kind: tar.TypeSymlink, linkname: "Other.md"},
			},
			wantErr: "symlink",
		},
		{
			name: "hardlink entry",
			entries: []archiveFixtureEntry{
				{name: "Daily.md", kind: tar.TypeLink, linkname: "Other.md"},
			},
			wantErr: "hardlink",
		},
		{
			name: "special entry",
			entries: []archiveFixtureEntry{
				{name: "Daily.md", kind: tar.TypeChar},
			},
			wantErr: "unsupported type",
		},
	}
}

func TestRestoreOverlayRejectsDestinationConflictsBeforeWriting(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	writeFiles(t, vaultRoot, map[string]string{
		"Folder":                  "not a directory\n",
		"Marker.md":               "unchanged\n",
		"backups/conflict.tar.gz": "",
	})

	archivePath := filepath.Join(vaultRoot, "backups", "conflict.tar.gz")
	writeArchive(t, archivePath, []archiveFixtureEntry{
		{name: "Folder/Nested.md", body: "new content\n"},
	})

	_, err := Restore(vaultRoot, "backups/conflict.tar.gz", RestoreOptions{})
	if err == nil {
		t.Fatalf("Restore returned nil error")
	}
	if !strings.Contains(err.Error(), "destination parent") {
		t.Fatalf("Restore error = %q, want destination parent conflict", err)
	}

	assertFileContent(t, filepath.Join(vaultRoot, "Folder"), "not a directory\n")
	assertFileContent(t, filepath.Join(vaultRoot, "Marker.md"), "unchanged\n")
	if _, err := os.Stat(filepath.Join(vaultRoot, "Folder", "Nested.md")); err == nil || !strings.Contains(err.Error(), "not a directory") && !os.IsNotExist(err) {
		t.Fatalf("Folder/Nested.md should not be created, stat err = %v", err)
	}
}

func TestRestoreWipeReplacesConflictingShapesAndKeepsArchivedEmptyDirectories(t *testing.T) {
	vaultRoot := t.TempDir()
	writeFiles(t, vaultRoot, map[string]string{
		"AsDirectory/old.md": "old\n",
		"AsFile":             "blocks directory\n",
		"Deep":               "blocks several directories\n",
	})
	if err := os.MkdirAll(filepath.Join(vaultRoot, "Restricted", "stale-empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, vaultRoot, map[string]string{
		"Restricted/stale-locked/child.md": "stale\n",
	})
	if err := os.Chmod(filepath.Join(vaultRoot, "Restricted", "stale-locked"), 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(vaultRoot, "Restricted"), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Join(vaultRoot, "Restricted"), 0o700)
		_ = os.Chmod(filepath.Join(vaultRoot, "Restricted", "stale-locked"), 0o700)
	})
	if err := os.Symlink("AsFile", filepath.Join(vaultRoot, "AsSymlink")); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(vaultRoot, "backups", "shapes.tar.gz")
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeArchive(t, archivePath, []archiveFixtureEntry{
		{name: "AsDirectory", body: "now a file\n"},
		{name: "AsFile/child.md", body: "nested\n"},
		{name: "AsSymlink", body: "regular\n"},
		{name: "Deep/A/B/C.md", body: "deep\n"},
		{name: "Empty/", kind: tar.TypeDir},
		{name: "Restricted/", kind: tar.TypeDir, mode: 0o500},
		{name: "Restricted/keep.md", body: "kept\n"},
		{name: "LockedZero/", kind: tar.TypeDir, modeSet: true, mode: 0o000},
		{name: "LockedZero/inside.md", body: "zero mode\n"},
		{name: "SearchOnly/", kind: tar.TypeDir, mode: 0o100},
		{name: "SearchOnly/inside.md", body: "search only\n"},
	})
	if _, err := Restore(vaultRoot, archivePath, RestoreOptions{Wipe: true}); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	assertRestoredConflictingShapes(t, vaultRoot)
}

func assertRestoredConflictingShapes(t *testing.T, vaultRoot string) {
	t.Helper()
	assertFileContent(t, filepath.Join(vaultRoot, "AsDirectory"), "now a file\n")
	assertFileContent(t, filepath.Join(vaultRoot, "AsFile", "child.md"), "nested\n")
	assertFileContent(t, filepath.Join(vaultRoot, "AsSymlink"), "regular\n")
	assertFileContent(t, filepath.Join(vaultRoot, "Deep", "A", "B", "C.md"), "deep\n")
	assertFileContent(t, filepath.Join(vaultRoot, "Restricted", "keep.md"), "kept\n")
	if _, err := os.Stat(filepath.Join(vaultRoot, "Restricted", "stale-empty")); !os.IsNotExist(err) {
		t.Fatalf("stale restricted child survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(vaultRoot, "Restricted", "stale-locked")); !os.IsNotExist(err) {
		t.Fatalf("stale mode-000 subtree survived: %v", err)
	}
	if info, err := os.Stat(filepath.Join(vaultRoot, "Empty")); err != nil || !info.IsDir() {
		t.Fatalf("archived empty directory was not preserved: info=%v err=%v", info, err)
	}
	assertRestrictiveRestoredDir(t, vaultRoot, "LockedZero", 0o000, "zero mode\n")
	assertRestrictiveRestoredDir(t, vaultRoot, "SearchOnly", 0o100, "search only\n")
}

func assertRestrictiveRestoredDir(t *testing.T, root, name string, mode os.FileMode, body string) {
	t.Helper()
	path := filepath.Join(root, name)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat restrictive directory %s: %v", name, err)
	}
	if got := info.Mode().Perm(); got != mode {
		t.Fatalf("%s mode = %o, want %o", name, got, mode)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("reopen restrictive directory %s: %v", name, err)
	}
	assertFileContent(t, filepath.Join(path, "inside.md"), body)
}

func TestRestoreKeepsBnotesContainerOwnerAccessible(t *testing.T) {
	vaultRoot := t.TempDir()
	archivePath := filepath.Join(vaultRoot, "backups", "sidecar.tar.gz")
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeArchive(t, archivePath, []archiveFixtureEntry{{name: ".bnotes/", kind: tar.TypeDir, mode: 0o400}})
	if _, err := Restore(vaultRoot, archivePath, RestoreOptions{}); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	info, err := os.Stat(filepath.Join(vaultRoot, ".bnotes"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o700 != 0o700 {
		t.Fatalf(".bnotes mode = %o, want owner rwx", info.Mode().Perm())
	}
}

// normalizationConflictCases are archives whose entries name one file on a
// case-insensitive, normalization-insensitive filesystem such as APFS, spelled
// with composed and decomposed accents.
func normalizationConflictCases() []unsafeArchiveCase {
	return []unsafeArchiveCase{
		{
			name: "duplicate entry differing only in normalization",
			entries: []archiveFixtureEntry{
				{name: "Caf\u00e9.md", body: "composed"},
				{name: "Cafe\u0301.md", body: "decomposed"},
			},
			wantErr: "duplicate entry",
		},
		{
			name: "duplicate entry differing in normalization and case",
			entries: []archiveFixtureEntry{
				{name: "caf\u00e9.md", body: "composed"},
				{name: "CAFE\u0301.MD", body: "decomposed"},
			},
			wantErr: "duplicate entry",
		},
		{
			name: "file conflicts with decomposed descendant",
			entries: []archiveFixtureEntry{
				{name: "Cafe\u0301/Note.md", body: "child"},
				{name: "Caf\u00e9", body: "file"},
			},
			wantErr: "conflicts with descendant",
		},
		{
			name: "decomposed path conflicts with composed file prefix",
			entries: []archiveFixtureEntry{
				{name: "Caf\u00e9", body: "file"},
				{name: "Cafe\u0301/Note.md", body: "child"},
			},
			wantErr: "conflicts with file",
		},
	}
}
