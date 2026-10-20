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

// archiveIndex is what the extraction has seen so far: every entry by key, and
// every directory those entries imply by being beneath it. The implied set is
// what makes the file-over-descendant check a lookup rather than a scan of every
// earlier entry, which made a large archive quadratic to validate.
type archiveIndex struct {
	entries map[string]entryKind
	// implied maps a directory key to the first entry found beneath it, which is
	// the path a conflict reports.
	implied map[string]string
}

func newArchiveIndex() *archiveIndex {
	return &archiveIndex{entries: map[string]entryKind{}, implied: map[string]string{}}
}

// add records pathKey and the directories it implies. Callers check first.
func (idx *archiveIndex) add(pathKey string, kind entryKind) {
	idx.entries[pathKey] = kind
	for parent := pathKey; ; {
		i := strings.LastIndexByte(parent, '/')
		if i < 0 {
			return
		}
		parent = parent[:i]
		if _, ok := idx.implied[parent]; ok {
			// Walking from the deepest parent up, every shorter prefix was
			// recorded when this one was.
			return
		}
		idx.implied[parent] = pathKey
	}
}

func checkArchiveConflicts(pathKey string, kind entryKind, idx *archiveIndex) error {
	if _, ok := idx.entries[pathKey]; ok {
		return fmt.Errorf("backup restore: archive contains duplicate entry %q", pathKey)
	}

	parts := strings.Split(pathKey, "/")
	prefix := parts[0]
	for i := 1; i < len(parts); i++ {
		if seenKind, ok := idx.entries[prefix]; ok && seenKind != entryDir {
			return fmt.Errorf("backup restore: archive path %q conflicts with file %q", pathKey, prefix)
		}
		prefix += "/" + parts[i]
	}

	if kind != entryFile {
		return nil
	}

	if descendant, ok := idx.implied[pathKey]; ok {
		return fmt.Errorf("backup restore: archive path %q conflicts with descendant %q", pathKey, descendant)
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
