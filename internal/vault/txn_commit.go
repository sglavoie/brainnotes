package vault

import (
	"fmt"
	"os"
	"path/filepath"
)

// Commit validates and stages every planned change, records one journal entry,
// then applies the changes in path order. Any failure after the first
// successful apply triggers a compensating rollback of the applied paths.
//
// lockPaths runs first and runs GuardMutation over the root and every planned
// path, so a transaction on refused storage stops before a lock file, a staged
// temp file, or a journal entry directory exists. That ordering is load-bearing:
// stage creates missing folders and writes a temp file beside every
// destination, and journaling creates the entry directory, all of which are
// artifacts the guard is supposed to prevent.
func (t *FileTxn) Commit(journaler TxnJournal) error {
	if t == nil || len(t.order) == 0 {
		return nil
	}
	if err := t.lockPaths(); err != nil {
		t.unlockPaths()
		return err
	}
	defer t.unlockPaths()
	intent, err := t.prepareCommit(journaler)
	if err != nil {
		return err
	}
	t.intent = intent
	// Clearing is idempotent. The success path calls it explicitly once every
	// destination is durable; every failure path falls through to this deferred
	// call, which is correct because Commit is returning to a caller that has
	// already been told the outcome — the record only ever needs to outlive a
	// process that never returns.
	retainIntent := false
	defer func() {
		t.intent = nil
		t.abandonTempOwners()
		if retainIntent {
			intent.retain()
		} else {
			_ = intent.clear()
		}
	}()
	txnCrash(txnCrashIntent)

	if err := t.applyPrepared(); err != nil {
		return err
	}
	if err := syncParentDirs(t.root, t.order); err != nil {
		retainIntent = true
		return fmt.Errorf("vault: sync committed transaction: %w", err)
	}
	txnCrash(txnCrashSynced)
	// Durable completion, so recovery sources and then the intent may go. Cleanup
	// failures are reported and leave the record for the next recovery pass;
	// otherwise a large streamed preimage could become an unowned permanent leak.
	if err := intent.clear(); err != nil {
		retainIntent = true
		return fmt.Errorf("vault: clear committed transaction: %w", err)
	}
	// Every mutation landed. A temp file that could not be unlinked afterwards
	// is reported here rather than through the rollback path, because undoing a
	// committed exchange over a housekeeping failure would be the destructive
	// answer to the harmless problem.
	return t.cleanupFailure()
}

func (t *FileTxn) prepareCommit(journaler TxnJournal) (*txnIntent, error) {
	if err := t.stage(); err != nil {
		t.cleanupStaged()
		return nil, err
	}
	if journaler != nil {
		for _, path := range t.order {
			if err := journaler.Capture(path); err != nil {
				t.cleanupStaged()
				return nil, err
			}
		}
	}
	// Staged names must be durable before the intent refers to them. Replacements
	// now live in the private transaction directory, so syncing destination
	// parents would neither publish these entries nor be possible for a new
	// nested destination whose parents intentionally do not exist yet.
	if err := t.syncTempOwnerDir(); err != nil {
		t.cleanupStaged()
		return nil, err
	}
	txnCrash(txnCrashStaged)
	intent, err := t.writeIntent()
	if err != nil {
		t.cleanupStaged()
		return nil, err
	}
	txnCrash(txnCrashHandoff)
	// The durable transaction intent now owns every staged name. Publish that
	// transfer before releasing the owner liveness lock, so a surviving owner
	// record can never race the live intent and delete its recovery material.
	if err := t.handoffTempOwner(); err != nil {
		// The main intent is already durable, so an uncertain handoff is itself a
		// recovery state. Leave both records and their staged bytes in place, but
		// release liveness so the next open can roll the intent forward before it
		// processes the untransferred owner record.
		intent.retain()
		t.retainTempOwner()
		return nil, &TxnIntentError{
			IntentPath: intent.path,
			Paths:      append([]string(nil), t.order...),
			Err:        fmt.Errorf("hand off staged files to transaction intent: %w", err),
		}
	}
	return intent, nil
}

func (t *FileTxn) applyPrepared() error {
	applied := make([]*txnChange, 0, len(t.order))
	for i, path := range t.order {
		change := t.changes[path]
		err := t.apply(change)
		if change.applied {
			applied = append(applied, change)
		}
		if err != nil {
			t.cleanupStagedAfter(path)
			return t.rollback(change.path, err, applied)
		}
		txnCrash(txnCrashApplied(i))
	}
	return nil
}

// lockPaths takes one exclusive lock per planned path and holds it for the whole
// commit, so two bn writers never run their replacements against one note at the
// same time.
//
// Overwrites no longer vacate a destination, so the lock is no longer what keeps
// a second writer out of a hole in the middle of a replacement. What it still
// buys is that two bn writers never interleave a plan and a commit on one note:
// without it both stage against the same bytes, the second one's swap displaces
// the first one's committed file, and the compare-and-swap turns that into a
// *TxnConflictError the caller has to replan from. Serializing them makes the
// second writer see the committed file while it stages instead — an ordinary
// staging conflict, detected before anything is written.
//
// It also still guards the two operations that do vacate a path by design: a
// delete, and the compensating undo of a create.
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
// is why every mutation still judges the bytes it displaces rather than trusting
// the lock.
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

// stage verifies every precondition and writes every replacement to the private
// transaction area on the vault filesystem. Nothing observable changes until
// apply renames them, so a failure here leaves vault content untouched.
func (t *FileTxn) stage() error {
	if err := t.verifyRequirements(); err != nil {
		return err
	}
	for _, path := range t.order {
		if err := t.stageChange(t.changes[path]); err != nil {
			return err
		}
	}
	return nil
}

// stageChange validates one planned path and writes its replacement privately.
func (t *FileTxn) stageChange(change *txnChange) error {
	if change.remove && change.streamPrior {
		return t.stageStreamedRemoval(change)
	}
	if change.source != "" {
		return t.stageStreamedChange(change)
	}
	path := change.path
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
		return nil
	}
	wantMode, err := stagedMode(t.root, path, change.mode, mode, exists)
	if err != nil {
		return err
	}
	staged, err := t.stageOwnedTempFile(path, change.data, wantMode)
	if err != nil {
		return err
	}
	change.staged = staged
	change.mode = wantMode
	return nil
}

// stagedMode resolves the mode a staged replacement is written with. A zero
// planned mode preserves an existing destination's mode, or derives a new file's
// from the process umask.
func stagedMode(root, path string, planned, current os.FileMode, exists bool) (os.FileMode, error) {
	if planned != 0 {
		return planned, nil
	}
	if exists {
		return current, nil
	}
	// The probe creates a real file to read the umask off, so the parent has to
	// exist first. Staging creates it anyway; doing it here is what lets a
	// transaction write into a folder the vault does not have yet.
	return defaultNewFileMode(root)
}

func (t *FileTxn) apply(change *txnChange) error {
	if err := t.verifyRequirements(); err != nil {
		return err
	}
	// Parent directories are created only after the recovery intent is durable.
	// A killed pre-intent transaction therefore has no content-tree artifacts,
	// while a killed apply is completed by intent recovery.
	if !change.remove {
		if err := EnsureDirDurable(filepath.Dir(change.path), 0o755); err != nil {
			return err
		}
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

// claim performs one planned change against its destination. The three kinds of
// change are three different primitives rather than one generic one, because
// only the overwrite has to keep a name occupied throughout:
//
//   - A create places the staged file exclusively. Nothing is at the path, so
//     nothing can be evacuated, and refusing an occupied path is what keeps a
//     file that appeared since staging from being clobbered.
//   - An overwrite is a path-preserving compare-and-swap. It never calls a
//     helper that renames the destination away first — that vacancy is the bug
//     this whole layer exists to remove.
//   - A delete takes the destination into custody, judges the bytes it took, and
//     unlinks them. The path ends up absent because that is what was asked for,
//     so the transient vacancy is the operation rather than a side effect of it.
//
// All three judge the bytes they actually displace rather than the bytes an
// earlier read observed. The validation above is a plain read, so a save landing
// between it and the mutation would otherwise be lost to a check that already
// passed; a mismatch here can only come from a writer that ran after that check,
// and its file always survives.
func (t *FileTxn) claim(change *txnChange) error {
	switch {
	case change.remove:
		return t.claimRemoval(change)
	case !change.priorExists:
		return t.claimCreate(change)
	default:
		return t.claimOverwrite(change)
	}
}

// claimCreate installs a staged file at a path that held nothing when the
// transaction staged it. A failure leaves the path absent and the staged bytes
// where they were, so there is nothing to put back.
func (t *FileTxn) claimCreate(change *txnChange) error {
	if err := placeNew(&t.hooks, change.staged, change.path); err != nil {
		return err
	}
	change.staged = ""
	change.applied = true
	return nil
}

// claimOverwrite replaces an existing destination through the safe replacement
// layer, requiring the content it displaces to be exactly what staging read. A
// refused replacement leaves the destination untouched; a stale preimage leaves
// the concurrent writer's version in place and reports a conflict. The
// destination name exists continuously in both outcomes.
func (t *FileTxn) claimOverwrite(change *txnChange) error {
	expect := ExpectContent(change.priorData)
	if change.priorSource != "" {
		expect = ExpectHash(change.priorDigest)
	}
	outcome, err := compareAndSwap(&t.hooks, change.staged, change.path, expect)
	if outcome.sourceConsumed {
		change.staged = ""
	}
	if outcome.landed {
		change.applied = true
	}
	t.noteCleanupFailure(outcome.cleanup)
	return err
}

// claimRemoval deletes a planned path. Custody is what makes the delete a
// compare-and-swap: after the rename the destination no longer exists, so the
// bytes it judges are exactly the bytes it discards.
func (t *FileTxn) claimRemoval(change *txnChange) error {
	if !change.priorExists {
		change.applied = true
		return nil
	}
	expect := ExpectContent(change.priorData)
	if change.priorSource != "" {
		expect = ExpectHash(change.priorDigest)
	}
	custody, err := takeVerifiedCustody(&t.hooks, change.path, expect)
	if err != nil {
		return err
	}
	change.applied = true
	t.noteCleanupFailure(removeSuperseded(change.path, custody))
	return nil
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
	if change.source != "" || change.streamPrior {
		if change.priorExists {
			return verifyFileDigest(change.path, ExpectHash(change.priorDigest))
		}
		return verifyAbsent(change.path)
	}
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
	if change.remove {
		return verifyCurrentState(change.path, ExpectMissing())
	}
	if change.source != "" {
		return verifyFileDigest(change.path, ExpectHash(change.digest))
	}
	return verifyCurrentState(change.path, ExpectContent(change.data))
}
