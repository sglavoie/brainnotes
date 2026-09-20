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

	"github.com/sglavoie/sglavoie_brainnotes/internal/checkpoint"
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

func planWipe(root string, txn *vault.FileTxn, restored map[string]struct{}) ([]string, error) {
	planner := &wipePlanner{root: root, txn: txn, restored: restored}
	err := filepath.WalkDir(root, planner.visit)
	if err != nil {
		return nil, fmt.Errorf("backup restore: plan wipe: %w", err)
	}
	sort.Slice(planner.cleanup, func(i, j int) bool {
		return strings.Count(planner.cleanup[i], string(filepath.Separator)) > strings.Count(planner.cleanup[j], string(filepath.Separator))
	})
	return planner.cleanup, nil
}

type wipePlanner struct {
	root     string
	txn      *vault.FileTxn
	restored map[string]struct{}
	cleanup  []string
}

func (p *wipePlanner) visit(current string, entry fs.DirEntry, walkErr error) error {
	if walkErr != nil || current == p.root {
		return walkErr
	}
	rel, err := filepath.Rel(p.root, current)
	if err != nil {
		return err
	}
	if isRootExcluded(filepath.ToSlash(rel)) {
		return skipExcludedEntry(entry)
	}
	if entry.IsDir() {
		if err := enableWipeTraversal(current); err != nil {
			return err
		}
	}
	if _, ok := p.restored[filepath.Clean(current)]; ok {
		return nil
	}
	if entry.IsDir() {
		return p.visitDir(current, rel)
	}
	return p.visitLeaf(current, entry)
}

func enableWipeTraversal(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("backup restore: inspect wipe directory %s: %w", dir, err)
	}
	mode := info.Mode().Perm()
	if mode&0o700 == 0o700 {
		return nil
	}
	if err := os.Chmod(dir, mode|0o700); err != nil {
		return fmt.Errorf("backup restore: make wipe directory traversable %s: %w", dir, err)
	}
	if err := vault.SyncDir(dir); err != nil {
		return fmt.Errorf("backup restore: sync traversable wipe directory %s: %w", dir, err)
	}
	return nil
}

func (p *wipePlanner) visitDir(current, rel string) error {
	if !strings.EqualFold(filepath.ToSlash(rel), ".bnotes") {
		p.cleanup = append(p.cleanup, current)
	}
	return nil
}

func (p *wipePlanner) visitLeaf(current string, entry fs.DirEntry) error {
	info, err := entry.Info()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		p.cleanup = append(p.cleanup, current)
		return nil
	}
	digest, _, err := vault.HashFile(current)
	if err != nil {
		return err
	}
	return p.txn.RemoveStreamed(current, vault.ExpectHash(digest))
}

func skipExcludedEntry(entry fs.DirEntry) error {
	if entry.IsDir() {
		return filepath.SkipDir
	}
	return nil
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

func cleanupWipedPaths(root string, paths []string) error {
	for _, candidate := range paths {
		if err := cleanupWipedPath(candidate); err != nil {
			return err
		}
	}
	return vault.SyncDir(root)
}

func cleanupWipedPath(candidate string) error {
	info, err := os.Lstat(candidate)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("backup restore: inspect stale path %s: %w", candidate, err)
	}
	if err := os.Remove(candidate); err != nil && !errors.Is(err, fs.ErrNotExist) {
		kind := "path"
		if info.IsDir() {
			kind = "directory"
		}
		return fmt.Errorf("backup restore: remove stale %s %s: %w", kind, candidate, err)
	}
	return vault.SyncDir(filepath.Dir(candidate))
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
		if strings.EqualFold(entry.Name(), ".bnotes") && entry.IsDir() {
			if err := wipeBnotes(filepath.Join(root, entry.Name())); err != nil {
				return err
			}
			continue
		}

		target := filepath.Join(root, entry.Name())
		if err := os.RemoveAll(target); err != nil {
			return fmt.Errorf("backup restore: remove %s during wipe: %w", target, err)
		}
	}
	if err := vault.SyncDir(root); err != nil {
		return fmt.Errorf("backup restore: sync wiped vault root: %w", err)
	}
	return nil
}

// wipeBnotes removes portable sidecar state while retaining the live checkpoint
// record and lock inodes. Replacing a held lock path would let another process
// enter the same critical section while the original holder still believes it
// owns it.
func wipeBnotes(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("backup restore: read sidecar directory %s: %w", dir, err)
	}
	for _, entry := range entries {
		rel := ".bnotes/" + entry.Name()
		if isRootExcluded(rel) {
			continue
		}
		target := filepath.Join(dir, entry.Name())
		if err := os.RemoveAll(target); err != nil {
			return fmt.Errorf("backup restore: remove %s during wipe: %w", target, err)
		}
	}
	if err := vault.SyncDir(dir); err != nil {
		return fmt.Errorf("backup restore: sync sidecar directory: %w", err)
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
	// Checkpoint artifacts describe a live process on this machine, not vault
	// state. Copying the announcement would restore a dead client's identity,
	// and copying lock files would make the archive's file/byte accounting depend
	// on whether the vault happened to have been written before the backup.
	// Preserve the lock directory during a wipe for the same reason .git is
	// preserved: a running process may still hold one of its inodes.
	if strings.EqualFold(relPath, checkpoint.RecordRel) ||
		strings.EqualFold(relPath, ".bnotes/locks") ||
		strings.HasPrefix(strings.ToLower(relPath), ".bnotes/locks/") ||
		strings.EqualFold(relPath, ".bnotes/txn") ||
		strings.HasPrefix(strings.ToLower(relPath), ".bnotes/txn/") ||
		strings.EqualFold(relPath, ".bnotes/restore") ||
		strings.HasPrefix(strings.ToLower(relPath), ".bnotes/restore/") {
		return true
	}

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
	// A mode of zero is a real archived permission set, not a missing value.
	// Restore materializes directories as owner-accessible first and applies this
	// exact final mode only after their descendants are durable.
	return mode.Perm()
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
	if err := vault.SyncDir(parent); err != nil {
		return fmt.Errorf("sync parent directory for %s: %w", dest, err)
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
