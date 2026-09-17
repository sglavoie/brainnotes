package vaultimport

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultexport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
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
// and undoable, and the acknowledgment could not be minted or published.
//
// Apply runs this itself before every import, so a caller that only ever applies
// packages never needs it. It is exported for the caller that wants to resolve a
// vault without applying anything else to it.
func Recover(root string, opts Options) (*Recovery, error) {
	release, acquired, err := lockImports(root)
	if err != nil {
		return nil, err
	}
	defer release()
	if !acquired {
		return nil, fmt.Errorf("%w: recovery did not run", ErrImportBusy)
	}
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
	if err := ensureSnapshot(r.root, entryID); err != nil {
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
	err := vaultexport.RecordImport(r.root, entryID, vaultexport.Import{
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

// undo takes an application that never became recovery history back out.
//
// The order is the contract. Content first, because that is the part another
// process can see; the half-built journal entry second, because its snapshots are
// what the content was restored *from* and removing them first would make a
// failure between the two unrecoverable; the attempt last, because it is what
// says either of the first two still has to happen.
//
// This can be interrupted too, so the content step writes down that it finished:
// see Attempt.Restored. The two steps after it are removals of things nothing
// else reads, which is what makes them safe to repeat — but only for a pass that
// is not still deciding whether the first step happened.
func (r *resolver) undo() (*Recovery, error) {
	restored, err := r.rollBack()
	if err != nil {
		return nil, err
	}
	if err := journal.DiscardUnfinalized(noteops.JournalRoot(r.root), r.attempt.JournalEntry); err != nil {
		return nil, err
	}
	if err := r.settle(nil); err != nil {
		return nil, err
	}
	outcome := OutcomeDiscarded
	if len(restored) > 0 {
		outcome = OutcomeRolledBack
	}
	return &Recovery{Outcome: outcome, PackageID: r.manifest.PackageID, Paths: restored}, nil
}

// rollBack puts the content back if that has not already happened, and reports
// the paths it put back.
//
// It asks two questions before it plans anything, and neither is an optimization.
// An attempt that already recorded a completed rollback is answered from that
// record, because the disk can no longer tell it apart from one that never
// started. And an attempt whose journal entry was never started cannot be planned
// against at all: plan decides per path by comparing the disk against what the
// package said it would find, so it is only meaningful when the package was right
// about that — and the commonest reason an application fails is that it was
// *wrong* about it. Without that branch an ordinary ErrConflict would leave an
// attempt no later pass could resolve, and the vault would refuse every
// subsequent import over a refusal that changed nothing. journal.Started answers
// it from the operation's own durable trace, which no claim in the package can
// contradict.
//
// The record is written after the restoration rather than before it, and the gap
// between the two is the one thing here that is narrowed rather than closed. A
// content transaction and a state file cannot be made one write, so a process
// that dies between them leaves a finished rollback that says nothing — and a
// person who edits one of those notes before the next pass runs turns it into the
// stranding refusal above, over work that was already correct. The other order is
// worse and not a trade: a record written first, then a crash before the restore,
// makes the next pass skip the restoration entirely, discard the entry holding
// the only copy of the pre-change bytes, and settle an attempt whose content is
// still the peer's. So the window is kept and made as small as one fsynced write:
// everything after it — discarding the entry directory, which is a recursive
// removal and not atomic either, and settling the attempt — is covered.
func (r *resolver) rollBack() ([]string, error) {
	if r.attempt.Restored != nil {
		return r.attempt.Restored.Paths, nil
	}
	started, err := journal.Started(noteops.JournalRoot(r.root), r.attempt.JournalEntry)
	if err != nil {
		return nil, err
	}
	var plan []string
	if started {
		if plan, err = r.plan(); err != nil {
			return nil, err
		}
		if err := r.restore(plan); err != nil {
			return nil, err
		}
	}
	if err := r.markRestored(plan); err != nil {
		return nil, err
	}
	importCrash(crashRestored)
	return plan, nil
}

// markRestored makes the finished rollback durable, in the same file and under
// the same lock as everything else this package remembers.
//
// It is written even when nothing was put back, and that is the case it matters
// most for: "there was nothing to restore" is a conclusion drawn from what the
// disk held at one moment, and a pass that has to draw it again later may be
// looking at a vault somebody has edited since.
func (r *resolver) markRestored(paths []string) error {
	next := r.state.clone()
	next.Pending.Restored = &Rollback{Paths: paths}
	if err := saveState(r.root, next); err != nil {
		return err
	}
	r.state = next
	r.attempt = next.Pending
	return nil
}

// plan reports which of the package's paths have to be put back, and refuses if
// any of them has moved on.
//
// Three answers per path and no fourth. A path holding what the package expected
// to find was never written, so there is nothing to undo. A path holding what the
// package installed was written and is undone. A path holding anything else was
// edited by somebody after the interruption — and that edit is newer than the
// import, newer than the snapshot, and the only copy of itself.
func (r *resolver) plan() ([]string, error) {
	var (
		plan      []string
		contested []string
	)
	for _, change := range r.operation.Changes {
		state, err := diskState(absPath(r.root, change.Path))
		if err != nil {
			return nil, err
		}
		switch {
		case state == beforeState(change):
			continue
		case state == afterState(change):
			plan = append(plan, change.Path)
		default:
			contested = append(contested, change.Path)
		}
	}
	if len(contested) > 0 {
		return nil, r.stranded("rolled back", contested)
	}
	return plan, nil
}

// restore puts the listed paths back as one transaction.
//
// It is journaled by nothing and snapshotted into nothing, deliberately. A
// rollback returns the vault to the state it was in before the import began,
// which is a state the content history already describes; recording an undo entry
// for it would add a manifest describing a change that, once finished, cannot be
// observed, and taking a snapshot would commit the vault's own prior content back
// over itself as though something had happened.
//
// Every expectation here is pinned to what the *package* installed rather than to
// what the plan just saw. That is what makes "no rollback over a newer edit" a
// property of the transaction instead of a property of a check: internal/vault
// verifies each one under the path's own lock, so an edit landing between the
// plan and the commit refuses the rollback rather than losing to it.
func (r *resolver) restore(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	txn, err := vault.NewFileTxn(r.root)
	if err != nil {
		return err
	}
	index := r.changeIndex()
	for _, rel := range paths {
		if err := r.stage(txn, index[rel]); err != nil {
			return err
		}
	}
	if err := txn.Commit(nil); err != nil {
		if vault.IsTxnConflict(err) {
			return r.stranded("rolled back", paths)
		}
		return fmt.Errorf("vaultimport: roll back %s: %w", r.manifest.PackageID, err)
	}
	return nil
}

// stage puts one path's restoration into the transaction.
func (r *resolver) stage(txn *vault.FileTxn, change vaultsync.Change) error {
	expect := resultExpectation(change)
	if !change.ExpectExists {
		return txn.Remove(change.Path, expect)
	}
	data, err := journal.ReadSnapshot(noteops.JournalRoot(r.root), r.attempt.JournalEntry, change.Path)
	if err != nil {
		return err
	}
	// The snapshot was taken before the change and the change only committed
	// because the path held what the producer believed, so these two must agree.
	// They disagreeing means this is not the entry the attempt names, and writing
	// the bytes anyway would restore some other operation's idea of the path.
	if got := vaultsync.HashBytes(data); got != change.ExpectSHA256 {
		return fmt.Errorf("%w: entry %s holds %s for %q, the package replaced %s",
			ErrStrandedImport, r.attempt.JournalEntry, got, change.Path, change.ExpectSHA256)
	}
	return txn.Write(change.Path, data, 0, expect)
}

func (r *resolver) changeIndex() map[string]vaultsync.Change {
	index := make(map[string]vaultsync.Change, len(r.operation.Changes))
	for _, change := range r.operation.Changes {
		index[change.Path] = change
	}
	return index
}

// settle writes the one state change that ends an attempt: the pending record is
// dropped, and — when the application stands — the package is remembered as
// applied. Both in a single write, because a crash between two would either
// strand the attempt or forget the application.
func (r *resolver) settle(record *Record) error {
	next := r.state.clone()
	next.Pending = nil
	if record != nil {
		next.Applied[r.manifest.PackageID] = *record
	}
	return saveState(r.root, next)
}

// stranded builds the refusal that keeps every version of the content. It names
// the paths and the journal entry, because those are what a person needs to see
// all of them: the entry's snapshots hold what the vault had, the paths hold what
// is there now, and the package is still in the channel.
func (r *resolver) stranded(action string, paths []string) error {
	return fmt.Errorf("%w: %s cannot be %s because %s no longer holds what the import left; "+
		"the attempt, journal entry %s, and the content are all kept",
		ErrStrandedImport, r.manifest.PackageID, action, strings.Join(paths, ", "), r.attempt.JournalEntry)
}

// pathState is what a path holds, reduced to the three distinctions an import
// cares about. A non-regular file is deliberately not "present with unknown
// content": a directory or a symlink standing where a note was is something no
// change in a package could have produced, so it compares equal to nothing.
type pathState struct {
	present bool
	regular bool
	sha256  string
}

func diskState(abs string) (pathState, error) {
	info, err := os.Lstat(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return pathState{}, nil
	case err != nil:
		return pathState{}, fmt.Errorf("vaultimport: inspect %q: %w", abs, err)
	case !info.Mode().IsRegular():
		return pathState{present: true}, nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return pathState{}, fmt.Errorf("vaultimport: read %q: %w", abs, err)
	}
	return pathState{present: true, regular: true, sha256: vaultsync.HashBytes(data)}, nil
}

// beforeState is what the package expected the path to hold. Manifest.Validate
// has already made ExpectExists and ExpectSHA256 agree, so this cannot describe a
// file with no content.
func beforeState(change vaultsync.Change) pathState {
	if !change.ExpectExists {
		return pathState{}
	}
	return pathState{present: true, regular: true, sha256: change.ExpectSHA256}
}

// afterState is what the package installs at the path.
func afterState(change vaultsync.Change) pathState {
	if change.Action == vaultsync.ActionDelete {
		return pathState{}
	}
	return pathState{present: true, regular: true, sha256: change.ResultSHA256}
}

// resultExpectation is afterState as the precondition a rollback is allowed
// under.
func resultExpectation(change vaultsync.Change) vault.FileExpectation {
	if change.Action == vaultsync.ActionDelete {
		return vault.ExpectMissing()
	}
	return vault.ExpectHash(change.ResultSHA256)
}

// ensureSnapshot records the entry in the vault's content history if it is not
// there already.
func ensureSnapshot(root, entryID string) error {
	needed, err := snapshotNeeded(root, entryID)
	if err != nil {
		return err
	}
	if !needed {
		return nil
	}
	return noteops.SnapshotEntry(root, entryID, operationToken)
}

// snapshotNeeded asks the content history whether the entry's manifest is at the
// tip, which is how this decides whether to commit it again.
//
// The question matters because an entry that deleted a note proves that deletion
// with its own uncommitted record: re-committing an entry whose deletion already
// landed meets a path gone from the working tree *and* gone from HEAD, which is
// the shape internal/vaultgit refuses as an unexplained deletion. Recovering from
// an interruption must not be able to block the vault, so vaultgit answers the
// same question again under its own lock and treats an entry the history already
// holds as nothing to do. This asks anyway, because the answer read here is also
// the one this package reports and reasons about, and because a recovery pass
// that can see there is nothing to do should not take the vault's write lock to
// find out.
//
// A vault with no content history needs no snapshot. A vault whose branch has no
// commits yet needs one: the entry is not in a history that exists, and the
// snapshot is what creates it.
func snapshotNeeded(root, entryID string) (bool, error) {
	ctx := context.Background()
	history, err := vaultgit.OpenHistory(ctx, root, vaultgit.HistoryOptions{})
	if err != nil {
		if vaultgit.IsUnmanaged(err) {
			return false, nil
		}
		return false, fmt.Errorf("vaultimport: open content history: %w", err)
	}
	tip, err := history.Tip(ctx)
	if err != nil {
		if errors.Is(err, vaultgit.ErrUnbornBranch) {
			return true, nil
		}
		return false, fmt.Errorf("vaultimport: resolve history tip: %w", err)
	}
	rel := path.Join(config.DefaultJournalDir, entryID, journal.ManifestFileName)
	kinds, err := history.EntryKinds(ctx, tip, []string{rel})
	if err != nil {
		return false, fmt.Errorf("vaultimport: look up %q in the content history: %w", rel, err)
	}
	return kinds[rel] != "blob", nil
}
