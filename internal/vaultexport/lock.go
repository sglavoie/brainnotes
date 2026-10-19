package vaultexport

import (
	"fmt"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// lockProducer takes the vault's producer guard without waiting.
//
// It is the export counterpart of internal/vaultimport's import.lock, and it
// exists so the export record's own lock is never held for a whole pass. A pass
// reads the cursor, the sequence, and the reservation, decides, and writes them
// back many times over, and a second producer interleaving with it would publish
// two packages under one sequence. That exclusion used to come from holding the
// record's path lock — and the shared vault guard under it — for the length of
// the pass, which made a RecordImport tried in the meantime wait out the lock
// budget and fail, leaving an applied import the next pass would echo. The
// cursor's writers are serialized here instead, and the record's path lock is
// taken only around each individual write, which is what
// docs/internals/locking.md asks of any new lock.
//
// Wait-free for the reason the import guard is: whoever holds it is doing this
// pass's work, and a waiter would come out of its wait into a cursor that moved
// under the record it was about to read. It goes through vault.TryLockPath and
// not vault.LockPaths, so it never enters the note lock hierarchy.
//
// The caller must already have joined the checkpoint barrier, which sits above
// this guard exactly as it sits above every vault lock.
func lockProducer(root string) (func(), error) {
	release, acquired, err := vault.TryLockPath(vaultsync.StateLockPath(root, vaultsync.ExportLockName))
	if err != nil {
		return nil, fmt.Errorf("vaultexport: take the producer guard: %w", err)
	}
	if !acquired {
		release()
		return nil, ErrExportBusy
	}
	return release, nil
}

// lockState takes the export record's own path lock for one write.
//
// Producers hold the producer guard as well; RecordImport deliberately does not.
// An import records provenance while a pass may be running, and the pass must
// never be able to make it wait for longer than one write.
func lockState(root string) (func(), error) {
	release, err := vault.LockPaths(root, StatePath(root))
	if err != nil {
		return nil, fmt.Errorf("vaultexport: lock export state: %w", err)
	}
	return release, nil
}
