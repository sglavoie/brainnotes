package cli

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackupCommandCreatesArchiveAndReportsCounts(t *testing.T) {
	root := buildBackupFixture(t, map[string]string{
		"Daily.md":                "# Daily\n",
		"Folder/Nested.md":        "nested\n",
		".obsidian/workspace":     "workspace\n",
		".bnotes/state.json":      "{\"cursor\":1}\n",
		".git/config":             "[core]\n",
		"backups/existing.tar.gz": "previous-backup\n",
	})

	out, err := runBackupCLI(t, "backup")
	if err != nil {
		t.Fatalf("backup: %v\n%s", err, out)
	}

	archivePath := filepath.Join(resolveTestPath(t, root), "backups", "backup-20260528T232425.123456789Z.tar.gz")
	want := "backup: created " + archivePath + " (files=5 bytes=38)\n"
	if out != want {
		t.Fatalf("backup output = %q, want %q", out, want)
	}

	entries := readArchiveEntries(t, archivePath)
	for _, relPath := range []string{
		"Daily.md",
		"Folder/Nested.md",
		".obsidian/workspace",
		".bnotes/state.json",
	} {
		if _, ok := entries[relPath]; !ok {
			t.Fatalf("archive missing %q: %#v", relPath, entries)
		}
	}
	if _, ok := entries[".git/config"]; ok {
		t.Fatalf("archive should exclude .git/config: %#v", entries)
	}
	if _, ok := entries["backups/existing.tar.gz"]; ok {
		t.Fatalf("archive should exclude backups/existing.tar.gz: %#v", entries)
	}
}

func TestBackupCommandReportsSkippedSymlinks(t *testing.T) {
	root := buildBackupFixture(t, map[string]string{
		"Daily.md": "body\n",
	})

	if err := os.Symlink("Daily.md", filepath.Join(root, "Daily.link")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	out, err := runBackupCLI(t, "backup")
	if err != nil {
		t.Fatalf("backup with symlink: %v\n%s", err, out)
	}

	archivePath := filepath.Join(resolveTestPath(t, root), "backups", "backup-20260528T232425.123456789Z.tar.gz")
	want := "backup: created " + archivePath + " (files=2 bytes=5 skipped_symlinks=1)\n"
	if out != want {
		t.Fatalf("backup output = %q, want %q", out, want)
	}
}

func TestRestoreCommandOverlayUsesVaultRelativeArchivePath(t *testing.T) {
	root := buildBackupFixture(t, map[string]string{
		"Daily.md":     "old body\n",
		"Unrelated.md": "keep me\n",
	})
	archivePath := filepath.Join(root, "backups", "overlay.tar.gz")
	writeBackupArchive(t, archivePath, []archiveFixtureEntry{
		{name: "Daily.md", body: "new body\n"},
		{name: "Folder/Nested.md", body: "nested\n"},
	})

	out, err := runCLI(t, "", "restore", "backups/overlay.tar.gz")
	if err != nil {
		t.Fatalf("restore overlay: %v\n%s", err, out)
	}

	want := "restore: restored " + filepath.Join(resolveTestPath(t, root), "backups", "overlay.tar.gz") + " (mode=overlay files=2)\n"
	if out != want {
		t.Fatalf("restore output = %q, want %q", out, want)
	}
	assertBackupFileContent(t, filepath.Join(root, "Daily.md"), "new body\n")
	assertBackupFileContent(t, filepath.Join(root, "Folder", "Nested.md"), "nested\n")
	assertBackupFileContent(t, filepath.Join(root, "Unrelated.md"), "keep me\n")
}

func TestRestoreCommandAcceptsAbsoluteArchivePath(t *testing.T) {
	root := buildBackupFixture(t, map[string]string{
		"Daily.md": "old body\n",
	})
	externalDir := t.TempDir()
	archivePath := filepath.Join(externalDir, "absolute.tar.gz")
	writeBackupArchive(t, archivePath, []archiveFixtureEntry{
		{name: "Daily.md", body: "absolute restore\n"},
		{name: "Folder/Nested.md", body: "nested\n"},
	})

	out, err := runCLI(t, "", "restore", archivePath)
	if err != nil {
		t.Fatalf("restore absolute path: %v\n%s", err, out)
	}

	want := "restore: restored " + archivePath + " (mode=overlay files=2)\n"
	if out != want {
		t.Fatalf("restore output = %q, want %q", out, want)
	}
	assertBackupFileContent(t, filepath.Join(root, "Daily.md"), "absolute restore\n")
	assertBackupFileContent(t, filepath.Join(root, "Folder", "Nested.md"), "nested\n")
}

// This fixture is deliberately not a Git repository. A vault whose `.git` is
// real is refused or snapshotted by the safety engine, and what that does to a
// wipe is proved against an actual repository in internal/mutationmatrix; here
// the subject is only which entries the command removes, so a hidden directory
// stands in for the general case.
func TestRestoreCommandWipePreservesBackupsDirectory(t *testing.T) {
	root := buildBackupFixture(t, map[string]string{
		"Daily.md":                "old\n",
		".stale/leftover.json":    "{}",
		"Scratch/keep.txt":        "wipe me\n",
		"backups/existing.tar.gz": "existing-backup\n",
	})
	archivePath := filepath.Join(root, "backups", "wipe.tar.gz")
	writeBackupArchive(t, archivePath, []archiveFixtureEntry{
		{name: "Daily.md", body: "restored\n"},
		{name: ".obsidian/workspace.json", body: "{}"},
	})

	out, err := runCLI(t, "", "restore", "backups/wipe.tar.gz", "--wipe", "--yes")
	if err != nil {
		t.Fatalf("restore wipe: %v\n%s", err, out)
	}

	want := "restore: restored " + filepath.Join(resolveTestPath(t, root), "backups", "wipe.tar.gz") + " (mode=wipe files=2)\n"
	if out != want {
		t.Fatalf("restore output = %q, want %q", out, want)
	}
	assertBackupFileContent(t, filepath.Join(root, "Daily.md"), "restored\n")
	assertBackupFileContent(t, filepath.Join(root, ".obsidian", "workspace.json"), "{}")
	assertBackupFileContent(t, filepath.Join(root, "backups", "existing.tar.gz"), "existing-backup\n")
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("restore source archive missing after wipe: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Scratch", "keep.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Scratch/keep.txt should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".stale")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".stale should be removed by wipe, stat err = %v", err)
	}
}

func TestRestoreCommandRejectsCorruptArchiveBeforeWriting(t *testing.T) {
	root := buildBackupFixture(t, map[string]string{
		"Marker.md": "unchanged\n",
	})
	archivePath := filepath.Join(root, "backups", "corrupt.tar.gz")
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		t.Fatalf("mkdir backups: %v", err)
	}
	if err := os.WriteFile(archivePath, []byte("not a gzip stream"), 0o644); err != nil {
		t.Fatalf("write corrupt archive: %v", err)
	}

	out, err := runCLI(t, "", "restore", "backups/corrupt.tar.gz")
	if err == nil {
		t.Fatalf("restore corrupt archive should fail\n%s", out)
	}
	if !strings.Contains(err.Error(), "open gzip stream") {
		t.Fatalf("restore error = %q, want gzip failure", err)
	}
	assertBackupFileContent(t, filepath.Join(root, "Marker.md"), "unchanged\n")
}

func TestRestoreCommandRejectsOverlayConflictsBeforeWriting(t *testing.T) {
	root := buildBackupFixture(t, map[string]string{
		"Folder":    "not a directory\n",
		"Marker.md": "unchanged\n",
	})
	archivePath := filepath.Join(root, "backups", "conflict.tar.gz")
	writeBackupArchive(t, archivePath, []archiveFixtureEntry{
		{name: "Folder/Nested.md", body: "new content\n"},
	})

	out, err := runCLI(t, "", "restore", "backups/conflict.tar.gz")
	if err == nil {
		t.Fatalf("restore conflict should fail\n%s", out)
	}
	if !strings.Contains(err.Error(), "destination parent") {
		t.Fatalf("restore error = %q, want destination conflict", err)
	}
	assertBackupFileContent(t, filepath.Join(root, "Folder"), "not a directory\n")
	assertBackupFileContent(t, filepath.Join(root, "Marker.md"), "unchanged\n")
	if _, err := os.Stat(filepath.Join(root, "Folder", "Nested.md")); err == nil || (!errors.Is(err, os.ErrNotExist) && !strings.Contains(err.Error(), "not a directory")) {
		t.Fatalf("Folder/Nested.md should not be created, stat err = %v", err)
	}
}

type archiveFixtureEntry struct {
	name string
	body string
	kind byte
}

func buildBackupFixture(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()
	for relPath, body := range files {
		fullPath := filepath.Join(root, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", filepath.Dir(fullPath), err)
		}
		if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
			t.Fatalf("write %q: %v", fullPath, err)
		}
	}

	// The vault-root marker file lives inside the vault, so it is one extra
	// (empty) file in every snapshot taken from this fixture.
	useVaultRoot(t, root, "")
	return root
}

func runBackupCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()

	prev := nowForBackup
	nowForBackup = func() time.Time {
		return time.Date(2026, time.May, 28, 23, 24, 25, 123456789, time.UTC)
	}
	t.Cleanup(func() { nowForBackup = prev })

	return runCLI(t, "", args...)
}

func writeBackupArchive(t *testing.T, archivePath string, entries []archiveFixtureEntry) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		t.Fatalf("mkdir backups dir: %v", err)
	}

	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("create archive: %v", err)
	}
	defer file.Close()

	gzw := gzip.NewWriter(file)
	tw := tar.NewWriter(gzw)

	for _, entry := range entries {
		writeBackupArchiveEntry(t, tw, entry)
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
}

func writeBackupArchiveEntry(t *testing.T, tw *tar.Writer, entry archiveFixtureEntry) {
	t.Helper()
	kind := entry.kind
	if kind == 0 {
		kind = tar.TypeReg
	}
	header := &tar.Header{
		Name:     entry.name,
		Typeflag: kind,
		Mode:     0o644,
		Size:     int64(len(entry.body)),
	}
	if kind == tar.TypeDir {
		header.Mode = 0o755
		header.Size = 0
	}
	if err := tw.WriteHeader(header); err != nil {
		t.Fatalf("write header %q: %v", entry.name, err)
	}
	if kind == tar.TypeReg {
		if _, err := io.WriteString(tw, entry.body); err != nil {
			t.Fatalf("write body %q: %v", entry.name, err)
		}
	}
}

func readArchiveEntries(t *testing.T, archivePath string) map[string]struct{} {
	t.Helper()

	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("open gzip stream: %v", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	entries := make(map[string]struct{})
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return entries
		}
		if err != nil {
			t.Fatalf("read archive entry: %v", err)
		}
		entries[header.Name] = struct{}{}
	}
}

func assertBackupFileContent(t *testing.T, path, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, string(data), want)
	}
}

func resolveTestPath(t *testing.T, path string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve path %q: %v", path, err)
	}
	return resolved
}

// A wipe cannot be undone with `bn undo`, so without --yes and without a
// terminal to ask on it is refused before anything is touched, and --dry-run
// lists exactly what it would do.
func TestRestoreCommandWipeRequiresConfirmationAndPreviews(t *testing.T) {
	root := buildBackupFixture(t, map[string]string{
		"Daily.md":         "old\n",
		"Same.md":          "same\n",
		"Scratch/keep.txt": "wipe me\n",
	})
	writeBackupArchive(t, filepath.Join(root, "backups", "wipe.tar.gz"), []archiveFixtureEntry{
		{name: "Daily.md", body: "restored\n"},
		{name: "Same.md", body: "same\n"},
		{name: "New.md", body: "new\n"},
	})

	out, err := runCLI(t, "", "restore", "backups/wipe.tar.gz", "--wipe")
	if err == nil || !strings.Contains(err.Error(), "pass --yes to confirm") {
		t.Fatalf("err = %v, want the --yes refusal\n%s", err, out)
	}
	assertBackupFileContent(t, filepath.Join(root, "Daily.md"), "old\n")
	assertBackupFileContent(t, filepath.Join(root, "Scratch", "keep.txt"), "wipe me\n")

	out, err = runCLI(t, "", "restore", "backups/wipe.tar.gz", "--wipe", "--dry-run")
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	for _, want := range []string{
		"create     New.md\n",
		"overwrite  Daily.md\n",
		"remove     Scratch/\n",
		// The fixture's own configuration is not in the archive either.
		"remove     brainnotes.toml\n",
		"summary: create=1 overwrite=1 unchanged=1 remove=2\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out)
		}
	}
	assertBackupFileContent(t, filepath.Join(root, "Daily.md"), "old\n")
	if _, err := os.Stat(filepath.Join(root, "New.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run created New.md, stat err = %v", err)
	}
}
