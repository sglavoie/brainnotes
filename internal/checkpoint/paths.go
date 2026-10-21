package checkpoint

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sglavoie/brainnotes/internal/vault"
)

const (
	// barrierLockName is the lock writers hold shared and a checkpoint holds
	// exclusively. It lives beside the other vault locks so one directory holds
	// every flock this program takes.
	barrierLockName = "checkpoint.lock"
	// claimLockName is held exclusively by a checkpoint client from the moment it
	// announces until it releases, and is only ever try-locked by anybody else. It
	// is what makes "is the client that wrote this record still alive" a question
	// the kernel answers.
	claimLockName = "checkpoint.claim.lock"
	// RecordRel is the vault-relative path of an unmanaged vault's checkpoint
	// record. Managed vaults keep the record with the locks under .git instead.
	//
	// It is under .bnotes and so is out of the content history's scope, out of
	// what synchronization considers portable, and skipped by the vault walk. That
	// is deliberate: the record describes one machine's momentary intent, and
	// committing it or sending it to a peer would describe that intent as part of
	// the vault.
	RecordRel = ".bnotes/checkpoint.json"
)

// vaultPaths are the three files one vault's checkpoint protocol uses, resolved
// once so every operation on a vault agrees on where they are.
type vaultPaths struct {
	// root is the symlink-resolved vault root, and doubles as the process
	// registry key: two names for one vault must share one reference count or the
	// process would take the shared flock twice and deadlock its own checkpoint.
	root    string
	barrier string
	claim   string
	record  string
}

// resolve locates one vault's checkpoint files without creating anything.
func resolve(root string) (vaultPaths, error) {
	if strings.TrimSpace(root) == "" {
		return vaultPaths{}, errors.New("checkpoint: empty vault root")
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return vaultPaths{}, fmt.Errorf("checkpoint: resolve vault root %q: %w", root, err)
	}
	dir, err := checkpointLockDir(realRoot)
	if err != nil {
		return vaultPaths{}, err
	}
	record := filepath.Join(realRoot, filepath.FromSlash(RecordRel))
	if strings.HasPrefix(dir, filepath.Join(realRoot, ".git")+string(filepath.Separator)) {
		record = filepath.Join(dir, "checkpoint.json")
	}
	return vaultPaths{
		root:    realRoot,
		barrier: filepath.Join(dir, barrierLockName),
		claim:   filepath.Join(dir, claimLockName),
		record:  record,
	}, nil
}

// checkpointLockDir keeps protocol locks in Git-private storage for managed
// vaults, where their mere existence cannot dirty the working tree. Unmanaged
// vaults retain the common lock directory used by the rest of internal/vault.
func checkpointLockDir(root string) (string, error) {
	gitDir := filepath.Join(root, ".git")
	if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
		return filepath.Join(gitDir, "brainnotes-safety", "checkpoint-locks"), nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("checkpoint: inspect Git directory: %w", err)
	}
	dir, err := vault.LockDir(root)
	if err != nil {
		return "", fmt.Errorf("checkpoint: locate vault locks: %w", err)
	}
	return dir, nil
}

// RecordPath returns the absolute path of the vault's checkpoint record.
func RecordPath(root string) (string, error) {
	paths, err := resolve(root)
	if err != nil {
		return "", err
	}
	return paths.record, nil
}
