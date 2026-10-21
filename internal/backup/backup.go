package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/sglavoie/brainnotes/internal/checkpoint"
	"github.com/sglavoie/brainnotes/internal/vault"
)

const (
	backupDirName     = "backups"
	gitDirName        = ".git"
	archiveTimeFormat = "20060102T150405.000000000Z"
)

type CreateOptions struct {
	Now func() time.Time
	// Drain is how long Create waits for in-flight writers to finish before it
	// gives up on taking a consistent copy. Anything non-positive means
	// checkpoint.DefaultDrain.
	Drain time.Duration
	// Context, when set, lets an interrupt stop the archive between entries.
	// A cancelled backup removes its temporary file and publishes nothing.
	Context context.Context
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

// ValidateRestore fully parses an archive and checks its destination conflicts
// without changing vault content. Production restore uses it before publishing
// the outer recovery intent, so a malformed archive cannot strand the vault in
// a permanently pending operation.
func ValidateRestore(vaultRoot, archivePath string, opts RestoreOptions) (string, error) {
	result, err := InspectRestore(vaultRoot, archivePath, opts)
	if err != nil {
		return "", err
	}
	return result.ArchivePath, nil
}

// InspectRestore validates and describes a restore without changing the vault.
func InspectRestore(vaultRoot, archivePath string, opts RestoreOptions) (*RestoreResult, error) {
	return inspectRestore(vaultRoot, archivePath, opts, true)
}

// InspectValidatedRestore describes a durably staged archive from an existing
// outer restore intent without rechecking destination conflicts. Content has
// already completed in this phase, and a restored mode-000 directory may make
// descendant destination inspection impossible by design.
func InspectValidatedRestore(vaultRoot, archivePath string, opts RestoreOptions) (*RestoreResult, error) {
	return inspectRestore(vaultRoot, archivePath, opts, false)
}

func inspectRestore(vaultRoot, archivePath string, opts RestoreOptions, preflight bool) (*RestoreResult, error) {
	root, err := resolveVaultRoot(vaultRoot)
	if err != nil {
		return nil, err
	}
	resolved, err := resolveArchivePath(root, archivePath)
	if err != nil {
		return nil, err
	}
	// Describing a restore needs the entries, not their bytes, so nothing is
	// staged: a preview writes nothing anywhere, in the vault or out of it.
	entries, err := scanArchive(resolved, nil)
	if err != nil {
		return nil, err
	}
	if preflight {
		if err := preflightRestore(root, entries, opts.Wipe); err != nil {
			return nil, err
		}
	}
	return &RestoreResult{ArchivePath: resolved, Files: restoredFileCount(entries), Mode: restoreMode(opts.Wipe)}, nil
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

// Create writes a `.tar.gz` of the whole vault into `backups/`.
//
// It is this repository's own checkpoint client, and the reason
// internal/checkpoint exists: a walk of a live vault reads each file at a
// different instant, so an archive taken while a rename is half-applied holds
// the note under both names, or under neither, and a journal entry whose
// manifest names bytes the archive never copied. Holding the barrier for the
// whole walk is what makes the archive a state the vault was actually in.
//
// The hold is taken after the location guard and before the first byte is
// written. That order matters in both directions: a vault on refused storage is
// refused without ever stopping another process's write, and no part of the
// archive is read before the writers have drained.
//
// A drain that does not finish inside the budget is an error rather than a
// best-effort copy. An archive nobody can trust is worse than no archive, and
// the operator who ran this can run it again.
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
	// A backup writes its archive inside the vault, so it is held to the same
	// location policy as a note. Refusing here rather than mid-archive is what
	// keeps a half-written .tar.gz off storage that replicates it.
	if err := vault.GuardMutation(root, backupsDir); err != nil {
		return nil, err
	}

	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	held, err := checkpoint.Hold(root, checkpoint.Options{Reason: "backup archive", Drain: opts.Drain})
	if err != nil {
		return nil, fmt.Errorf("backup: hold the vault still: %w", err)
	}

	result, createErr := createArchive(ctx, root, backupsDir, now)
	// The release is reported only when nothing else went wrong. A released
	// checkpoint that could not clear its record is a liveness problem for the
	// next writer, not a reason to call a finished archive a failure.
	if releaseErr := held.Release(); releaseErr != nil && createErr == nil {
		return result, fmt.Errorf("backup: release the checkpoint: %w", releaseErr)
	}
	return result, createErr
}

// createArchive writes the archive with the checkpoint already held.
//
// Everything here runs inside that window, including the rename that publishes
// the archive: `backups/` is excluded from the walk, so the bytes this writes
// are never bytes it also reads.
func createArchive(ctx context.Context, root, backupsDir string, now func() time.Time) (*CreateResult, error) {
	if err := vault.EnsureDirDurable(backupsDir, 0o755); err != nil {
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
	walkErr := archiveTree(ctx, tw, root, result)

	closeErr := closeArchiveWriter(tw, gzw, tmp)
	if walkErr != nil {
		return nil, walkErr
	}
	if closeErr != nil {
		return nil, closeErr
	}

	if err := publishArchive(tmp.Name(), archivePath, backupsDir); err != nil {
		return nil, err
	}

	committed = true
	return result, nil
}

func publishArchive(tempPath, archivePath, backupsDir string) error {
	if err := os.Rename(tempPath, archivePath); err != nil {
		return fmt.Errorf("backup: finalize archive %s: %w", archivePath, err)
	}
	if err := vault.SyncDir(backupsDir); err != nil {
		return fmt.Errorf("backup: sync backups directory: %w", err)
	}
	if err := vault.SyncDir(filepath.Dir(backupsDir)); err != nil {
		return fmt.Errorf("backup: sync vault root after publishing backups directory: %w", err)
	}
	return nil
}

// archiveTree walks the vault and hands every eligible entry to archiveOne,
// stopping at the next entry once ctx is cancelled.
func archiveTree(ctx context.Context, tw *tar.Writer, root string, result *CreateResult) error {
	return filepath.WalkDir(root, func(current string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
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

		return archiveOne(tw, current, relPath, info, result)
	})
}

// archiveOne writes one walked entry, counting what it wrote and what it left
// out. A symlink is counted and skipped rather than followed or recorded.
func archiveOne(tw *tar.Writer, current, relPath string, info fs.FileInfo, result *CreateResult) error {
	if info.Mode()&os.ModeSymlink != 0 {
		result.SkippedSymlinks++
		return nil
	}

	switch {
	case info.IsDir():
		return writeArchiveHeader(tw, current, relPath+"/", info)

	case info.Mode().IsRegular():
		if err := writeArchiveHeader(tw, current, relPath, info); err != nil {
			return err
		}
		if err := copyArchiveFile(tw, current); err != nil {
			return err
		}

		result.Files++
		result.Bytes += info.Size()
		return nil

	default:
		return fmt.Errorf("backup: unsupported file type at %s", current)
	}
}

func writeArchiveHeader(tw *tar.Writer, current, name string, info fs.FileInfo) error {
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return fmt.Errorf("backup: create tar header for %s: %w", current, err)
	}

	header.Name = name
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("backup: write tar header for %s: %w", current, err)
	}
	return nil
}

func copyArchiveFile(tw *tar.Writer, current string) error {
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
	return nil
}

// Restore extracts an archive over the vault, either overlaid or after wiping.
//
// It enforces the storage-location policy itself but knows nothing about the
// vault's content history, which is deliberate: this package stays a leaf. Every
// production caller goes through noteops.RestoreBackup instead, which adds the
// Git safety preflight ahead of the extraction and the snapshot after it. Call
// this directly only from a test with no vault history to protect.
func Restore(vaultRoot, archivePath string, opts RestoreOptions) (*RestoreResult, error) {
	return restore(vaultRoot, archivePath, opts, false)
}

// ResumeRestore continues a restore whose outer intent already durably records
// a fully validated archive. Unlike a first attempt, recovery may make archived
// ancestors traversable before overlay preflight: a previous process can die
// after applying a final mode such as 000 but before advancing the outer phase.
func ResumeRestore(vaultRoot, archivePath string, opts RestoreOptions) (*RestoreResult, error) {
	return restore(vaultRoot, archivePath, opts, true)
}

func restore(vaultRoot, archivePath string, opts RestoreOptions, recovery bool) (*RestoreResult, error) {
	root, err := resolveVaultRoot(vaultRoot)
	if err != nil {
		return nil, err
	}

	resolvedArchivePath, err := resolveArchivePath(root, archivePath)
	if err != nil {
		return nil, err
	}

	// Restore is the most destructive mutation bn has — --wipe empties the vault
	// before it writes anything back — so it is refused before the archive is
	// even extracted rather than after the vault has been cleared.
	if err := vault.GuardMutation(root); err != nil {
		return nil, err
	}

	stageDir, entries, cleanup, err := extractArchive(root, resolvedArchivePath)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	if recovery {
		if err := enableExistingRestoreAncestors(root, entries); err != nil {
			return nil, err
		}
	}
	if err := preflightRestore(root, entries, opts.Wipe); err != nil {
		return nil, err
	}

	if err := applyTransactionalRestore(root, stageDir, entries, opts.Wipe); err != nil {
		return nil, err
	}

	return &RestoreResult{
		ArchivePath: resolvedArchivePath,
		Files:       restoredFileCount(entries),
		Mode:        restoreMode(opts.Wipe),
	}, nil
}

func preflightRestore(root string, entries []archiveEntry, wipe bool) error {
	if wipe {
		return nil
	}
	return preflightOverlay(root, entries)
}

func restoreMode(wipe bool) string {
	if wipe {
		return "wipe"
	}
	return "overlay"
}

func restoredFileCount(entries []archiveEntry) int {
	files := 0
	for _, entry := range entries {
		if entry.Kind == entryFile {
			files++
		}
	}
	return files
}
