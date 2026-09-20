package backup

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateArchivesVaultExcludingBackupsAndGit(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	writeFiles(t, vaultRoot, map[string]string{
		"Daily.md":                 "# Daily\n",
		"Attachments/image.png":    "png-bytes",
		"Templates/note.md":        "---\ntitle: Template\n---\n",
		".obsidian/workspace.json": "{\"foo\":true}",
		".bnotes/state.json":       "{\"cursor\":1}",
		".git/config":              "[core]\nrepositoryformatversion = 0\n",
		"backups/old.tar.gz":       "previous-backup",
	})

	fixedTime := time.Date(2026, time.May, 28, 23, 24, 25, 123456789, time.UTC)
	result, err := Create(vaultRoot, CreateOptions{
		Now: func() time.Time { return fixedTime },
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	wantArchivePath := filepath.Join(resolvedPath(t, vaultRoot), "backups", "backup-20260528T232425.123456789Z.tar.gz")
	if result.ArchivePath != wantArchivePath {
		t.Fatalf("ArchivePath = %q, want %q", result.ArchivePath, wantArchivePath)
	}

	entries := readArchiveEntries(t, result.ArchivePath)
	assertBackupArchiveContents(t, entries)

	if result.Files != 5 {
		t.Fatalf("Files = %d, want 5", result.Files)
	}

	wantBytes := int64(len("# Daily\n") + len("png-bytes") + len("---\ntitle: Template\n---\n") + len("{\"foo\":true}") + len("{\"cursor\":1}"))
	if result.Bytes != wantBytes {
		t.Fatalf("Bytes = %d, want %d", result.Bytes, wantBytes)
	}
}

func assertBackupArchiveContents(t *testing.T, entries map[string]tar.Header) {
	t.Helper()
	for _, name := range []string{
		"Daily.md", "Attachments/image.png", "Templates/note.md",
		".obsidian/workspace.json", ".bnotes/state.json",
	} {
		if _, ok := entries[name]; !ok {
			t.Errorf("archive missing %s: %#v", name, entries)
		}
	}
	for _, name := range []string{".git/config", "backups/old.tar.gz", ".bnotes/checkpoint.json"} {
		if _, ok := entries[name]; ok {
			t.Errorf("archive should exclude %s: %#v", name, entries)
		}
	}
	for name := range entries {
		if strings.HasPrefix(name, ".bnotes/locks/") {
			t.Errorf("archive should exclude machine-local lock %s: %#v", name, entries)
		}
	}
}

func TestRestoreOverlayOverwritesAndLeavesUnrelatedFiles(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	writeFiles(t, vaultRoot, map[string]string{
		"Daily.md":               "old body\n",
		"Unrelated.md":           "keep me\n",
		"backups/overlay.tar.gz": "",
	})

	archivePath := filepath.Join(vaultRoot, "backups", "overlay.tar.gz")
	writeArchive(t, archivePath, []archiveFixtureEntry{
		{name: "Daily.md", body: "new body\n"},
		{name: "Folder/Nested.md", body: "nested\n"},
	})

	result, err := Restore(vaultRoot, "backups/overlay.tar.gz", RestoreOptions{})
	if err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}

	if result.Mode != "overlay" {
		t.Fatalf("Mode = %q, want overlay", result.Mode)
	}
	if result.Files != 2 {
		t.Fatalf("Files = %d, want 2", result.Files)
	}
	if result.ArchivePath != resolvedPath(t, archivePath) {
		t.Fatalf("ArchivePath = %q, want %q", result.ArchivePath, archivePath)
	}

	assertFileContent(t, filepath.Join(vaultRoot, "Daily.md"), "new body\n")
	assertFileContent(t, filepath.Join(vaultRoot, "Folder", "Nested.md"), "nested\n")
	assertFileContent(t, filepath.Join(vaultRoot, "Unrelated.md"), "keep me\n")
}

// A wipe removes what an archive can put back and nothing else. `backups/` and
// `.git/` are excluded from every archive, so removing either would destroy the
// only copy of something — the sibling backups, or the vault's whole content
// history — with no archive anywhere able to restore it.
func TestRestoreWipeRemovesOtherFilesAndPreservesBackupsAndGit(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	writeFiles(t, vaultRoot, map[string]string{
		"Daily.md":                "old\n",
		".git/config":             "git data\n",
		"Scratch/keep.txt":        "wipe me\n",
		".bnotes/locks/live.lock": "held inode\n",
		".bnotes/checkpoint.json": "live announcement\n",
		".bnotes/state.json":      "wipe sidecar\n",
		"backups/existing.tar.gz": "existing-backup",
		"backups/wipe.tar.gz":     "",
	})

	archivePath := filepath.Join(vaultRoot, "backups", "wipe.tar.gz")
	writeArchive(t, archivePath, []archiveFixtureEntry{
		{name: "Daily.md", body: "restored\n"},
		{name: ".obsidian/workspace.json", body: "{}"},
	})

	result, err := Restore(vaultRoot, "backups/wipe.tar.gz", RestoreOptions{Wipe: true})
	if err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}

	if result.Mode != "wipe" {
		t.Fatalf("Mode = %q, want wipe", result.Mode)
	}
	if result.Files != 2 {
		t.Fatalf("Files = %d, want 2", result.Files)
	}

	assertFileContent(t, filepath.Join(vaultRoot, "Daily.md"), "restored\n")
	assertFileContent(t, filepath.Join(vaultRoot, ".obsidian", "workspace.json"), "{}")
	assertFileContent(t, filepath.Join(vaultRoot, "backups", "existing.tar.gz"), "existing-backup")

	if _, err := os.Stat(filepath.Join(vaultRoot, "Scratch", "keep.txt")); !os.IsNotExist(err) {
		t.Fatalf("Scratch/keep.txt should be removed, stat err = %v", err)
	}
	assertFileContent(t, filepath.Join(vaultRoot, ".git", "config"), "git data\n")
	assertFileContent(t, filepath.Join(vaultRoot, ".bnotes", "locks", "live.lock"), "held inode\n")
	assertFileContent(t, filepath.Join(vaultRoot, ".bnotes", "checkpoint.json"), "live announcement\n")
	if _, err := os.Stat(filepath.Join(vaultRoot, ".bnotes", "state.json")); !os.IsNotExist(err) {
		t.Fatalf("portable sidecar should be removed during wipe, stat err = %v", err)
	}
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("source archive in backups should remain, stat err = %v", err)
	}
}

func TestRestoreRejectsUnsafeArchiveEntries(t *testing.T) {
	t.Parallel()

	for _, test := range unsafeArchiveCases() {
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

type archiveFixtureEntry struct {
	name     string
	body     string
	kind     byte
	linkname string
	mode     int64
	modeSet  bool
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()

	for relativePath, contents := range files {
		fullPath := filepath.Join(root, filepath.FromSlash(relativePath))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", fullPath, err)
		}
		if err := os.WriteFile(fullPath, []byte(contents), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", fullPath, err)
		}
	}
}

func writeArchive(t *testing.T, archivePath string, entries []archiveFixtureEntry) {
	t.Helper()

	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("Create(%q): %v", archivePath, err)
	}

	gzw := gzip.NewWriter(file)
	tw := tar.NewWriter(gzw)

	for _, entry := range entries {
		writeArchiveFixtureEntry(t, tw, entry)
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("tw.Close(): %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("gzw.Close(): %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("file.Close(): %v", err)
	}
}

func writeArchiveFixtureEntry(t *testing.T, tw *tar.Writer, entry archiveFixtureEntry) {
	kind := entry.kind
	if kind == 0 {
		kind = tar.TypeReg
	}
	header := &tar.Header{Name: entry.name, Typeflag: kind, Mode: fixtureMode(entry), Linkname: entry.linkname}
	if kind == tar.TypeReg || kind == tar.TypeRegA {
		header.Size = int64(len(entry.body))
	}
	if err := tw.WriteHeader(header); err != nil {
		t.Fatalf("WriteHeader(%q): %v", entry.name, err)
	}
	if kind == tar.TypeReg || kind == tar.TypeRegA {
		if _, err := io.WriteString(tw, entry.body); err != nil {
			t.Fatalf("Write(%q): %v", entry.name, err)
		}
	}
}

func fixtureMode(entry archiveFixtureEntry) int64 {
	if entry.modeSet || entry.mode != 0 {
		return entry.mode
	}
	if entry.kind == tar.TypeDir {
		return 0o755
	}
	return 0o644
}

func readArchiveEntries(t *testing.T, archivePath string) map[string]tar.Header {
	t.Helper()

	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("Open(%q): %v", archivePath, err)
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("gzip.NewReader(%q): %v", archivePath, err)
	}
	defer gzr.Close()

	entries := make(map[string]tar.Header)
	reader := tar.NewReader(gzr)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return entries
		}
		if err != nil {
			t.Fatalf("reader.Next(): %v", err)
		}

		entries[header.Name] = *header
	}
}

func assertFileContent(t *testing.T, path string, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, string(data), want)
	}
}

func resolvedPath(t *testing.T, path string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}

	return resolved
}
