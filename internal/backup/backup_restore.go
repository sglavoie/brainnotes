package backup

import (
	"archive/tar"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
	case tar.TypeReg:
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
