package backup

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/checkpoint"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

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

// resolveArchivePath turns the archive argument of `bn restore` into an
// absolute regular file. Its refusals carry the `restore:` label the user typed
// and name the path as given: a missing archive is the common mistake, and the
// absolute path of a file that does not exist tells nobody where to look.
func resolveArchivePath(root, archivePath string) (string, error) {
	if strings.TrimSpace(archivePath) == "" {
		return "", errors.New("restore: archive path is required")
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
	if errors.Is(err, fs.ErrNotExist) {
		return "", archiveNotFound(archivePath, err)
	}
	if err != nil {
		return "", fmt.Errorf("restore: stat archive %s: %w", archivePath, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("restore: archive %s is not a regular file", archivePath)
	}

	return resolved, nil
}

// archiveNotFound reports an archive argument that names nothing. A relative
// path resolves from the vault root rather than the working directory, which is
// the surprise worth spelling out, along with where archives are written.
func archiveNotFound(archivePath string, cause error) error {
	msg := "restore: no archive at " + archivePath
	if !filepath.IsAbs(archivePath) {
		msg += " (relative paths resolve from the vault root; see " + backupDirName + "/)"
	}
	return &archiveNotFoundError{msg: msg, cause: cause}
}

// archiveNotFoundError keeps the underlying not-exist error reachable through
// errors.Is without appending its absolute-path text to the message.
type archiveNotFoundError struct {
	msg   string
	cause error
}

func (e *archiveNotFoundError) Error() string { return e.msg }
func (e *archiveNotFoundError) Unwrap() error { return e.cause }

func resolveRelativeWithinRoot(root, relativePath string) (string, error) {
	joined := filepath.Join(root, relativePath)
	resolved, err := filepath.EvalSymlinks(joined)
	if errors.Is(err, fs.ErrNotExist) {
		return "", archiveNotFound(relativePath, err)
	}
	if err != nil {
		return "", fmt.Errorf("restore: resolve archive path %s: %w", relativePath, err)
	}

	if !isWithinRoot(root, resolved) {
		return "", fmt.Errorf("restore: archive path %s escapes the vault root", relativePath)
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
	if isMachineLocalSidecar(relPath) {
		return true
	}

	firstSegment := relPath
	if index := strings.IndexByte(relPath, '/'); index >= 0 {
		firstSegment = relPath[:index]
	}

	return strings.EqualFold(firstSegment, backupDirName) || strings.EqualFold(firstSegment, gitDirName)
}

// isMachineLocalSidecar reports the .bnotes entries that describe a live
// process on this machine rather than vault state.
//
// Checkpoint artifacts describe a live process on this machine, not vault
// state. Copying the announcement would restore a dead client's identity, and
// copying lock files would make the archive's file/byte accounting depend on
// whether the vault happened to have been written before the backup. Preserve
// the lock directory during a wipe for the same reason .git is preserved: a
// running process may still hold one of its inodes. The synchronization guards
// live beside the state they serialize rather than under .bnotes/locks, and are
// machine-local for the same reason.
func isMachineLocalSidecar(relPath string) bool {
	return strings.EqualFold(relPath, checkpoint.RecordRel) ||
		strings.EqualFold(relPath, ".bnotes/locks") ||
		strings.HasPrefix(strings.ToLower(relPath), ".bnotes/locks/") ||
		strings.EqualFold(relPath, ".bnotes/txn") ||
		strings.HasPrefix(strings.ToLower(relPath), ".bnotes/txn/") ||
		strings.EqualFold(relPath, ".bnotes/restore") ||
		strings.HasPrefix(strings.ToLower(relPath), ".bnotes/restore/") ||
		vaultsync.IsStateLock(relPath)
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
