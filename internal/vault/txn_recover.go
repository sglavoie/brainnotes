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
	failures = appendTempOwnerFailureWhenSafe(realRoot, failures)
	return errors.Join(failures...)
}

func appendTempOwnerFailureWhenSafe(root string, failures []error) []error {
	if len(failures) != 0 {
		return failures
	}
	return appendTempOwnerFailure(root, failures)
}

func appendTempOwnerFailure(root string, failures []error) []error {
	if err := recoverTempOwners(root); err != nil {
		return append(failures, err)
	}
	return failures
}

func recoverTxnIntent(root, realRoot, dir, path string) error {
	file, active, release, err := openIntentForRecovery(path)
	if err != nil {
		return err
	}
	defer release()
	if !active {
		return nil
	}
	record, proceed, err := readIntentRecord(file, dir, path)
	if err != nil || !proceed {
		return err
	}

	paths, err := validateIntentPaths(path, realRoot, record.Changes)
	if err != nil {
		return err
	}
	// A process can die after this intent becomes durable and before its earlier
	// staged-file owner record publishes the transfer marker. Correlate exact
	// staged paths before applying: an undecidable foreign edit deliberately
	// leaves its staged bytes alone, and the later owner pass must do the same.
	if err := transferTempOwnersForIntent(realRoot, record.Changes); err != nil {
		return &TxnIntentError{IntentPath: path, Paths: paths, Err: err}
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

	cleanup, err := recoverIntentChanges(path, record.Changes)
	if err != nil {
		return err
	}
	return finishRecoveredIntent(root, dir, path, paths, cleanup)
}

func finishRecoveredIntent(root, dir, path string, paths, cleanup []string) error {
	// Same ordering the forward path uses: every destination is durable before the
	// recovery sources or the record that describes it stop existing.
	if err := syncParentDirs(root, paths); err != nil {
		return &TxnIntentError{IntentPath: path, Paths: paths, Err: err}
	}
	if err := removeAndSyncTxnTemps(cleanup); err != nil {
		return &TxnIntentError{IntentPath: path, Err: err}
	}
	return discardTxnIntent(dir, path)
}

func openIntentForRecovery(path string) (*os.File, bool, func(), error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, func() {}, nil
		}
		return nil, false, func() {}, fmt.Errorf("vault: open intent %q: %w", path, err)
	}
	release := func() { _ = file.Close() }
	if !lockingSupported {
		return file, true, release, nil
	}
	held, err := tryLock(file, true)
	if err != nil {
		_ = file.Close()
		return nil, false, func() {}, fmt.Errorf("vault: lock intent %q: %w", path, err)
	}
	release = func() { _ = unlockFile(file); _ = file.Close() }
	return file, held && sameOpenFile(file, path), release, nil
}

func readIntentRecord(file *os.File, dir, path string) (txnIntentRecord, bool, error) {
	data, err := io.ReadAll(file)
	if err != nil {
		return txnIntentRecord{}, false, fmt.Errorf("vault: read intent %q: %w", path, err)
	}
	var record txnIntentRecord
	if err := json.Unmarshal(data, &record); err != nil || record.Version == 0 {
		return txnIntentRecord{}, false, discardTxnIntent(dir, path)
	}
	if record.Version != txnIntentVersion {
		return txnIntentRecord{}, false, &TxnIntentError{IntentPath: path, Err: fmt.Errorf("record version %d, this build understands %d", record.Version, txnIntentVersion)}
	}
	return record, true, nil
}

func recoverIntentChanges(intentPath string, changes []txnIntentChange) ([]string, error) {
	var unresolved []string
	var failures []error
	var cleanup []string
	for _, change := range changes {
		paths, err := recoverTxnIntentChangeWithCleanup(change)
		if err != nil {
			unresolved = append(unresolved, change.Path)
			failures = append(failures, err)
			continue
		}
		cleanup = append(cleanup, paths...)
	}
	if len(failures) != 0 {
		return nil, &TxnIntentError{IntentPath: intentPath, Paths: unresolved, Err: errors.Join(failures...)}
	}
	return cleanup, nil
}

func validateIntentPaths(intentPath, root string, changes []txnIntentChange) ([]string, error) {
	paths := make([]string, 0, len(changes))
	for _, change := range changes {
		if !containedIn(root, change.Path) {
			return nil, &TxnIntentError{IntentPath: intentPath, Err: fmt.Errorf("change %q resolves outside vault root %q", change.Path, root)}
		}
		if change.Staged != "" && !containedIn(root, change.Staged) {
			return nil, &TxnIntentError{IntentPath: intentPath, Err: fmt.Errorf("staged file %q resolves outside vault root %q", change.Staged, root)}
		}
		if change.PriorStaged != "" && !containedIn(root, change.PriorStaged) {
			return nil, &TxnIntentError{IntentPath: intentPath, Err: fmt.Errorf("prior staged file %q resolves outside vault root %q", change.PriorStaged, root)}
		}
		paths = append(paths, change.Path)
	}
	return paths, nil
}

// recoverTxnIntentChange brings one planned change to a decided state. It
// returns an error only for a failure worth retrying; a change another writer
// has taken over is decided, not failed, because there is nothing this process
// may do about it that would not destroy that writer's work.
func recoverTxnIntentChange(change txnIntentChange) error {
	paths, err := recoverTxnIntentChangeWithCleanup(change)
	if err != nil {
		return err
	}
	return removeAndSyncTxnTemps(paths)
}

func recoverTxnIntentChangeWithCleanup(change txnIntentChange) ([]string, error) {
	// The destination is judged by digest rather than by content: an intent
	// records the prior and planned hashes, so nothing here needs the bytes, and
	// a streamed create's destination is the one file this must not hold.
	digest, exists, err := currentFileDigest(change.Path)
	if err != nil {
		if IsTxnConflict(err) {
			// Something that is not a regular file occupies the destination. Only
			// a person can decide what that means.
			return nil, nil
		}
		return nil, err
	}
	if txnIntentReached(change, digest, exists) {
		// The staged file, if it is still there, holds bytes the destination has
		// already superseded — either the replacement that landed, or the content
		// the swap pushed out. Unlinking it is exactly what the forward path's
		// housekeeping would have done.
		return intentChangeTemps(change), nil
	}
	if !txnIntentPrior(change, digest, exists) {
		// The destination holds neither what staging read nor what the transaction
		// meant to write, so a writer outside this transaction owns it now. The
		// staged file is left alone: it holds bytes that exist nowhere else, and
		// deleting it would be the destructive answer to a conflict a person still
		// has to judge.
		return nil, nil
	}
	if change.Remove {
		return completeTxnIntentRemoval(change)
	}
	return completeTxnIntentWrite(change)
}

// txnIntentReached reports whether the destination already holds the outcome the
// transaction planned for it.
func txnIntentReached(change txnIntentChange, digest string, exists bool) bool {
	if change.Remove {
		return !exists
	}
	return exists && digest == change.NewSHA256
}

// txnIntentPrior reports whether the destination still holds exactly what
// staging read, which is the only state a roll-forward may act on.
func txnIntentPrior(change txnIntentChange, digest string, exists bool) bool {
	if exists != change.PriorExists {
		return false
	}
	if !exists {
		return true
	}
	return digest == change.PriorSHA256
}

// completeTxnIntentRemoval finishes a delete the crash interrupted. Custody makes
// it a compare-and-swap, so a writer that recreated the path between the read
// above and the rename keeps its file.
func completeTxnIntentRemoval(change txnIntentChange) ([]string, error) {
	custody, err := takeVerifiedCustody(nil, change.Path, ExpectHash(change.PriorSHA256))
	if err != nil {
		if IsTxnConflict(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := removeSuperseded(change.Path, custody); err != nil {
		return nil, err
	}
	return intentChangeTemps(change), nil
}

// completeTxnIntentWrite finishes a write the crash interrupted, from the staged
// replacement the record names.
func completeTxnIntentWrite(change txnIntentChange) ([]string, error) {
	if change.Staged == "" {
		return intentChangeTemps(change), nil
	}
	// Streamed through a hasher rather than read: the staged file is a full copy
	// of the replacement, and for an attachment that is the whole 25 MiB.
	if err := verifyFileDigest(change.Staged, ExpectHash(change.NewSHA256)); err != nil {
		// The staged replacement is gone or no longer holds the planned bytes, so
		// there is nothing left to roll forward. The destination still holds
		// exactly what staging read, which is a consistent state: the write simply
		// never happened, and reporting it as a recovery failure would wedge the
		// vault over work that cannot be completed by anyone.
		return intentChangeTemps(change), nil
	}
	if err := EnsureDirDurable(filepath.Dir(change.Path), 0o755); err != nil {
		return nil, err
	}
	if !change.PriorExists {
		if err := placeNew(nil, change.Staged, change.Path); err != nil {
			if IsTxnConflict(err) {
				return nil, nil
			}
			return nil, err
		}
		return intentChangeTemps(change), nil
	}
	// The destination exists, so the replacement is a path-preserving
	// compare-and-swap against the bytes staging read — never an evacuate and
	// place, for the same reason the forward path never uses one.
	if _, err := compareAndSwap(nil, change.Staged, change.Path, ExpectHash(change.PriorSHA256)); err != nil {
		if IsTxnConflict(err) {
			return nil, nil
		}
		return nil, err
	}
	return intentChangeTemps(change), nil
}

func intentChangeTemps(change txnIntentChange) []string {
	paths := make([]string, 0, 2)
	if change.Staged != "" {
		paths = append(paths, change.Staged)
	}
	if change.PriorStaged != "" {
		paths = append(paths, change.PriorStaged)
	}
	return paths
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
