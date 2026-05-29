package backup

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	backupDirName     = "backups"
	gitDirName        = ".git"
	archiveTimeFormat = "20060102T150405.000000000Z"
)

type CreateOptions struct {
	Now func() time.Time
}

type CreateResult struct {
	ArchivePath     string
	Files           int
	Bytes           int64
	SkippedSymlinks int
}

type RestoreOptions struct {
	Wipe bool
}

type RestoreResult struct {
	ArchivePath string
	Files       int
	Mode        string
}

type entryKind string

const (
	entryDir  entryKind = "directory"
	entryFile entryKind = "file"
)

type archiveEntry struct {
	Path string
	Kind entryKind
	Mode fs.FileMode
}

func Create(vaultRoot string, opts CreateOptions) (*CreateResult, error) {
	root, err := resolveVaultRoot(vaultRoot)
	if err != nil {
		return nil, err
	}

	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}

	backupsDir := filepath.Join(root, backupDirName)
	if err := os.MkdirAll(backupsDir, 0o755); err != nil {
		return nil, fmt.Errorf("backup: create backups directory: %w", err)
	}

	archivePath := filepath.Join(backupsDir, fmt.Sprintf("backup-%s.tar.gz", now().UTC().Format(archiveTimeFormat)))
	if _, err := os.Lstat(archivePath); err == nil {
		return nil, fmt.Errorf("backup: archive already exists: %s", archivePath)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("backup: stat archive path %s: %w", archivePath, err)
	}

	tmp, err := os.CreateTemp(backupsDir, ".backup-*.tar.gz")
	if err != nil {
		return nil, fmt.Errorf("backup: create temporary archive: %w", err)
	}

	committed := false
	defer func() {
		if committed {
			return
		}

		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	gzw := gzip.NewWriter(tmp)
	tw := tar.NewWriter(gzw)

	result := &CreateResult{ArchivePath: archivePath}

	walkErr := filepath.WalkDir(root, func(current string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if current == root {
			return nil
		}

		relPath, err := filepath.Rel(root, current)
		if err != nil {
			return fmt.Errorf("backup: compute relative path for %s: %w", current, err)
		}

		relPath = filepath.ToSlash(relPath)
		if isRootExcluded(relPath) {
			if d.IsDir() {
				return fs.SkipDir
			}

			return nil
		}

		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("backup: stat %s: %w", current, err)
		}

		if info.Mode()&os.ModeSymlink != 0 {
			result.SkippedSymlinks++
			return nil
		}

		switch {
		case info.IsDir():
			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return fmt.Errorf("backup: create tar header for %s: %w", current, err)
			}

			header.Name = relPath + "/"
			if err := tw.WriteHeader(header); err != nil {
				return fmt.Errorf("backup: write tar header for %s: %w", current, err)
			}

		case info.Mode().IsRegular():
			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return fmt.Errorf("backup: create tar header for %s: %w", current, err)
			}

			header.Name = relPath
			if err := tw.WriteHeader(header); err != nil {
				return fmt.Errorf("backup: write tar header for %s: %w", current, err)
			}

			file, err := os.Open(current)
			if err != nil {
				return fmt.Errorf("backup: open %s: %w", current, err)
			}

			_, copyErr := io.Copy(tw, file)
			closeErr := file.Close()
			if copyErr != nil {
				return fmt.Errorf("backup: copy %s into archive: %w", current, copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("backup: close %s: %w", current, closeErr)
			}

			result.Files++
			result.Bytes += info.Size()

		default:
			return fmt.Errorf("backup: unsupported file type at %s", current)
		}

		return nil
	})

	closeErr := closeArchiveWriter(tw, gzw, tmp)
	if walkErr != nil {
		return nil, walkErr
	}
	if closeErr != nil {
		return nil, closeErr
	}

	if err := os.Rename(tmp.Name(), archivePath); err != nil {
		return nil, fmt.Errorf("backup: finalize archive %s: %w", archivePath, err)
	}

	committed = true
	return result, nil
}

func Restore(vaultRoot, archivePath string, opts RestoreOptions) (*RestoreResult, error) {
	root, err := resolveVaultRoot(vaultRoot)
	if err != nil {
		return nil, err
	}

	resolvedArchivePath, err := resolveArchivePath(root, archivePath)
	if err != nil {
		return nil, err
	}

	stageDir, entries, err := extractArchive(resolvedArchivePath)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stageDir)

	if opts.Wipe {
		if err := wipeVault(root); err != nil {
			return nil, err
		}
	} else if err := preflightOverlay(root, entries); err != nil {
		return nil, err
	}

	if err := applyStage(root, stageDir, entries); err != nil {
		return nil, err
	}

	mode := "overlay"
	if opts.Wipe {
		mode = "wipe"
	}

	files := 0
	for _, entry := range entries {
		if entry.Kind == entryFile {
			files++
		}
	}

	return &RestoreResult{
		ArchivePath: resolvedArchivePath,
		Files:       files,
		Mode:        mode,
	}, nil
}

func extractArchive(archivePath string) (string, []archiveEntry, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", nil, fmt.Errorf("backup restore: open archive %s: %w", archivePath, err)
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return "", nil, fmt.Errorf("backup restore: open gzip stream %s: %w", archivePath, err)
	}
	defer gzr.Close()

	stageDir, err := os.MkdirTemp("", "bn-restore-*")
	if err != nil {
		return "", nil, fmt.Errorf("backup restore: create staging directory: %w", err)
	}

	success := false
	defer func() {
		if success {
			return
		}

		_ = os.RemoveAll(stageDir)
	}()

	reader := tar.NewReader(gzr)
	entries := make([]archiveEntry, 0, 32)
	seen := make(map[string]entryKind)

	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", nil, fmt.Errorf("backup restore: read archive entry from %s: %w", archivePath, err)
		}

		entry, err := normalizeArchiveEntry(header, seen)
		if err != nil {
			return "", nil, err
		}

		stagePath := filepath.Join(stageDir, filepath.FromSlash(entry.Path))
		switch entry.Kind {
		case entryDir:
			if err := os.MkdirAll(stagePath, normalizeDirMode(entry.Mode)); err != nil {
				return "", nil, fmt.Errorf("backup restore: create staged directory %s: %w", stagePath, err)
			}
			if err := os.Chmod(stagePath, normalizeDirMode(entry.Mode)); err != nil {
				return "", nil, fmt.Errorf("backup restore: chmod staged directory %s: %w", stagePath, err)
			}

		case entryFile:
			if err := writeReaderAtomic(stagePath, reader, normalizeFileMode(entry.Mode)); err != nil {
				return "", nil, fmt.Errorf("backup restore: extract %s: %w", entry.Path, err)
			}
		}

		entries = append(entries, entry)
	}

	success = true
	return stageDir, entries, nil
}

func normalizeArchiveEntry(header *tar.Header, seen map[string]entryKind) (archiveEntry, error) {
	name := strings.ReplaceAll(header.Name, "\\", "/")
	if name == "" {
		return archiveEntry{}, fmt.Errorf("backup restore: archive contains an empty path")
	}
	if path.IsAbs(name) || hasWindowsVolumePrefix(name) {
		return archiveEntry{}, fmt.Errorf("backup restore: archive entry %q is absolute", header.Name)
	}
	if hasDotDotSegment(name) {
		return archiveEntry{}, fmt.Errorf("backup restore: archive entry %q escapes the vault", header.Name)
	}

	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == "" {
		return archiveEntry{}, fmt.Errorf("backup restore: archive entry %q resolves to the vault root", header.Name)
	}
	if strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return archiveEntry{}, fmt.Errorf("backup restore: archive entry %q escapes the vault", header.Name)
	}
	if hasWindowsVolumePrefix(cleaned) {
		return archiveEntry{}, fmt.Errorf("backup restore: archive entry %q has a volume prefix", header.Name)
	}
	if targetsBackupDirectory(cleaned) {
		return archiveEntry{}, fmt.Errorf("backup restore: archive entry %q targets %s/", header.Name, backupDirName)
	}

	kind, err := entryKindFromHeader(header)
	if err != nil {
		return archiveEntry{}, err
	}

	key := strings.ToLower(cleaned)
	if err := checkArchiveConflicts(key, kind, seen); err != nil {
		return archiveEntry{}, err
	}
	seen[key] = kind

	return archiveEntry{
		Path: cleaned,
		Kind: kind,
		Mode: header.FileInfo().Mode(),
	}, nil
}

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

func wipeVault(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("backup restore: read vault root %s: %w", root, err)
	}

	for _, entry := range entries {
		if strings.EqualFold(entry.Name(), backupDirName) {
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
