package backup

import (
	"errors"
	"fmt"
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
	stageDir, entries, err := extractArchive(resolved)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stageDir)
	if err := preflightRestore(root, entries, opts.Wipe); err != nil {
		return nil, err
	}
	preview := &RestorePreview{ArchivePath: resolved, Mode: restoreMode(opts.Wipe)}
	if err := preview.classifyFiles(root, stageDir, entries); err != nil {
		return nil, err
	}
	if opts.Wipe {
		if preview.Remove, err = previewWipe(root, desiredRestorePaths(root, entries)); err != nil {
			return nil, err
		}
	}
	return preview, nil
}

func (p *RestorePreview) classifyFiles(root, stageDir string, entries []archiveEntry) error {
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
		same, err := sameFileContent(filepath.Join(stageDir, filepath.FromSlash(entry.Path)), dest)
		if err != nil {
			return err
		}
		if same {
			p.Unchanged++
		} else {
			p.Overwrite = append(p.Overwrite, entry.Path)
		}
	}
	sort.Strings(p.Create)
	sort.Strings(p.Overwrite)
	return nil
}

func sameFileContent(a, b string) (bool, error) {
	da, _, err := vault.HashFile(a)
	if err != nil {
		return false, fmt.Errorf("backup restore: hash staged file %s: %w", a, err)
	}
	db, _, err := vault.HashFile(b)
	if err != nil {
		return false, fmt.Errorf("backup restore: hash %s: %w", b, err)
	}
	return da == db, nil
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
