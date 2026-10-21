package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// RestorePreview is what a restore would do to the vault, computed without
// changing it. Paths are vault-relative and slash-separated.
type RestorePreview struct {
	ArchivePath string
	Mode        string
	// Create lists archived files with no file at their destination.
	Create []string
	// Overwrite lists archived files whose destination holds other bytes.
	Overwrite []string
	// Unchanged counts archived files already identical in the vault.
	Unchanged int
	// Remove lists what --wipe deletes: every path outside the archive except
	// the directories a wipe always keeps.
	Remove []string
}

// PreviewRestore validates the archive exactly as a restore would, then
// describes its effect. It reads the vault and never writes to it, so unlike
// the wipe planner it does not make unreadable directories traversable: one it
// cannot enter is an error rather than a guess.
func PreviewRestore(vaultRoot, archivePath string, opts RestoreOptions) (*RestorePreview, error) {
	root, err := resolveVaultRoot(vaultRoot)
	if err != nil {
		return nil, err
	}
	resolved, err := resolveArchivePath(root, archivePath)
	if err != nil {
		return nil, err
	}
	// The preview hashes each archived file as it streams past instead of
	// staging it: comparing digests needs no bytes on disk, so a preview writes
	// nothing anywhere, in the vault or out of it.
	digests := map[string]string{}
	entries, err := scanArchive(resolved, func(entry archiveEntry, reader io.Reader) error {
		if entry.Kind != entryFile {
			return nil
		}
		hasher := sha256.New()
		if _, err := io.Copy(hasher, reader); err != nil {
			return fmt.Errorf("backup restore: read %s from archive: %w", entry.Path, err)
		}
		digests[entry.Path] = hex.EncodeToString(hasher.Sum(nil))
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := preflightRestore(root, entries, opts.Wipe); err != nil {
		return nil, err
	}
	preview := &RestorePreview{ArchivePath: resolved, Mode: restoreMode(opts.Wipe)}
	if err := preview.classifyFiles(root, digests, entries); err != nil {
		return nil, err
	}
	if opts.Wipe {
		if preview.Remove, err = previewWipe(root, desiredRestorePaths(root, entries)); err != nil {
			return nil, err
		}
	}
	return preview, nil
}

func (p *RestorePreview) classifyFiles(root string, digests map[string]string, entries []archiveEntry) error {
	for _, entry := range entries {
		if entry.Kind != entryFile {
			continue
		}
		dest := filepath.Join(root, filepath.FromSlash(entry.Path))
		info, err := os.Lstat(dest)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			p.Create = append(p.Create, entry.Path)
			continue
		case err != nil:
			return fmt.Errorf("backup restore: inspect %s: %w", entry.Path, err)
		case !info.Mode().IsRegular():
			p.Overwrite = append(p.Overwrite, entry.Path)
			continue
		}
		current, _, err := vault.HashFile(dest)
		if err != nil {
			return fmt.Errorf("backup restore: hash %s: %w", dest, err)
		}
		if current == digests[entry.Path] {
			p.Unchanged++
		} else {
			p.Overwrite = append(p.Overwrite, entry.Path)
		}
	}
	sort.Strings(p.Create)
	sort.Strings(p.Overwrite)
	return nil
}

// previewWipe lists the leaves and directories a wipe would remove, following
// the same exclusions as planWipe.
func previewWipe(root string, desired map[string]struct{}) ([]string, error) {
	var remove []string
	err := filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || current == root {
			return walkErr
		}
		rel, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		slashRel := filepath.ToSlash(rel)
		if isRootExcluded(slashRel) {
			return skipExcludedEntry(entry)
		}
		if _, ok := desired[filepath.Clean(current)]; ok {
			return nil
		}
		if entry.IsDir() && strings.EqualFold(slashRel, ".bnotes") {
			// The wipe keeps the container and empties it (visitDir), so its
			// contents are listed one by one.
			return nil
		}
		if entry.IsDir() {
			// A whole directory goes; listing its contents too would only
			// repeat the one line that matters.
			remove = append(remove, slashRel+"/")
			return filepath.SkipDir
		}
		remove = append(remove, slashRel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("backup restore: preview wipe: %w", err)
	}
	sort.Strings(remove)
	return remove, nil
}
