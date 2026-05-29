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
	if _, ok := entries["Daily.md"]; !ok {
		t.Fatalf("archive missing note: %#v", entries)
	}
	if _, ok := entries["Attachments/image.png"]; !ok {
		t.Fatalf("archive missing attachment: %#v", entries)
	}
	if _, ok := entries["Templates/note.md"]; !ok {
		t.Fatalf("archive missing template: %#v", entries)
	}
	if _, ok := entries[".obsidian/workspace.json"]; !ok {
		t.Fatalf("archive missing .obsidian data: %#v", entries)
	}
	if _, ok := entries[".bnotes/state.json"]; !ok {
		t.Fatalf("archive missing .bnotes data: %#v", entries)
	}
	if _, ok := entries[".git/config"]; ok {
		t.Fatalf("archive should exclude .git contents: %#v", entries)
	}
	if _, ok := entries["backups/old.tar.gz"]; ok {
		t.Fatalf("archive should exclude backups contents: %#v", entries)
	}

	if result.Files != 5 {
		t.Fatalf("Files = %d, want 5", result.Files)
	}

	wantBytes := int64(len("# Daily\n") + len("png-bytes") + len("---\ntitle: Template\n---\n") + len("{\"foo\":true}") + len("{\"cursor\":1}"))
	if result.Bytes != wantBytes {
		t.Fatalf("Bytes = %d, want %d", result.Bytes, wantBytes)
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

func TestRestoreWipeRemovesOtherFilesAndPreservesBackups(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	writeFiles(t, vaultRoot, map[string]string{
		"Daily.md":                "old\n",
		".git/config":             "git data\n",
		"Scratch/keep.txt":        "wipe me\n",
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
	if _, err := os.Stat(filepath.Join(vaultRoot, ".git")); !os.IsNotExist(err) {
		t.Fatalf(".git should be removed by wipe, stat err = %v", err)
	}
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("source archive in backups should remain, stat err = %v", err)
	}
}

func TestRestoreRejectsUnsafeArchiveEntries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		entries []archiveFixtureEntry
		wantErr string
	}{
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
			wantErr: "targets backups/",
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

	for _, test := range tests {
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

type archiveFixtureEntry struct {
	name     string
	body     string
	kind     byte
	linkname string
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
		kind := entry.kind
		if kind == 0 {
			kind = tar.TypeReg
		}

		header := &tar.Header{
			Name:     entry.name,
			Typeflag: kind,
			Mode:     0o644,
			Linkname: entry.linkname,
		}
		if kind == tar.TypeDir {
			header.Mode = 0o755
		}
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
