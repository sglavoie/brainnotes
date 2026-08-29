package backup

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func checkArchiveConflicts(pathKey string, kind entryKind, seen map[string]entryKind) error {
	if _, ok := seen[pathKey]; ok {
		return fmt.Errorf("backup restore: archive contains duplicate entry %q", pathKey)
	}

	parts := strings.Split(pathKey, "/")
	prefix := parts[0]
	for i := 1; i < len(parts); i++ {
		if seenKind, ok := seen[prefix]; ok && seenKind != entryDir {
			return fmt.Errorf("backup restore: archive path %q conflicts with file %q", pathKey, prefix)
		}
		prefix += "/" + parts[i]
	}

	if kind != entryFile {
		return nil
	}

	prefix = pathKey + "/"
	for seenPath := range seen {
		if strings.HasPrefix(seenPath, prefix) {
			return fmt.Errorf("backup restore: archive path %q conflicts with descendant %q", pathKey, seenPath)
		}
	}

	return nil
}

func entryKindFromHeader(header *tar.Header) (entryKind, error) {
	switch header.Typeflag {
	case tar.TypeDir:
		return entryDir, nil
	case tar.TypeReg, tar.TypeRegA:
		return entryFile, nil
	case tar.TypeSymlink:
		return "", fmt.Errorf("backup restore: archive entry %q is a symlink", header.Name)
	case tar.TypeLink:
		return "", fmt.Errorf("backup restore: archive entry %q is a hardlink", header.Name)
	default:
		return "", fmt.Errorf("backup restore: archive entry %q has unsupported type %q", header.Name, header.Typeflag)
	}
}

func preflightOverlay(root string, entries []archiveEntry) error {
	for _, entry := range entries {
		if err := checkDestinationConflict(root, entry); err != nil {
			return err
		}
	}

	return nil
}

func checkDestinationConflict(root string, entry archiveEntry) error {
	parts := strings.Split(entry.Path, "/")
	current := root

	for index, part := range parts {
		current = filepath.Join(current, filepath.FromSlash(part))
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("backup restore: inspect destination %s: %w", current, err)
		}

		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("backup restore: destination %s is a symlink", current)
		}

		isLast := index == len(parts)-1
		if !isLast {
			if !info.IsDir() {
				return fmt.Errorf("backup restore: destination parent %s is not a directory", current)
			}

			continue
		}

		switch entry.Kind {
		case entryDir:
			if !info.IsDir() {
				return fmt.Errorf("backup restore: cannot restore directory %s over existing non-directory", current)
			}

		case entryFile:
			if info.IsDir() {
				return fmt.Errorf("backup restore: cannot restore file %s over existing directory", current)
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("backup restore: destination %s has unsupported type", current)
			}
		}
	}

	return nil
}

func applyStage(root, stageDir string, entries []archiveEntry) error {
	dirEntries := make([]archiveEntry, 0, len(entries))
	fileEntries := make([]archiveEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Kind == entryDir {
			dirEntries = append(dirEntries, entry)
			continue
		}

		fileEntries = append(fileEntries, entry)
	}

	sort.Slice(dirEntries, func(i, j int) bool {
		depthI := strings.Count(dirEntries[i].Path, "/")
		depthJ := strings.Count(dirEntries[j].Path, "/")
		if depthI != depthJ {
			return depthI < depthJ
		}

		return dirEntries[i].Path < dirEntries[j].Path
	})

	for _, entry := range dirEntries {
		dest := filepath.Join(root, filepath.FromSlash(entry.Path))
		if err := os.MkdirAll(dest, normalizeDirMode(entry.Mode)); err != nil {
			return fmt.Errorf("backup restore: create directory %s: %w", dest, err)
		}
		if err := os.Chmod(dest, normalizeDirMode(entry.Mode)); err != nil {
			return fmt.Errorf("backup restore: chmod directory %s: %w", dest, err)
		}
	}

	for _, entry := range fileEntries {
		src := filepath.Join(stageDir, filepath.FromSlash(entry.Path))
		dest := filepath.Join(root, filepath.FromSlash(entry.Path))

		file, err := os.Open(src)
		if err != nil {
			return fmt.Errorf("backup restore: open staged file %s: %w", src, err)
		}

		writeErr := writeReaderAtomic(dest, file, normalizeFileMode(entry.Mode))
		closeErr := file.Close()
		if writeErr != nil {
			return fmt.Errorf("backup restore: restore %s: %w", entry.Path, writeErr)
		}
		if closeErr != nil {
			return fmt.Errorf("backup restore: close staged file %s: %w", src, closeErr)
		}
	}

	return nil
}

// wipeVault empties the vault of everything a restore is able to put back.
//
// What it skips is not a list of conveniences, it is exactly isRootExcluded —
// the same rule that kept those entries out of the archive in the first place.
// `backups/` and `.git/` are never archived, so removing either would delete
// something no archive on disk can restore: the other backups, and the vault's
// entire content history. A wipe is meant to discard the vault's *content* and
// replace it with an archive's, and both of those directories are the record of
// what the content used to be. Keeping the two rules the same function means a
// change to what is archived cannot silently make a wipe destructive.
func wipeVault(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("backup restore: read vault root %s: %w", root, err)
	}

	for _, entry := range entries {
		if isRootExcluded(entry.Name()) {
			continue
		}

		target := filepath.Join(root, entry.Name())
		if err := os.RemoveAll(target); err != nil {
			return fmt.Errorf("backup restore: remove %s during wipe: %w", target, err)
		}
	}

	return nil
}

func resolveVaultRoot(vaultRoot string) (string, error) {
	if strings.TrimSpace(vaultRoot) == "" {
		return "", fmt.Errorf("backup: vault root is required")
	}

	absRoot, err := filepath.Abs(vaultRoot)
	if err != nil {
		return "", fmt.Errorf("backup: resolve vault root %q: %w", vaultRoot, err)
	}

	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err == nil {
		absRoot = resolvedRoot
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("backup: resolve vault root %q: %w", vaultRoot, err)
	}

	info, err := os.Stat(absRoot)
	if err != nil {
		return "", fmt.Errorf("backup: stat vault root %s: %w", absRoot, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("backup: vault root %s is not a directory", absRoot)
	}

	return absRoot, nil
}

func resolveArchivePath(root, archivePath string) (string, error) {
	if strings.TrimSpace(archivePath) == "" {
		return "", fmt.Errorf("backup restore: archive path is required")
	}

	var resolved string
	if filepath.IsAbs(archivePath) {
		resolved = filepath.Clean(archivePath)
	} else {
		var err error
		resolved, err = resolveRelativeWithinRoot(root, archivePath)
		if err != nil {
			return "", err
		}
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("backup restore: stat archive %s: %w", resolved, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("backup restore: archive %s is not a regular file", resolved)
	}

	return resolved, nil
}

func resolveRelativeWithinRoot(root, relativePath string) (string, error) {
	joined := filepath.Join(root, relativePath)
	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", fmt.Errorf("backup restore: resolve archive path %s: %w", relativePath, err)
	}

	if !isWithinRoot(root, resolved) {
		return "", fmt.Errorf("backup restore: archive path %s escapes the vault root", relativePath)
	}

	return resolved, nil
}

func isWithinRoot(root, candidate string) bool {
	relPath, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}

	if relPath == "." {
		return true
	}

	return relPath != ".." && !strings.HasPrefix(relPath, ".."+string(filepath.Separator))
}

func isRootExcluded(relPath string) bool {
	firstSegment := relPath
	if index := strings.IndexByte(relPath, '/'); index >= 0 {
		firstSegment = relPath[:index]
	}

	return strings.EqualFold(firstSegment, backupDirName) || strings.EqualFold(firstSegment, gitDirName)
}

func targetsBackupDirectory(relPath string) bool {
	firstSegment := relPath
	if index := strings.IndexByte(relPath, '/'); index >= 0 {
		firstSegment = relPath[:index]
	}

	return strings.EqualFold(firstSegment, backupDirName)
}

func hasDotDotSegment(name string) bool {
	for _, segment := range strings.Split(name, "/") {
		if segment == ".." {
			return true
		}
	}

	return false
}

func hasWindowsVolumePrefix(name string) bool {
	if len(name) < 2 || name[1] != ':' {
		return false
	}

	letter := name[0]
	return ('a' <= letter && letter <= 'z') || ('A' <= letter && letter <= 'Z')
}

func normalizeFileMode(mode fs.FileMode) fs.FileMode {
	if perm := mode.Perm(); perm != 0 {
		return perm
	}

	return 0o644
}

func normalizeDirMode(mode fs.FileMode) fs.FileMode {
	if perm := mode.Perm(); perm != 0 {
		return perm
	}

	return 0o755
}

func writeReaderAtomic(dest string, reader io.Reader, mode fs.FileMode) error {
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create parent directory %s: %w", parent, err)
	}

	tmp, err := os.CreateTemp(parent, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", parent, err)
	}

	committed := false
	defer func() {
		if committed {
			return
		}

		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	if _, err := io.Copy(tmp, reader); err != nil {
		return fmt.Errorf("write temp file for %s: %w", dest, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		return fmt.Errorf("chmod temp file for %s: %w", dest, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temp file for %s: %w", dest, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file for %s: %w", dest, err)
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return fmt.Errorf("rename temp file into %s: %w", dest, err)
	}

	committed = true
	return nil
}

func closeArchiveWriter(tw *tar.Writer, gzw *gzip.Writer, file *os.File) error {
	if err := tw.Close(); err != nil {
		return fmt.Errorf("backup: close tar writer: %w", err)
	}
	if err := gzw.Close(); err != nil {
		return fmt.Errorf("backup: close gzip writer: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("backup: sync archive: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("backup: close archive: %w", err)
	}

	return nil
}
