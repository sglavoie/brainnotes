package vault

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// SyncDir flushes a directory's entries so a rename or a creation that changed
// them survives power loss. POSIX guarantees rename atomicity within a running
// system but not across a crash until the directory itself is synced, which is
// what makes it the last step of every durable write in the vault.
//
// It is exported so packages that keep their own operational state outside the
// vault tree — internal/vaultgit writes below `.git` — reach the same primitive
// instead of duplicating its per-platform build tags. On Windows it is a no-op,
// for the reason stated in dirsync_windows.go.
func SyncDir(dir string) error { return syncDir(dir) }

// EnsureDirDurable creates a directory chain and fsyncs every newly published
// child together with its parent. Syncing only the leaf is insufficient when
// the leaf's ancestor entry is itself new.
func EnsureDirDurable(dir string, mode fs.FileMode) error {
	var missing []string
	for current := filepath.Clean(dir); ; current = filepath.Dir(current) {
		_, err := os.Stat(current)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("vault: stat directory %q: %w", current, err)
		}
		missing = append(missing, current)
		if filepath.Dir(current) == current {
			break
		}
	}
	if err := os.MkdirAll(dir, mode); err != nil {
		return fmt.Errorf("vault: mkdir %q: %w", dir, err)
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := SyncDir(missing[i]); err != nil {
			return err
		}
		if err := SyncDir(filepath.Dir(missing[i])); err != nil {
			return err
		}
	}
	return nil
}
