package vault

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteStateFile installs data at path through the same path-continuous
// replacement a note gets.
//
// Durable operational state — the brief, the cockpit queue, the recall sidecar —
// is not a note, but it is the record of decisions a person made and cannot
// reconstruct, so losing it is the same class of harm this package exists to
// prevent. Each of those packages used to roll its own guard-fsync-rename
// sequence. The rename was path-preserving, so none of them had the bug that
// cost this vault a note, but each was a separate copy of the durability
// argument and none of them ran the volume self-test that proves the primitive
// behaves the way the argument assumes.
//
// perm applies only when path does not exist yet; an existing state file keeps
// the mode it already has, exactly as a note does.
//
// The location policy is consulted against the file itself rather than a vault
// root, because state files are addressed absolutely by their owning package and
// there is no root to check them against at this depth.
func WriteStateFile(path string, data []byte, perm os.FileMode) error {
	if err := GuardMutation("", path); err != nil {
		return err
	}
	if perm == 0 {
		perm = 0o644
	}
	// ExpectAnyContent rather than a pinned hash: every caller here holds its own
	// process lock over a read-modify-write of the whole file, so there is no
	// preimage to compare against and nothing for a compare-and-swap to add. The
	// expectation still matters — it is what makes the replacement judge that a
	// destination it displaced actually existed.
	if err := atomicWrite(path, data, ExpectAnyContent(), perm); err != nil {
		return fmt.Errorf("vault: write state file %q: %w", path, err)
	}
	// The write itself already fsyncs the directory best-effort. Doing it again
	// here and reporting the failure is the difference between a note (whose
	// content the caller can still see) and state a caller is about to tell a
	// person was saved.
	if err := SyncDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("vault: sync state directory for %q: %w", path, err)
	}
	return nil
}
