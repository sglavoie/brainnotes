package backup

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

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
	seen := newArchiveIndex()

	for {
		header, err := nextArchiveHeader(reader)
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

		if err := extractArchiveEntry(stageDir, entry, reader); err != nil {
			return "", nil, err
		}

		entries = append(entries, entry)
	}

	success = true
	return stageDir, entries, nil
}

// nextArchiveHeader is tar.Reader.Next without the synchronization guards that
// archives made before those guards were excluded still carry. They are skipped
// rather than refused like the rest of isRootExcluded: the file is empty and
// carries no state, refusing it would make every such archive unrestorable, and
// restoring it would replace the inode a live import or export may hold.
func nextArchiveHeader(reader *tar.Reader) (*tar.Header, error) {
	for {
		header, err := reader.Next()
		if err != nil || !vaultsync.IsStateLock(path.Clean(strings.ReplaceAll(header.Name, "\\", "/"))) {
			return header, err
		}
	}
}

func extractArchiveEntry(stageDir string, entry archiveEntry, reader io.Reader) error {
	stagePath := filepath.Join(stageDir, filepath.FromSlash(entry.Path))
	if entry.Kind == entryDir {
		// Staging directories remain owner-writable until every descendant has
		// been extracted. Archived directory modes are applied only after all
		// content has reached the live destination.
		if err := os.MkdirAll(stagePath, 0o700); err != nil {
			return fmt.Errorf("backup restore: create staged directory %s: %w", stagePath, err)
		}
		return nil
	}
	if err := writeReaderAtomic(stagePath, reader, normalizeFileMode(entry.Mode)); err != nil {
		return fmt.Errorf("backup restore: extract %s: %w", entry.Path, err)
	}
	return nil
}

func normalizeArchiveEntry(header *tar.Header, seen *archiveIndex) (archiveEntry, error) {
	cleaned, err := normalizeArchivePath(header.Name)
	if err != nil {
		return archiveEntry{}, err
	}

	kind, err := entryKindFromHeader(header)
	if err != nil {
		return archiveEntry{}, err
	}
	if strings.EqualFold(cleaned, ".bnotes") && kind != entryDir {
		return archiveEntry{}, fmt.Errorf("backup restore: archive entry %q cannot replace the live sidecar directory", header.Name)
	}

	// The key is the name a case-insensitive, normalization-insensitive
	// filesystem such as APFS treats as one file: composed and decomposed
	// spellings of `café.md` must collide, or one entry silently overwrites
	// the other on extraction.
	key := vaultsync.CollisionKey(cleaned)
	if err := checkArchiveConflicts(key, kind, seen); err != nil {
		return archiveEntry{}, err
	}
	seen.add(key, kind)

	return archiveEntry{Path: cleaned, Kind: kind, Mode: header.FileInfo().Mode()}, nil
}

func normalizeArchivePath(headerName string) (string, error) {
	name := strings.ReplaceAll(headerName, "\\", "/")
	if name == "" {
		return "", fmt.Errorf("backup restore: archive contains an empty path")
	}
	if path.IsAbs(name) || hasWindowsVolumePrefix(name) {
		return "", fmt.Errorf("backup restore: archive entry %q is absolute", headerName)
	}
	if hasDotDotSegment(name) {
		return "", fmt.Errorf("backup restore: archive entry %q escapes the vault", headerName)
	}

	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == "" {
		return "", fmt.Errorf("backup restore: archive entry %q resolves to the vault root", headerName)
	}
	if hasWindowsVolumePrefix(cleaned) {
		return "", fmt.Errorf("backup restore: archive entry %q has a volume prefix", headerName)
	}
	if isRootExcluded(cleaned) {
		return "", fmt.Errorf("backup restore: archive entry %q targets reserved machine-local state", headerName)
	}
	return cleaned, nil
}
