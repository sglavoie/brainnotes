package vaultgit

import (
	"context"
	"os"
	"path/filepath"
)

// validateRemovals accepts missing regular files, including journaled files
// created and removed before their first snapshot. A directory or symlink
// replacing a tracked file remains an ambiguous change.
func (m *Manager) validateRemovals(ctx context.Context, head, op string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	if err := m.removalsStillMissing(op, paths); err != nil {
		return err
	}
	if head == "" {
		return nil
	}
	entries, err := m.git.lsTree(ctx, head, paths)
	if err != nil {
		return safetyErrorf(KindRepositoryState, op, "the history of removed paths could not be inspected: %s", err)
	}
	for _, entry := range entries {
		if entry.mode != "100644" && entry.mode != "100755" {
			return m.latch(&SafetyError{Kind: KindAmbiguousChange, Op: op,
				Reason: "a removed path was not a regular file in the content history", Paths: []string{entry.path}})
		}
	}
	return nil
}

// removalsStillMissing distinguishes absence from unreadability and from a
// path recreated after the scan. Stale observations are retryable, not damage.
func (m *Manager) removalsStillMissing(op string, paths []string) error {
	for _, path := range paths {
		info, err := os.Lstat(filepath.Join(m.root, filepath.FromSlash(path)))
		if os.IsNotExist(err) {
			continue
		}
		if err == nil && !info.Mode().IsRegular() {
			return m.latch(&SafetyError{Kind: KindAmbiguousChange, Op: op,
				Reason: "a removed file was replaced by a different filesystem type", Paths: []string{path}})
		}
		return &SafetyError{Kind: KindStaleRequest, Op: op,
			Reason: "a removed path is present again or could not be inspected; retry after the working tree settles", Paths: []string{path}}
	}
	return nil
}
