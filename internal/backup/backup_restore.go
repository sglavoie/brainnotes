package backup

import (
	"archive/tar"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
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

// applyTransactionalRestore plans every regular-file replacement and wipe
// removal in one recoverable vault transaction. Its intent is durable before
// the first destination changes, so a killed restore is rolled forward by the
// next checkpoint settle pass instead of becoming a copyable half-restore.
func applyTransactionalRestore(root, stageDir string, entries []archiveEntry, wipe bool) error {
	desired := desiredRestorePaths(root, entries)
	if err := enableExistingRestoreAncestors(root, entries); err != nil {
		return err
	}
	if wipe {
		if err := prepareWipeShapes(root, entries); err != nil {
			return err
		}
	}
	txn, cleanup, err := planRestoreTransaction(root, stageDir, entries, desired, wipe)
	if err != nil {
		return err
	}
	if err := txn.Commit(nil); err != nil {
		return fmt.Errorf("backup restore: commit transaction: %w", err)
	}
	dirs := restoredDirectories(entries)
	if err := materializeRestoreDirs(root, dirs); err != nil {
		return err
	}
	if wipe {
		if err := cleanupWipedPaths(root, cleanup); err != nil {
			return err
		}
	}
	return applyRestoreDirModes(root, dirs)
}

func enableRestoreParents(root string, entries []archiveEntry) error {
	for _, entry := range entries {
		dest := filepath.Join(root, filepath.FromSlash(entry.Path))
		if entry.Kind == entryFile {
			dest = filepath.Dir(dest)
		}
		if err := vault.EnsureDirDurable(dest, 0o755); err != nil {
			return err
		}
		for current := dest; current != root; current = filepath.Dir(current) {
			info, err := os.Stat(current)
			if err != nil {
				return err
			}
			if err := os.Chmod(current, info.Mode().Perm()|0o700); err != nil {
				return err
			}
		}
	}
	return nil
}

func enableExistingRestoreAncestors(root string, entries []archiveEntry) error {
	// A previous restore may have died after publishing an archived restrictive
	// mode but before advancing the outer restore phase. Make every existing
	// ancestor traversable from the root down before inspecting descendants or
	// removing wipe conflicts. Missing and non-directory components are handled
	// by the ordinary materialization/shape pass below.
	for _, entry := range entries {
		dest := filepath.Join(root, filepath.FromSlash(entry.Path))
		if entry.Kind == entryFile {
			dest = filepath.Dir(dest)
		}
		if err := enableExistingRestorePath(root, dest); err != nil {
			return err
		}
	}
	return nil
}

func enableExistingRestorePath(root, dest string) error {
	rel, err := filepath.Rel(root, dest)
	if err != nil {
		return err
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("backup restore: inspect recoverable parent %s: %w", current, err)
		}
		if !info.IsDir() {
			return nil
		}
		if err := os.Chmod(current, info.Mode().Perm()|0o700); err != nil {
			return fmt.Errorf("backup restore: make parent %s recoverable: %w", current, err)
		}
	}
	return nil
}

func planRestoreTransaction(root, stageDir string, entries []archiveEntry, desired map[string]struct{}, wipe bool) (*vault.FileTxn, []string, error) {
	txn, err := vault.NewFileTxn(root)
	if err != nil {
		return nil, nil, fmt.Errorf("backup restore: create transaction: %w", err)
	}
	if _, err := planRestoredFiles(root, stageDir, entries, txn); err != nil {
		return nil, nil, err
	}
	if !wipe {
		return txn, nil, nil
	}
	cleanup, err := planWipe(root, txn, desired)
	if err != nil {
		return nil, nil, err
	}
	return txn, cleanup, nil
}

func desiredRestorePaths(root string, entries []archiveEntry) map[string]struct{} {
	desired := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		path := filepath.Join(root, filepath.FromSlash(entry.Path))
		for path != root {
			desired[filepath.Clean(path)] = struct{}{}
			path = filepath.Dir(path)
		}
	}
	return desired
}

// prepareWipeShapes removes only type conflicts needed to materialize the
// archive (file->directory, directory/symlink->file). The outer restore intent
// makes every step resumable; each mutation is synced before the next begins.
func prepareWipeShapes(root string, entries []archiveEntry) error {
	ordered := append([]archiveEntry(nil), entries...)
	sort.Slice(ordered, func(i, j int) bool {
		return strings.Count(ordered[i].Path, "/") < strings.Count(ordered[j].Path, "/")
	})
	for _, entry := range ordered {
		if err := prepareWipeShape(root, entry); err != nil {
			return err
		}
	}
	return nil
}

func prepareWipeShape(root string, entry archiveEntry) error {
	dest := filepath.Join(root, filepath.FromSlash(entry.Path))
	if err := removeConflictingParents(root, filepath.Dir(dest)); err != nil {
		return err
	}
	info, err := os.Lstat(dest)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("backup restore: inspect destination %s: %w", dest, err)
	}
	if entry.Kind == entryFile && !info.Mode().IsRegular() || entry.Kind == entryDir && !info.IsDir() {
		return removeAndSync(dest)
	}
	return nil
}

func removeConflictingParents(root, start string) error {
	rel, err := filepath.Rel(root, start)
	if err != nil {
		return err
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("backup restore: inspect parent %s: %w", current, err)
		}
		if !info.IsDir() {
			if err := removeAndSync(current); err != nil {
				return err
			}
			return nil
		}
	}
	return nil
}

func removeAndSync(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("backup restore: remove conflicting path %s: %w", path, err)
	}
	if err := vault.SyncDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("backup restore: sync removal of %s: %w", path, err)
	}
	return nil
}

func planRestoredFiles(root, stageDir string, entries []archiveEntry, txn *vault.FileTxn) (map[string]struct{}, error) {
	restored := make(map[string]struct{})
	for _, entry := range entries {
		if entry.Kind != entryFile {
			continue
		}
		src := filepath.Join(stageDir, filepath.FromSlash(entry.Path))
		dest := filepath.Join(root, filepath.FromSlash(entry.Path))
		digest, size, err := vault.HashFile(src)
		if err != nil {
			return nil, fmt.Errorf("backup restore: hash staged file %s: %w", src, err)
		}
		expect, err := currentExpectation(dest)
		if err != nil {
			return nil, err
		}
		if err := txn.ReplaceFrom(dest, src, size, digest, normalizeFileMode(entry.Mode), expect); err != nil {
			return nil, fmt.Errorf("backup restore: plan %s: %w", entry.Path, err)
		}
		restored[filepath.Clean(dest)] = struct{}{}
	}
	return restored, nil
}

func currentExpectation(path string) (vault.FileExpectation, error) {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return vault.ExpectMissing(), nil
	case err != nil:
		return vault.FileExpectation{}, fmt.Errorf("backup restore: inspect destination %s: %w", path, err)
	case !info.Mode().IsRegular():
		return vault.FileExpectation{}, fmt.Errorf("backup restore: destination %s has unsupported type", path)
	}
	digest, _, err := vault.HashFile(path)
	if err != nil {
		return vault.FileExpectation{}, fmt.Errorf("backup restore: hash destination %s: %w", path, err)
	}
	return vault.ExpectHash(digest), nil
}
func applyRestoredDirectories(root string, entries []archiveEntry) error {
	dirs := restoredDirectories(entries)
	if err := materializeRestoreDirs(root, dirs); err != nil {
		return err
	}
	return applyRestoreDirModes(root, dirs)
}

func restoredDirectories(entries []archiveEntry) []archiveEntry {
	dirs := make([]archiveEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Kind == entryDir {
			dirs = append(dirs, entry)
		}
	}
	return dirs
}

func applyRestoreDirModes(root string, dirs []archiveEntry) error {
	sort.Slice(dirs, func(i, j int) bool {
		return strings.Count(dirs[i].Path, "/") > strings.Count(dirs[j].Path, "/")
	})
	for _, entry := range dirs {
		if err := applyRestoreDirMode(root, entry); err != nil {
			return err
		}
	}
	return nil
}

func materializeRestoreDirs(root string, dirs []archiveEntry) error {
	for _, entry := range dirs {
		dest := filepath.Join(root, filepath.FromSlash(entry.Path))
		if err := vault.EnsureDirDurable(dest, 0o755); err != nil {
			return fmt.Errorf("backup restore: create directory %s: %w", dest, err)
		}
		if err := vault.SyncDir(filepath.Dir(dest)); err != nil {
			return fmt.Errorf("backup restore: sync parent of %s: %w", dest, err)
		}
		info, err := os.Stat(dest)
		if err != nil {
			return fmt.Errorf("backup restore: stat directory %s: %w", dest, err)
		}
		if err := os.Chmod(dest, info.Mode().Perm()|0o700); err != nil {
			return fmt.Errorf("backup restore: make directory %s recoverable: %w", dest, err)
		}
	}
	return nil
}

func applyRestoreDirMode(root string, entry archiveEntry) error {
	dest := filepath.Join(root, filepath.FromSlash(entry.Path))
	mode := normalizeDirMode(entry.Mode)
	if strings.EqualFold(entry.Path, ".bnotes") {
		mode |= 0o700
	}
	handle, err := os.Open(dest)
	if err != nil {
		return fmt.Errorf("backup restore: open directory %s for final mode: %w", dest, err)
	}
	if err := handle.Chmod(mode); err != nil {
		_ = handle.Close()
		return fmt.Errorf("backup restore: chmod directory %s: %w", dest, err)
	}
	if err := handle.Sync(); err != nil {
		_ = handle.Close()
		return fmt.Errorf("backup restore: sync directory %s: %w", dest, err)
	}
	if err := handle.Close(); err != nil {
		return fmt.Errorf("backup restore: close directory %s: %w", dest, err)
	}
	if err := vault.SyncDir(filepath.Dir(dest)); err != nil {
		return fmt.Errorf("backup restore: sync parent of %s: %w", dest, err)
	}
	return nil
}
