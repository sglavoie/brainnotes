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
