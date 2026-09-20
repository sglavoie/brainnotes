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
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/checkpoint"
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
	return SaveContext(context.Background(), vaultRoot, operation, path, data, perm)
}

// SaveContext is Save for a caller that may already own the vault's checkpoint
// admission. The context is also passed into Git safety because opening or
// committing the engine can itself perform durable recovery work.
func SaveContext(ctx context.Context, vaultRoot, operation, path string, data []byte, perm os.FileMode) error {
	// Ahead of everything, including the refusals: joining the checkpoint barrier
	// is what puts this write inside the window a checkpoint waits for, and a
	// write that joined after it took a lock would have inverted the one ordering
	// the barrier depends on. The join covers the snapshot as well as the bytes,
	// because a copy taken between them holds sidecar state the content history
	// does not explain.
	joinedCtx, barrier, err := checkpoint.JoinContext(ctx, vaultRoot)
	if err != nil {
		return fmt.Errorf("vaultstate: %w", err)
	}
	defer barrier()
	// Ahead of the state directory, which is the first artifact a fresh vault
	// would otherwise be left holding after a refusal.
	if err := vault.GuardMutation(vaultRoot, path); err != nil {
		return err
	}
	safety, err := managerFor(joinedCtx, vaultRoot)
	if err != nil {
		return err
	}
	if safety != nil {
		if err := safety.Preflight(joinedCtx); err != nil {
			return fmt.Errorf("vaultstate: git safety preflight: %w", err)
		}
	}
	if err := prepareDir(path); err != nil {
		return err
	}
	if err := vault.WriteStateFile(path, data, perm); err != nil {
		return err
	}
	return snapshotState(joinedCtx, safety, operation, path)
}

// snapshotState records the state file that has just landed in the vault's
// content history. It runs after the bytes are durable, which is why every
// failure it reports is a *vaultgit.SnapshotError: the write happened and a
// caller must not be invited to repeat it.
func snapshotState(ctx context.Context, safety *vaultgit.Manager, operation, path string) error {
	if safety == nil {
		return nil
	}
	result, err := safety.CommitPaths(ctx, operation, []string{path})
	if err != nil {
		return &vaultgit.SnapshotError{
			Operation: operation,
			Blocked:   vaultgit.Blocks(err),
			Err:       err,
		}
	}
	// This call names one exact path, so a snapshot that reports leaving a path
	// out has left out the only one there was. The engine refuses that outcome
	// rather than returning it; confirming it here is what keeps the promise on
	// this side of the boundary too.
	if unrecorded := result.Unrecorded(); len(unrecorded) > 0 {
		return &vaultgit.SnapshotError{
			Operation: operation,
			Err:       fmt.Errorf("the snapshot left out %s", strings.Join(unrecorded, ", ")),
		}
	}
	return nil
}

// managerFor resolves the engine, treating a vault with no Git history as
// "nothing to snapshot" rather than as a failure.
func managerFor(ctx context.Context, vaultRoot string) (*vaultgit.Manager, error) {
	manager, err := vaultgit.Shared(ctx, vaultRoot, vaultgit.Options{})
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
