package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Commit validates and stages every planned change, records one journal entry,
// then applies the changes in path order. Any failure after the first
// successful apply triggers a compensating rollback of the applied paths.
func (t *FileTxn) Commit(journaler TxnJournal) error {
	if t == nil || len(t.order) == 0 {
		return nil
	}
	if err := t.lockPaths(); err != nil {
		t.unlockPaths()
		return err
	}
	defer t.unlockPaths()
	if err := t.stage(); err != nil {
		t.cleanupStaged()
		return err
	}
	if journaler != nil {
		for _, path := range t.order {
			if err := journaler.Capture(path); err != nil {
				t.cleanupStaged()
				return err
			}
		}
	}

	applied := make([]*txnChange, 0, len(t.order))
	for _, path := range t.order {
		change := t.changes[path]
		err := t.apply(change)
		if change.applied {
			applied = append(applied, change)
		}
		if err != nil {
			t.cleanupStagedAfter(path)
			return t.rollback(change.path, err, applied)
		}
	}
	syncParentDirs(t.order)
	return nil
}

// lockPaths takes one exclusive lock per planned path and holds it for the whole
// commit, so two bn writers never run their replacements against one note at the
// same time.
//
// Without it the custody step is observable. Replacing a destination renames it
// aside first, and during that window the path does not exist: a second writer
// reads it as missing, plans a create, and wins the vacant path. Both writers
// then hold a legitimate version and the one in custody can only come back
// through *TxnRecoveryError for a person to merge by hand. Serializing the
// writers turns that into an ordinary staging conflict — the second one sees the
// first one's committed file and replans — which callers already retry.
//
// The locks are taken in sorted path order, which is what makes concurrent
// multi-path transactions deadlock-free.
//
// A transaction planning more paths than the descriptor budget allows takes the
// vault-wide guard exclusively instead, so transaction size is not bounded by
// how many descriptors the process may hold. The exclusion it provides is
// strictly stronger; what it costs is that every other bn writer waits.
//
// External writers are unaffected: an editor saving a note takes no lock, which
// is why custody stays in place to guard against it.
func (t *FileTxn) lockPaths() error {
	locks, err := acquirePathLocks(t.root, t.lockWait, t.lockEscalation, t.order)
	if err != nil {
		return err
	}
	t.locks = locks
	return nil
}

func (t *FileTxn) unlockPaths() {
	t.locks.release()
	t.locks = nil
}

// stage verifies every precondition and writes every replacement to a temp file
// beside its destination. Nothing observable changes until apply renames them,
// so a failure here leaves the vault untouched.
func (t *FileTxn) stage() error {
	if err := t.verifyRequirements(); err != nil {
		return err
	}
	for _, path := range t.order {
		change := t.changes[path]
		data, mode, exists, err := currentFileState(path)
		if err != nil {
			return err
		}
		if err := verifyExpectation(path, change.expect, data, exists); err != nil {
			return err
		}
		change.priorExists = exists
		change.priorData = data
		change.priorMode = mode

		if change.remove {
			continue
		}
		wantMode := change.mode
		if wantMode == 0 {
			if exists {
				wantMode = mode
			} else {
				// The probe creates a real file to read the umask off, so the
				// parent has to exist first. Staging creates it anyway; doing it
				// here is what lets a transaction write into a folder the vault
				// does not have yet.
				dir := filepath.Dir(path)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return fmt.Errorf("vault: mkdir %q: %w", dir, err)
				}
				wantMode, err = defaultNewFileMode(dir)
				if err != nil {
					return err
				}
			}
		}
		staged, err := stageTempFile(path, change.data, wantMode)
		if err != nil {
			return err
		}
		change.staged = staged
		change.mode = wantMode
	}
	return nil
}

func (t *FileTxn) apply(change *txnChange) error {
	if err := t.verifyRequirements(); err != nil {
		return err
	}
	if err := t.verifyUnchangedSinceStage(change); err != nil {
		return err
	}
	if t.hooks.beforeCommit != nil {
		if err := t.hooks.beforeCommit(change.path); err != nil {
			return err
		}
	}
	if err := t.claim(change); err != nil {
		return err
	}
	return t.verifyRequirements()
}

// claim replaces or deletes the destination without a check-then-write window.
// The validation above is a plain read, so a save landing between it and the
// mutation would be overwritten by a check that already passed. Claim therefore
// renames the destination into a custody temp file first: after that rename the
// path no longer exists, so the bytes it judges are exactly the bytes it
// discards, and a mismatch — which can only come from a writer that ran after
// the validation — travels back to the destination untouched.
//
// The replacement is then placed with an exclusive create, so a file that
// appeared while the path was vacant is never clobbered either. A placement
// that fails leaves the destination vacant with its only copy in custody, so
// the custody file goes straight back before the failure is reported.
func (t *FileTxn) claim(change *txnChange) error {
	custody := ""
	if change.priorExists {
		taken, err := t.takeVerifiedCustody(change.path, ExpectContent(change.priorData))
		if err != nil {
			return err
		}
		custody = taken
	}
	if change.remove {
		change.applied = true
		if custody == "" {
			return nil
		}
		return os.Remove(custody)
	}
	if err := t.place(change.staged, change.path); err != nil {
		return t.recoverCustody(err, change.path, custody)
	}
	change.applied = true
	if err := os.Remove(change.staged); err != nil {
		return fmt.Errorf("vault: remove %q: %w", change.staged, err)
	}
	change.staged = ""
	if custody == "" {
		return nil
	}
	return os.Remove(custody)
}

func (t *FileTxn) place(source, path string) error {
	if t.hooks.placeReplacement != nil {
		return t.hooks.placeReplacement(source, path)
	}
	return t.linkExclusive(source, path)
}

// recoverCustody undoes a failed replacement placement. The destination is
// already in custody by then and the replacement never landed, so the path is
// vacant and the custody file holds the only copy of what was there. Putting
// that copy back is what keeps a placement failure from stranding the note in a
// hidden temp file while the caller sees nothing but the underlying error.
//
// The put-back is exclusive, so a writer that created the path while it was
// vacant keeps its file. Both versions then survive and the caller gets a
// *TxnRecoveryError naming the destination and the file the content is kept in.
// A put-back that fails for any other reason leaves the destination absent and
// is reported as such, since telling the caller another writer took the path
// would send them looking for a file that is not there.
func (t *FileTxn) recoverCustody(cause error, path, custody string) error {
	if custody == "" {
		return cause
	}
	err := t.placeExclusive(custody, path)
	if err == nil {
		return cause
	}
	return &TxnRecoveryError{
		Path:         path,
		RecoveryPath: custody,
		Displaced:    IsTxnConflict(err),
		Err:          errors.Join(cause, err),
	}
}

// verifyRequirements re-checks every read-only precondition. Staging is a long
// way from the last rename — journal snapshots are written and earlier paths are
// replaced in between — so the check runs again around each apply, keeping the
// whole commit tied to the dependency it was planned against.
//
// The check before a rename cannot cover the rename itself: a dependency
// replaced in that window would leave the change on disk under content that
// decided nothing about it. Running it again once the change is visible is what
// makes that window decidable — the mismatch is reported as a conflict and the
// applied path is compensated, so the change does not remain committed.
func (t *FileTxn) verifyRequirements() error {
	for _, requirement := range t.requires {
		if err := verifyCurrentState(requirement.path, requirement.expect); err != nil {
			return err
		}
	}
	return nil
}

// verifyUnchangedSinceStage re-reads the destination immediately before it is
// replaced and compares it to the state staging observed. Staging can be a long
// way from the mutation — journal snapshots are written in between — so this
// second check refuses the common concurrent-edit case without moving any file.
// It is a pre-check only: claim re-judges the same expectation once it owns the
// destination, which is what covers the window this check cannot.
func (t *FileTxn) verifyUnchangedSinceStage(change *txnChange) error {
	expect := ExpectMissing()
	if change.priorExists {
		expect = ExpectHash(HashBytes(change.priorData))
	}
	return verifyCurrentState(change.path, expect)
}

// verifyStillCommitted requires the destination to still hold exactly what this
// transaction applied to it. Rollback runs after the path has been observable
// to other writers, so without this check compensation would overwrite an edit
// the transaction never saw.
func (t *FileTxn) verifyStillCommitted(change *txnChange) error {
	expect := ExpectMissing()
	if !change.remove {
		expect = ExpectContent(change.data)
	}
	return verifyCurrentState(change.path, expect)
}
