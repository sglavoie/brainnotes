// Package vaultstate is the one write path for bn's durable sidecar state — the
// brief, cockpit, and recall stores under <vault>/.bnotes/.
//
// Those files are not notes, but losing one loses a person's work all the same:
// a snooze, a dismissal, a day's brief selection. So they get the same three
// guarantees a note write gets, and they get them here rather than three times
// over in three packages that had each grown their own temp-file-and-rename.
//
//  1. The storage-location policy refuses the write before anything exists.
//  2. The replacement is path-continuous — the destination never loses its name,
//     so a crash leaves either the old bytes or the new ones.
//  3. The result is snapshotted into the vault's content history, staging that
//     exact path and nothing else.
//
// What is deliberately not here: the generated index under .bnotes/index and the
// lock files under .bnotes/locks. Both are derived state that a vault rebuilds
// on demand, neither carries user intent, and committing either would put a
// commit on every read-shaped operation. They stay out of scope by path, which
// internal/vaultgit enforces, not by convention here.
package vaultstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
)

// Save installs data at path as durable state for the vault at vaultRoot and
// records it in the vault's content history under operation.
//
// operation is a fixed token chosen by the calling package — "brief-state",
// "cockpit-state", "recall-state" — never anything derived from user input or
// from the state's own contents.
//
// A vault with no content history skips the snapshot and keeps the write: an
// un-initialized vault, a fixture, or a copy is a normal thing to run against,
// and refusing to remember a snooze there would be a worse answer than not
// versioning it.
//
// A snapshot that fails after the bytes have landed is reported as a
// *vaultgit.SnapshotError, never as a plain failure. The state file on disk is
// the new state; saying otherwise would invite a caller to retry a write that
// already happened.
func Save(vaultRoot, operation, path string, data []byte, perm os.FileMode) error {
	// Ahead of the state directory, which is the first artifact a fresh vault
	// would otherwise be left holding after a refusal.
	if err := vault.GuardMutation(vaultRoot, path); err != nil {
		return err
	}
	safety, err := managerFor(vaultRoot)
	if err != nil {
		return err
	}
	if safety != nil {
		if err := safety.Preflight(context.Background()); err != nil {
			return fmt.Errorf("vaultstate: git safety preflight: %w", err)
		}
	}
	if err := prepareDir(path); err != nil {
		return err
	}
	if err := vault.WriteStateFile(path, data, perm); err != nil {
		return err
	}
	if safety == nil {
		return nil
	}
	if _, err := safety.CommitPaths(context.Background(), operation, []string{path}); err != nil {
		return &vaultgit.SnapshotError{
			Operation: operation,
			Blocked:   vaultgit.Blocks(err),
			Err:       err,
		}
	}
	return nil
}

// managerFor resolves the engine, treating a vault with no Git history as
// "nothing to snapshot" rather than as a failure.
func managerFor(vaultRoot string) (*vaultgit.Manager, error) {
	manager, err := vaultgit.Shared(context.Background(), vaultRoot, vaultgit.Options{})
	if err != nil {
		if errors.Is(err, vaultgit.ErrUnmanaged) {
			return nil, nil
		}
		return nil, fmt.Errorf("vaultstate: resolve git safety: %w", err)
	}
	return manager, nil
}

// prepareDir creates the state directory and refuses a state directory or state
// file that is itself a symlink, which is how a write the location policy just
// approved would otherwise be redirected somewhere it did not approve.
//
// Only the last component of each is judged. An ancestor may legitimately be a
// symlink — /var is one on macOS, and every t.TempDir lives under it — and
// rejecting those would refuse to write in perfectly ordinary places while
// catching nothing a redirect needs.
func prepareDir(path string) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("vaultstate: mkdir state directory %q: %w", directory, err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("vaultstate: inspect state directory %q: %w", directory, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("vaultstate: state directory %q must be a real directory", directory)
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("vaultstate: state path %q must be a regular file", path)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("vaultstate: inspect state path %q: %w", path, err)
	}
	return nil
}
