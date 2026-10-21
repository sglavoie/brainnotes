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

	"github.com/sglavoie/brainnotes/internal/restorestate"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

// stageRelDir is where a restore stages the archive it is about to apply: the
// outer restore intent's own directory, restorestate.RelDir.
//
// Not $TMPDIR, which is frequently a small tmpfs that a large vault does not
// fit on, and which is on another volume from the vault anyway. This directory
// is on the vault's volume and already belongs to the restore:
// noteops.RestoreBackup creates it (with the durable copy of the archive)
// before content is touched and removes it wholesale once the restore has
// converged, so staging left by a killed process is cleared on the resumed run.
// It is also outside everything that could see the staged files as vault
// content: isRootExcluded keeps it out of backups, out of --wipe's removal plan
// and out of archive targets; the content history's .bnotes allowlist never
// names it; the vault walk skips .bnotes; and the synchronization companion is
// locked out for the whole restore.
const stageRelDir = restorestate.RelDir

// scanArchive reads every entry of archivePath, validating each, and hands it to
// visit with the reader positioned at its content. A nil visit only validates:
// the tar reader skips content it is not asked for, and reading to the end still
// verifies the gzip checksum.
func scanArchive(archivePath string, visit func(archiveEntry, io.Reader) error) ([]archiveEntry, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("backup restore: open archive %s: %w", archivePath, err)
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("backup restore: open gzip stream %s: %w", archivePath, err)
	}
	defer gzr.Close()

	reader := tar.NewReader(gzr)
	entries := make([]archiveEntry, 0, 32)
	seen := newArchiveIndex()
	for {
		header, err := nextArchiveHeader(reader)
		if errors.Is(err, io.EOF) {
			return entries, nil
		}
		if err != nil {
			return nil, fmt.Errorf("backup restore: read archive entry from %s: %w", archivePath, err)
		}
		entry, err := normalizeArchiveEntry(header, seen)
		if err != nil {
			return nil, err
		}
		if visit != nil {
			if err := visit(entry, reader); err != nil {
				return nil, err
			}
		}
		entries = append(entries, entry)
	}
}

// extractArchive stages archivePath's content under a fresh directory beneath
// root's stageRelDir. The caller must run cleanup once the staged files have
// been applied; it removes the staging directory, and the parent too when
// nothing else (an outer restore intent) is in it.
func extractArchive(root, archivePath string) (stageDir string, entries []archiveEntry, cleanup func(), err error) {
	parent := filepath.Join(root, filepath.FromSlash(stageRelDir))
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", nil, nil, fmt.Errorf("backup restore: create staging parent: %w", err)
	}
	cleanup = func() {
		_ = os.RemoveAll(stageDir)
		_ = os.Remove(parent)
	}
	stageDir, err = os.MkdirTemp(parent, "extract-*")
	if err != nil {
		cleanup()
		return "", nil, nil, fmt.Errorf("backup restore: create staging directory: %w", err)
	}
	entries, err = scanArchive(archivePath, func(entry archiveEntry, reader io.Reader) error {
		return extractArchiveEntry(stageDir, entry, reader)
	})
	if err != nil {
		cleanup()
		return "", nil, nil, err
	}
	return stageDir, entries, cleanup, nil
}

// writeStagedFile writes one staged file in place, with no fsync and no
// temp-and-rename.
//
// A staged file is never the durable copy of anything. The restore transaction
// copies each one into its own temp file beside the destination, verifies the
// copy against the digest it measured from the staged file, and fsyncs that copy
// before its intent becomes durable; a process that dies before then re-extracts
// from the archive, which is durable already. Syncing here would pay one flush
// per file for bytes no recovery ever reads.
func writeStagedFile(dest string, reader io.Reader, mode fs.FileMode) error {
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create parent directory %s: %w", parent, err)
	}
	file, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create staged file %s: %w", dest, err)
	}
	_, copyErr := io.Copy(file, reader)
	chmodErr := file.Chmod(mode)
	closeErr := file.Close()
	return errors.Join(copyErr, chmodErr, closeErr)
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
	if err := writeStagedFile(stagePath, reader, normalizeFileMode(entry.Mode)); err != nil {
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
