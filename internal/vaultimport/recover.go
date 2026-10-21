package vaultimport

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sglavoie/brainnotes/internal/journal"
	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/vaultexport"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

// Outcome names what resolving an interrupted application did.
type Outcome string

const (
	// OutcomeNone means there was nothing to resolve.
	OutcomeNone Outcome = "none"
	// OutcomeDiscarded means this vault's content is as if the package had never
	// arrived: the half-built journal entry was removed and the attempt dropped,
	// and nothing was put back because there was nothing to put back. Scratch
	// files a dead transaction left beside a destination are not destinations and
	// are cleared by settleTransactions.
	//
	// It is a statement about what this pass did, not a promise that no
	// destination was ever written. A rollback that put three notes back and died
	// before it could record that it had leaves a disk indistinguishable from one
	// no destination was written on — that is precisely what a rollback makes
	// true — so the pass that finds it reports this rather than OutcomeRolledBack.
	// The vault is right either way; only the account of how it got there is
	// poorer. See Attempt.Restored for the record that keeps the ordinary case
	// from reaching that point.
	OutcomeDiscarded Outcome = "discarded"
	// OutcomeRolledBack means content had landed without becoming recovery
	// history, and every path was put back to what the package expected to find.
	OutcomeRolledBack Outcome = "rolled-back"
	// OutcomeCompleted means the application had become recovery history, and the
	// steps after it — provenance, snapshot, receipt — were carried through.
	OutcomeCompleted Outcome = "completed"
)

// Recovery reports what Recover resolved.
type Recovery struct {
	// Outcome is what was done.
	Outcome Outcome
	// PackageID is the package the interrupted attempt was applying, empty when
	// there was nothing to resolve.
	PackageID string
	// Paths are the vault-relative paths a rollback restored, in package order.
	Paths []string
	// Result is the applied package's outcome when the attempt was completed
	// rather than undone, carrying the receipt and the recovery entry exactly as
	// Apply would have.
	Result *Result
	// Revised are the packages whose interrupted resolution this pass carried
	// through to its revision, in package order. It is independent of Outcome,
	// which is about the one attempt that may have been pending: a vault can have
	// no interrupted application and still owe a revision, which is exactly the
	// window a resolution leaves.
	Revised []string
	// Abandoned are the packages whose promised resolution this pass dropped, in
	// package order, and it is the report a person needs rather than a statistic:
	// a decision they made did not take effect. There are two reasons, and the
	// vault is left in a different place by each. An application that never
	// landed leaves the package still held, so the conflict can be previewed and
	// decided again. A revision the vault has moved past — a note edited between
	// the application and the revision — leaves the package applied and the edit
	// standing, which is the newer of the two and the one nothing can put back.
	// See finishResolution.
	Abandoned []string
	// AbandonedBecause says which of those two it was, per package, and carries
	// the refusal itself for the second — the digest, the path and the entry a
	// person needs to tell "my edit was newer" from "this vault's copy of my
	// merge is damaged". Every name in Abandoned has an entry; the first reason
	// is not a failure and reads as its own sentence rather than an error from
	// somewhere.
	//
	// It is populated even when this pass then returns an error: the promise is
	// out of the state file by then, so nothing later rediscovers it and dropping
	// the entry would be the end of the news. For the callers that return no
	// Recovery at all on a failure, withAbandonments folds the same names into
	// that error.
	AbandonedBecause map[string]error
}

// Recover resolves an application this vault was interrupted in the middle of,
// and reports what it did.
//
// It is the counterpart of the record Apply writes before it touches anything,
// and it is the only thing that reads that record. An import is six durable
// events — the peer's bytes, the undo manifest, the provenance ledger, the
// commit, the receipt, and the memory of all of it — and no two of them can be
// made one. A process that dies in between leaves the vault in a state that is
// perfectly consistent and completely unexplained, and this is what explains it.
//
// The decision turns on one fact: whether the interrupted operation's journal
// entry has a manifest. That is the moment the change stops being something this
// vault is doing and becomes something this vault did — it is what `bn undo`
// reads, what authorizes committing a deletion, and what the receipt promises the
// peer. Before it, the application is rolled *back*, because acknowledging a
// change the vault could not undo is the one thing a peer must never be told.
// After it, the application is rolled *forward*, because the provenance record is
// already durable by then and the change is already the vault's own history.
//
// One thing it will not do, and refuses outright rather than deciding: it will
// not roll back over a path that holds neither what the package replaced nor what
// it installed. Somebody edited it after the interruption, and their edit is newer
// than anything this knows about. That refusal is ErrStrandedImport, and it leaves
// every artifact where it is — the attempt, the journal entry, the content —
// because a person with all three versions can choose, and a process that guessed
// would have destroyed one of them. The cost of it is that the attempt stays
// pending and every later import refuses too, which is why a rollback that has
// already finished says so durably rather than being inferred from paths that
// have since moved on. See Attempt.Restored, and rollBack for the one gap that
// record narrows rather than closes.
//
// A roll-forward has no such refusal, deliberately. An acknowledgment that cannot
// be minted — because a path no longer reads back as the package left it — fails
// the *acknowledgment* and nothing else: the application is this vault's own
// history either way, the ledger records it, and the attempt is cleared. Blocking
// every future import over a message to a peer would be a heavy answer to a
// problem the vault does not have, and the peer is protected by the receipt never
// being published rather than by the attempt being kept.
//
// A non-nil Recovery returned *with* a non-nil error means the same thing it
// means from Apply, and arises the same way: the package is applied, committed
// and undoable, and the acknowledgment could not be minted or published. It also
// arises from the second half of a pass — an interrupted resolution whose
// revision could not be made — where the Recovery describes what the attempt
// resolved and the error describes the revision that is still owed.
//
// Apply runs this itself before every import, so a caller that only ever applies
// packages never needs it. It is exported for the caller that wants to resolve a
// vault without applying anything else to it.
func Recover(root string, opts Options) (*Recovery, error) {
	ctx, release, acquired, err := lockImports(root)
	if err != nil {
		return nil, err
	}
	defer release()
	if !acquired {
		return nil, fmt.Errorf("%w: recovery did not run", ErrImportBusy)
	}
	opts.checkpointContext = ctx
	return recoverLocked(root, opts)
}

// recoverLocked is Recover with the import guard already held, which is how Apply
// reaches it without taking the lock twice.
func recoverLocked(root string, opts Options) (*Recovery, error) {
	local, err := vaultsync.Load(root)
	if err != nil {
		return nil, err
	}
	state, err := loadState(root, local)
	if err != nil {
		return nil, err
	}
	recovery, err := resolveAttempt(root, opts, local, state)
	if err != nil {
		return recovery, err
	}
	// After the attempt and never before it. A resolution is finished or abandoned
	// according to whether the application it revises landed, and that is exactly
	// the question the attempt above was still deciding — asking first would read
	// an answer that is about to change.
	revised, abandoned, why, err := finishResolutions(root, opts)
	recovery.Revised = revised
	recovery.Abandoned = abandoned
	recovery.AbandonedBecause = why
	return recovery, withAbandonments(err, recovery)
}

// withAbandonments restates a recovery failure so that it also says which
// decisions the same pass gave up on.
//
// The Recovery already carries them, and for Recover that is enough. It is not
// enough for anybody else: Apply, Resolve and Drain each run this pass and each
// return a nil report when it fails, so the map is built and dropped in the same
// breath. Ordinarily that would be survivable — ask the vault later — but an
// abandonment is exactly the fact the vault stops holding. clearResolution has
// already rewritten the state file without the promise, so there is no later
// pass that finds it again and no record left to query. The error is the last
// carrier there is, and a failure about a scratch directory that does not
// mention the decision it took with it is how somebody's choice disappears in
// silence.
//
// The cause stays wrapped rather than replaced, so every sentinel a caller tests
// for still matches.
func withAbandonments(err error, recovery *Recovery) error {
	if err == nil || recovery == nil || len(recovery.Abandoned) == 0 {
		return err
	}
	given := make([]string, 0, len(recovery.Abandoned))
	for _, packageID := range recovery.Abandoned {
		given = append(given, fmt.Sprintf("%s (%v)", packageID, recovery.AbandonedBecause[packageID]))
	}
	return fmt.Errorf("%w; the same pass also gave up on a conflict decision this vault no longer records: %s",
		err, strings.Join(given, "; "))
}

// resolveAttempt is the interrupted application half of a recovery pass, and
// answers OutcomeNone for a vault that was not in the middle of one.
func resolveAttempt(root string, opts Options, local *vaultsync.Enrollment, state *State) (*Recovery, error) {
	if state.Pending == nil {
		return &Recovery{Outcome: OutcomeNone}, nil
	}
	resolver, err := newResolver(root, opts, local, state)
	if err != nil {
		return nil, err
	}
	return resolver.resolve()
}

// resolver is one interrupted attempt being decided.
type resolver struct {
	root      string
	opts      Options
	local     *vaultsync.Enrollment
	state     *State
	attempt   *Attempt
	manifest  *vaultsync.Manifest
	operation *vaultsync.Operation
}

func newResolver(root string, opts Options, local *vaultsync.Enrollment, state *State) (*resolver, error) {
	attempt := state.Pending
	operation, err := singleOperation(attempt.Manifest)
	if err != nil {
		return nil, err
	}
	return &resolver{
		root:      root,
		opts:      opts,
		local:     local,
		state:     state,
		attempt:   attempt,
		manifest:  attempt.Manifest,
		operation: operation,
	}, nil
}

// resolve picks the direction and takes it.
func (r *resolver) resolve() (*Recovery, error) {
	if err := settleTransactions(r.root); err != nil {
		return nil, err
	}
	finalized, err := journal.Finalized(noteops.JournalRoot(r.root), r.attempt.JournalEntry)
	if err != nil {
		return nil, err
	}
	if finalized {
		return r.complete()
	}
	return r.undo()
}

// settleTransactions finishes any file transaction a dead process left prepared,
// so that what the paths hold is what they are going to hold.
//
// It has to happen before anything here looks at a path. internal/vault rolls a
// prepared transaction *forward* on the next acquisition, so a path that reads as
// untouched right now may be one lock away from holding the package's content —
// and a rollback planned against the earlier reading would put half the package
// back and leave the other half applied.
//
// An empty path set is the whole point of the call: acquirePathLocks runs the
// recovery pass before it takes a single lock, so this triggers it and returns
// holding nothing. Passing the package's paths instead would be worse than
// useless, since the locks would then have to be released before the rollback
// transaction could take them again.
func settleTransactions(root string) error {
	release, err := vault.LockPaths(root)
	if err != nil {
		return fmt.Errorf("vaultimport: finish interrupted transactions: %w", err)
	}
	release()
	return nil
}

// complete carries an application that already became recovery history through
// the steps that had not happened yet.
//
// Each of them is idempotent, and has to be, because this can run after a crash
// at any point between them. RecordImport is a no-op for an entry it already
// holds; the snapshot is skipped when the history already has the entry; and the
// receipt is published under a name that can only be taken once.
func (r *resolver) complete() (*Recovery, error) {
	entryID := r.attempt.JournalEntry
	if err := r.recordProvenance(entryID); err != nil {
		return nil, err
	}
	if err := ensureSnapshotContext(r.opts.checkpointContext, r.root, entryID, operationToken); err != nil {
		return nil, err
	}
	entry, err := journal.LoadEntry(noteops.JournalRoot(r.root), entryID)
	if err != nil {
		return nil, fmt.Errorf("%w: %s names %s: %v",
			vaultsync.ErrNoRecoveryEntry, r.manifest.PackageID, entryID, err)
	}
	result, ackErr := r.applier().acknowledge(entry)
	record := Record{
		OperationID:  r.operation.OperationID,
		OriginDevice: r.manifest.OriginDevice,
		Sequence:     r.manifest.Sequence,
		JournalEntry: entryID,
		Receipt:      result.Receipt,
	}
	// The ledger is written whether or not the receipt was, and that ordering is
	// the same one Apply uses: the application is a fact about this vault and the
	// receipt is a message to another one. Recording only the acknowledged case
	// would leave the attempt pending forever, and every later delivery of the
	// package would try to recover an application that has nothing left to
	// recover.
	if err := r.settle(&record); err != nil {
		return nil, errors.Join(ackErr, err)
	}
	return &Recovery{
		Outcome:   OutcomeCompleted,
		PackageID: r.manifest.PackageID,
		Result:    result,
	}, ackErr
}

// recordProvenance repairs the anti-echo ledger for an entry that may or may not
// have reached it. See vaultexport.RecordImport: recording the same entry twice
// is a no-op when the operation matches and an error when it does not.
func (r *resolver) recordProvenance(entryID string) error {
	err := vaultexport.RecordImportContext(r.opts.checkpointContext, r.root, entryID, vaultexport.Import{
		OperationID:  r.operation.OperationID,
		OriginDevice: r.manifest.OriginDevice,
	})
	if err != nil {
		return fmt.Errorf("vaultimport: record import provenance for %s: entry %s applied operation %s from %s: %w",
			r.manifest.PackageID, entryID, r.operation.OperationID, r.manifest.OriginDevice, err)
	}
	return nil
}

// applier rebuilds just enough of an application to read the paths back and mint
// the receipt. It never opens the package: the attempt carries the manifest, and
// what the paths hold is read off this disk. See withoutPayloads.
func (r *resolver) applier() *applier {
	session := &applier{
		root:      r.root,
		opts:      r.opts,
		local:     r.local,
		state:     r.state,
		manifest:  r.manifest,
		operation: r.operation,
	}
	session.withoutPayloads()
	return session
}
