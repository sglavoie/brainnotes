package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// resolveNewFolder normalizes the <new-folder> argument. Returns the vault-
// relative slash-separated folder path (empty for the vault root) and its
// absolute equivalent. Paths that escape the vault, are whitespace-only, or
// contain control characters are rejected.
func resolveNewFolder(vaultRoot, arg string) (rel, abs string, err error) {
	if arg == "" {
		return "", vaultRoot, nil
	}
	if strings.TrimSpace(arg) == "" {
		return "", "", fmt.Errorf("move: new folder %q is whitespace-only", arg)
	}
	for _, r := range arg {
		if r == 0 || r < 0x20 || r == 0x7f {
			return "", "", fmt.Errorf("move: new folder %q contains a control character (U+%04X)", arg, r)
		}
	}
	cleaned := filepath.Clean(filepath.FromSlash(arg))
	if cleaned == "." {
		return "", vaultRoot, nil
	}

	fullPath := cleaned
	if !filepath.IsAbs(cleaned) {
		fullPath = filepath.Join(vaultRoot, cleaned)
	}
	fullPath, err = absInVault(vaultRoot, fullPath)
	if err != nil {
		return "", "", fmt.Errorf("move: %w", err)
	}

	relPath, relErr := filepath.Rel(vaultRoot, fullPath)
	if relErr != nil {
		return "", "", fmt.Errorf("move: resolve %q: %w", arg, relErr)
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("move: new folder %q is outside the vault", arg)
	}
	if relPath == "." {
		return "", vaultRoot, nil
	}
	if info, statErr := os.Stat(fullPath); statErr == nil && !info.IsDir() {
		return "", "", fmt.Errorf("move: new folder %q exists but is not a directory", arg)
	}
	return filepath.ToSlash(relPath), fullPath, nil
}

// joinFolderTitle returns folder + "/" + title, or just title when folder is
// empty. Folders are slash-separated to match wikilink Target syntax.
func joinFolderTitle(folder, title string) string {
	if folder == "" {
		return title
	}
	return folder + "/" + title
}

// checkMoveDestination errors when newPath already names a different file
// unless force is set. A case-only move on a case-insensitive filesystem
// (where stat reports the same inode) is allowed.
func checkMoveDestination(targetPath, newPath string, force bool) error {
	if newPath == targetPath {
		return fmt.Errorf("move: new path is identical to current path: %s", newPath)
	}
	newInfo, err := os.Lstat(newPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("move: stat %q: %w", newPath, err)
	}
	targetInfo, terr := os.Lstat(targetPath)
	if terr == nil && os.SameFile(targetInfo, newInfo) {
		return nil
	}
	if newInfo.IsDir() {
		// A directory at the destination would defeat os.Rename — it can't
		// replace a non-empty directory, and --force would only get us partway
		// (after wikilink rewrites had already touched other notes).
		return fmt.Errorf("move: destination %s is an existing directory", newPath)
	}
	if force {
		return nil
	}
	return fmt.Errorf("move: destination %s already exists (use --force to overwrite)", newPath)
}
