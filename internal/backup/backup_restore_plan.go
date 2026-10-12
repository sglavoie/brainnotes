package backup

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

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
