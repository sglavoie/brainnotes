package vault

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// recoverTxnIntents finishes every abandoned transaction in the vault. It runs
// before a vault is opened and before any writer takes its locks, so a mutation
// never runs against a vault holding an undecided half-applied transaction.
//
// A record another process is actively committing is skipped, not waited on: the
// exclusive flock its owner holds is the liveness signal, and it is checked
// before any path lock is taken so a live transaction's locks are never queued
// behind.
func recoverTxnIntents(root string) error {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	realRoot, dir, err := txnIntentPaths(root)
	if err != nil {
		// An unresolvable root is not a recovery failure; whatever the caller was
		// about to do will report it far better than this can.
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("vault: read intent directory %q: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), txnIntentSuffix) {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	var failures []error
	for _, name := range names {
		if err := recoverTxnIntent(root, realRoot, dir, filepath.Join(dir, name)); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func recoverTxnIntent(root, realRoot, dir, path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("vault: open intent %q: %w", path, err)
	}
	defer file.Close()
	if lockingSupported {
		held, lockErr := tryLock(file, true)
		if lockErr != nil {
			return fmt.Errorf("vault: lock intent %q: %w", path, lockErr)
		}
		if !held {
			// Another process is committing this transaction right now.
			return nil
		}
		defer func() { _ = unlockFile(file) }()
		// The owner may have finished and unlinked the record between the open and
		// the lock, in which case this descriptor names a file nothing will read
		// again and the transaction it described is already complete.
		if !sameOpenFile(file, path) {
			return nil
		}
	}

	data, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("vault: read intent %q: %w", path, err)
	}
	var record txnIntentRecord
	if err := json.Unmarshal(data, &record); err != nil {
		// The record and its directory entry are both fsynced before the first
		// destructive step runs, so a record that cannot be decoded proves the
		// crash happened while it was still being written and nothing was applied.
		// Discarding it is therefore safe, and self-healing beats wedging the vault
		// on litter that describes no work.
		return discardTxnIntent(dir, path)
	}
	if record.Version == 0 {
		return discardTxnIntent(dir, path)
	}
	if record.Version != txnIntentVersion {
		return &TxnIntentError{
			IntentPath: path,
			Err: fmt.Errorf("record version %d, this build understands %d",
				record.Version, txnIntentVersion),
		}
	}

	paths := make([]string, 0, len(record.Changes))
	for _, change := range record.Changes {
		if !containedIn(realRoot, change.Path) {
			return &TxnIntentError{
				IntentPath: path,
				Err:        fmt.Errorf("change %q resolves outside vault root %q", change.Path, realRoot),
			}
		}
		if change.Staged != "" && !containedIn(realRoot, change.Staged) {
			return &TxnIntentError{
				IntentPath: path,
				Err:        fmt.Errorf("staged file %q resolves outside vault root %q", change.Staged, realRoot),
			}
		}
		paths = append(paths, change.Path)
	}

	// Recovery mutates the same paths ordinary writers do, so it takes the same
	// locks in the same order. acquirePathLocksRaw rather than acquirePathLocks:
	// the recovering wrapper is what called us, and re-entering it here would
	// recurse.
	locks, err := acquirePathLocksRaw(root, 0, 0, paths)
	if err != nil {
		return err
	}
	defer locks.release()

	var unresolved []string
	var failures []error
	for _, change := range record.Changes {
		if err := recoverTxnIntentChange(change); err != nil {
			unresolved = append(unresolved, change.Path)
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return &TxnIntentError{IntentPath: path, Paths: unresolved, Err: errors.Join(failures...)}
	}
	// Same ordering the forward path uses: every destination is durable before the
	// record that describes it stops existing.
	syncParentDirs(paths)
	return discardTxnIntent(dir, path)
}

// recoverTxnIntentChange brings one planned change to a decided state. It
// returns an error only for a failure worth retrying; a change another writer
// has taken over is decided, not failed, because there is nothing this process
// may do about it that would not destroy that writer's work.
func recoverTxnIntentChange(change txnIntentChange) error {
	data, _, exists, err := currentFileState(change.Path)
	if err != nil {
		if IsTxnConflict(err) {
			// Something that is not a regular file occupies the destination. Only
			// a person can decide what that means.
			return nil
		}
		return err
	}
	if txnIntentReached(change, data, exists) {
		// The staged file, if it is still there, holds bytes the destination has
		// already superseded — either the replacement that landed, or the content
		// the swap pushed out. Unlinking it is exactly what the forward path's
		// housekeeping would have done.
		if change.Staged != "" {
			_ = os.Remove(change.Staged)
		}
		return nil
	}
	if !txnIntentPrior(change, data, exists) {
		// The destination holds neither what staging read nor what the transaction
		// meant to write, so a writer outside this transaction owns it now. The
		// staged file is left alone: it holds bytes that exist nowhere else, and
		// deleting it would be the destructive answer to a conflict a person still
		// has to judge.
		return nil
	}
	if change.Remove {
		return completeTxnIntentRemoval(change)
	}
	return completeTxnIntentWrite(change)
}

// txnIntentReached reports whether the destination already holds the outcome the
// transaction planned for it.
func txnIntentReached(change txnIntentChange, data []byte, exists bool) bool {
	if change.Remove {
		return !exists
	}
	return exists && HashBytes(data) == change.NewSHA256
}

// txnIntentPrior reports whether the destination still holds exactly what
// staging read, which is the only state a roll-forward may act on.
func txnIntentPrior(change txnIntentChange, data []byte, exists bool) bool {
	if exists != change.PriorExists {
		return false
	}
	if !exists {
		return true
	}
	return HashBytes(data) == change.PriorSHA256
}

// completeTxnIntentRemoval finishes a delete the crash interrupted. Custody makes
// it a compare-and-swap, so a writer that recreated the path between the read
// above and the rename keeps its file.
func completeTxnIntentRemoval(change txnIntentChange) error {
	custody, err := takeVerifiedCustody(nil, change.Path, ExpectHash(change.PriorSHA256))
	if err != nil {
		if IsTxnConflict(err) {
			return nil
		}
		return err
	}
	// A custody file that cannot be unlinked is housekeeping, not a failed delete:
	// the destination is gone, which is what the transaction asked for.
	_ = removeSuperseded(change.Path, custody)
	return nil
}

// completeTxnIntentWrite finishes a write the crash interrupted, from the staged
// replacement the record names.
func completeTxnIntentWrite(change txnIntentChange) error {
	if change.Staged == "" {
		return nil
	}
	if err := verifyCurrentState(change.Staged, ExpectHash(change.NewSHA256)); err != nil {
		// The staged replacement is gone or no longer holds the planned bytes, so
		// there is nothing left to roll forward. The destination still holds
		// exactly what staging read, which is a consistent state: the write simply
		// never happened, and reporting it as a recovery failure would wedge the
		// vault over work that cannot be completed by anyone.
		return nil
	}
	if !change.PriorExists {
		if err := placeNew(nil, change.Staged, change.Path); err != nil {
			if IsTxnConflict(err) {
				return nil
			}
			return err
		}
		return nil
	}
	// The destination exists, so the replacement is a path-preserving
	// compare-and-swap against the bytes staging read — never an evacuate and
	// place, for the same reason the forward path never uses one.
	if _, err := compareAndSwap(nil, change.Staged, change.Path, ExpectHash(change.PriorSHA256)); err != nil {
		if IsTxnConflict(err) {
			return nil
		}
		return err
	}
	return nil
}

func discardTxnIntent(dir, path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("vault: remove intent %q: %w", path, err)
	}
	return syncDir(dir)
}

// sameOpenFile reports whether path still names the file the descriptor holds.
// It closes the unlink race: a record removed after the open and before the lock
// leaves a lockable descriptor for a file nobody owns.
func sameOpenFile(file *os.File, path string) bool {
	opened, err := file.Stat()
	if err != nil {
		return false
	}
	current, err := os.Stat(path)
	if err != nil {
		return false
	}
	return os.SameFile(opened, current)
}

// containedIn reports whether an absolute path names something inside root. The
// record is written 0600 inside the vault, but it is still a file on disk that
// decides which paths get renamed, so its claims are re-checked rather than
// trusted.
func containedIn(root, path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	rel, err := filepath.Rel(root, filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != "." && !relEscapesRoot(rel)
}
