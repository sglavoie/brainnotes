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
