package vaultimport

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// apply puts the package on this disk as one journaled, snapshotted operation
// and returns the recovery entry that can undo it.
//
// The identity of that entry is minted here rather than read off the recorder
// afterwards, and declared before the first byte moves. Everything up to declare
// creates nothing — BeginOperation only refuses, and plan only describes — so
// this is the last instant at which the vault can still be said not to be
// applying anything, and it is where the record that explains an interruption has
// to go. Without it a process that dies inside commit leaves a half-built entry
// under a name nobody wrote down, applied bytes nothing accounts for, and no way
// to tell either from a package that was never delivered.
func (a *applier) apply() (*journal.Entry, error) {
	entryID, err := journal.NewEntryID(a.opts.now())
	if err != nil {
		return nil, fmt.Errorf("vaultimport: mint recovery entry identity for %s: %w",
			a.manifest.PackageID, err)
	}
	operation, err := noteops.BeginOperationContext(a.opts.checkpointContext, a.root, operationToken, a.opts.command(),
		a.targets(), a.opts.recorderOptions(entryID)...)
	if err != nil {
		return nil, err
	}
	defer operation.Release()
	txn, err := a.plan()
	if err != nil {
		return nil, err
	}
	if err := a.declare(entryID); err != nil {
		return nil, err
	}
	importCrash(crashDeclared)
	if err := a.commit(txn, operation); err != nil {
		return nil, err
	}
	importCrash(crashApplied)
	if err := a.recordProvenance(operation); err != nil {
		return nil, err
	}
	importCrash(crashProvenance)
	entry, err := a.finalize(operation)
	if err != nil {
		return nil, err
	}
	importCrash(crashFinalized)
	return entry, nil
}

// recorderOptions pins the entry identity the attempt was declared under.
//
// The pin is appended last and so overrides a generator a caller supplied, which
// is deliberate and is the one place this package overrules its own Options. An
// identity that has been written down before the entry exists is not a choice
// left to anybody: a recorder that minted a different one would build an entry
// the recovery record cannot name, which is exactly the state the record exists
// to prevent. A test that wants a predictable entry pins the clock instead — the
// identity is derived from it.
//
// It is on Options rather than on the applier because a resolution's revision
// declares its own entry the same way and for the same reason, and the two must
// not be able to disagree about what pinning one means.
func (o Options) recorderOptions(entryID string) []journal.RecorderOption {
	opts := make([]journal.RecorderOption, 0, len(o.RecorderOptions)+1)
	opts = append(opts, o.RecorderOptions...)
	return append(opts, journal.WithIDGenerator(func(time.Time) (string, error) {
		return entryID, nil
	}))
}

// declare makes the attempt durable: this vault is about to apply this package,
// under this recovery entry.
func (a *applier) declare(entryID string) error {
	next := a.state.clone()
	next.Pending = &Attempt{
		JournalEntry: entryID,
		Manifest:     a.manifest,
		Converged:    a.convergedPaths(),
	}
	if err := a.saveState(next); err != nil {
		return err
	}
	a.state = next
	return nil
}

// convergedPaths are the paths this application is skipping, in package order.
// Recorded in the attempt because a recovering process cannot work them out: see
// Attempt.Converged.
func (a *applier) convergedPaths() []string {
	paths := make([]string, 0, len(a.changes))
	for _, planned := range a.changes {
		if planned.converged {
			paths = append(paths, planned.change.Path)
		}
	}
	return paths
}

// settle ends the attempt and remembers the application, in one write. See
// resolver.settle: separating the two would let a crash strand the attempt or
// forget the application.
func (a *applier) settle(entryID string, receipt *vaultsync.Receipt) error {
	next := a.state.clone()
	next.Pending = nil
	next.Applied[a.manifest.PackageID] = Record{
		OperationID:  a.operation.OperationID,
		OriginDevice: a.manifest.OriginDevice,
		Sequence:     a.manifest.Sequence,
		JournalEntry: entryID,
		Receipt:      receipt,
	}
	// A package that applies is no longer a package being held. This is the only
	// way a hold is lifted, and it needs no command: a person resolves a conflict
	// by editing the note, the next pass finds the expectation met, and the hold
	// and its retained branches go with the application.
	released := a.release(next)
	if err := a.saveState(next); err != nil {
		return err
	}
	a.state = next
	a.discard(released)
	return nil
}

// unwind resolves the attempt a failed application left behind and returns the
// original failure.
//
// It is the same resolution a crash gets, run in-process, and using one code path
// for both is the point: an ordinary conflict and a power cut leave the vault in
// states that differ only in whether anybody is still there to look, so they must
// converge the same way. It is also what finally cleans up the un-finalized entry
// directory a mid-commit configuration change leaves behind, which internal/
// vaultimport has documented as somebody else's problem until now.
//
// The cause is returned unchanged whatever resolution did, because the caller
// asked about the package: a conflict is still a conflict after the vault tidies
// up. A resolution that *fails* is joined on, since it means something is left
// behind and the next import will refuse until it is dealt with.
func (a *applier) unwind(cause error) error {
	if a.state.Pending == nil {
		return cause
	}
	if _, err := recoverLocked(a.root, a.opts); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

// targets are the absolute paths the operation will change, handed to
// BeginOperation so the storage-location policy refuses before any artifact
// exists rather than underneath the first write.
func (a *applier) targets() []string {
	targets := make([]string, 0, len(a.changes))
	for _, planned := range a.changes {
		targets = append(targets, a.abs(planned.change.Path))
	}
	return targets
}

func (a *applier) abs(rel string) string { return absPath(a.root, rel) }

func absPath(root, rel string) string {
	return filepath.Join(root, filepath.FromSlash(rel))
}

// answer responds to a package this vault has already applied, without applying
// it again.
//
// A redelivery is ordinary, not exceptional. The transport moves whole
// directories and has no idea what a receipt is, so a package reappears whenever
// a channel is re-copied, a peer is restored from a backup, or an acknowledgment
// went missing and the producer tried again. Applying it a second time is not
// merely wasteful — the vault no longer holds what the package expected to find,
// because the package itself changed it, so the second delivery would be reported
// as a conflict between the peer and its own change.
//
// Both halves of that are answered from the ledger. What was applied is reported
// as it was applied, and the acknowledgment the peer is missing is the *original*
// receipt republished rather than a new one minted now. See Record.Receipt for
// why a fresh receipt would be the wrong answer and not just a redundant one.
func (a *applier) answer(record Record) (*Result, error) {
	if record.OperationID != a.operation.OperationID {
		return nil, fmt.Errorf("%w: package %s was applied as operation %s and now carries %s",
			ErrCorruptState, a.manifest.PackageID, record.OperationID, a.operation.OperationID)
	}
	a.withoutPayloads()
	if record.Converged {
		if record.Receipt != nil {
			return a.republish(record)
		}
		return a.retryConvergedAcknowledge(record)
	}
	if record.Receipt != nil {
		return a.republish(record)
	}
	return a.retryAcknowledge(record)
}

// republish puts a receipt this vault already minted back into the outgoing
// channel.
//
// A receipt is immutable and published under a name that can only be taken once,
// so an identical receipt already occupying the name is the state this call was
// trying to reach. The stored receipt may have been minted during a publication
// failure, though, so an occupied name is read back and compared rather than
// assumed to be this record's own.
func (a *applier) republish(record Record) (*Result, error) {
	result := a.duplicate(record)
	// Carried before the publication rather than after it, because the receipt is a
	// fact about the earlier application and not about this delivery: it was minted
	// and it is in the record. A failure below means this vault could not put it
	// back in the channel, which is a different thing from never having had one,
	// and a caller that cannot tell them apart looks for a missing receipt instead
	// of a channel it cannot write. ReceiptPath stays empty, which is the honest
	// statement that the bytes are not where the peer reads them.
	result.Receipt = record.Receipt
	channel := vaultsync.OutgoingDir(a.root)
	path, err := vaultsync.PublishReceipt(channel, record.Receipt)
	result.ReceiptPath = path
	if errors.Is(err, vaultsync.ErrPackageExists) {
		existing, readErr := vaultsync.ReadReceipt(channel, a.manifest.PackageID)
		if readErr != nil {
			return result, readErr
		}
		if !reflect.DeepEqual(existing, record.Receipt) {
			return result, fmt.Errorf("%w: the receipt already published for %s differs from the retained acknowledgment",
				vaultsync.ErrReceiptMismatch, a.manifest.PackageID)
		}
		path, err = vaultsync.ReceiptPath(channel, a.manifest.PackageID)
		result.ReceiptPath = path
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

// retryAcknowledge answers a legacy redelivery of a package that was applied
// before unpublished minted receipts were retained.
//
// Minting now is honest in this one case and only this one: no receipt was ever
// retained, so there is no earlier statement for a new one to contradict. The
// recovery entry proves the paths that application wrote before later packages
// moved them on. Any package path absent from the entry was converged rather
// than written and is still read from the live vault, so missing evidence fails
// closed rather than being filled from the package's own claim.
func (a *applier) retryAcknowledge(record Record) (*Result, error) {
	entry, err := journal.LoadEntry(noteops.JournalRoot(a.root), record.JournalEntry)
	if err != nil {
		return a.duplicate(record), fmt.Errorf("%w: %s names %s: %v",
			vaultsync.ErrNoRecoveryEntry, a.manifest.PackageID, record.JournalEntry, err)
	}
	result, ackErr := a.acknowledgeRecorded(entry)
	result.Duplicate = true
	if result.Receipt == nil {
		return result, ackErr
	}
	if err := a.settle(record.JournalEntry, result.Receipt); err != nil {
		return result, errors.Join(ackErr, err)
	}
	// settle can drop a hold, and dropping one can fail to remove the bytes it had
	// retained. acknowledge built this Result before any of that happened, so the
	// housekeeping failure is copied across here or it would be dropped — the one
	// place in this package where a Result is made before the last thing that can
	// add to Cleanup.
	result.Cleanup = a.cleanup
	return result, ackErr
}

// retryConvergedAcknowledge answers a legacy redelivery whose first convergence
// acknowledgment could not be published and was not retained. A convergence
// record is written only after mintConvergence proved every package result, so
// the record plus the accepted immutable manifest are the durable evidence for
// that earlier observation. Reading today's paths would strand the package once
// a later valid package moved one of them on.
func (a *applier) retryConvergedAcknowledge(record Record) (*Result, error) {
	result, acknowledged, ackErr := a.acknowledgeConvergenceWith(a.mintRecordedConvergence)
	result.Duplicate = true
	if !acknowledged || result.Receipt == nil {
		return result, ackErr
	}
	if err := a.rememberConvergence(result.Receipt); err != nil {
		return result, errors.Join(ackErr, err)
	}
	result.Cleanup = a.cleanup
	return result, ackErr
}

// duplicate is the Result a redelivery reports before anything is said about the
// receipt.
func (a *applier) duplicate(record Record) *Result {
	return &Result{
		PackageID:    a.manifest.PackageID,
		OperationID:  record.OperationID,
		OriginDevice: a.manifest.OriginDevice,
		JournalEntry: record.JournalEntry,
		Duplicate:    true,
		Converged:    record.Converged,
	}
}

// withoutPayloads fills in the per-path changes that reading back and
// acknowledging need, without opening the package.
//
// The payload bytes are deliberately left out. observe hashes what is on this
// disk precisely so that an acknowledgment compares the vault against the
// package; a path carrying the package's own bytes around would be in a position
// to acknowledge those instead, and the comparison would be true by construction.
func (a *applier) withoutPayloads() {
	changes := make([]plannedChange, 0, len(a.operation.Changes))
	for _, change := range a.operation.Changes {
		changes = append(changes, plannedChange{change: change})
	}
	a.changes = changes
}
