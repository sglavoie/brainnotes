package vaultimport

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultexport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// operationToken names an import in this vault's own content history. Like every
// other token in internal/noteops it is a fixed word chosen by the code: it
// reaches a commit message, and a token derived from the package would publish
// the peer's note names into every clone of this vault.
const operationToken = "sync-import"

// defaultCommand is what the undo journal records when a caller names no
// invocation. The journal stays local, so it carries the full command rather
// than the token.
const defaultCommand = "bn sync apply"

// Options carry what applying a package needs from its caller and nothing it can
// work out for itself. The zero value is usable.
type Options struct {
	// Command is the full invocation recorded in the local undo journal, which
	// never leaves this machine. Empty means defaultCommand.
	Command string
	// Now is the clock seam for the receipt's application time.
	Now func() time.Time
	// RecorderOptions customize the undo entry's clock and identity generation.
	// They exist for tests; production callers pass none.
	RecorderOptions []journal.RecorderOption
	// writeState replaces the durable import-state writer for one application.
	// It is an unexported qualification seam; production callers always use nil.
	writeState func(string, []byte, fs.FileMode) error
	// resolving is the resolution this application is the first half of, and is
	// unexported because it is not a caller's to set: it is a plan this package
	// checked against the vault and recorded durably, and an application forced
	// over a conflict by anything else would be one nobody decided.
	resolving *Plan
	// checkpointContext identifies the import-wide barrier admission to nested
	// journal, Git, and export-state writers. Only this package sets it.
	checkpointContext context.Context
}

// forResolution is the application a resolution asks for: the same package, with
// the branches its plan pinned standing in for the producer's expectations.
func (o Options) forResolution(plan *Plan) Options {
	o.resolving = plan
	return o
}

func (o Options) command() string {
	if o.Command == "" {
		return defaultCommand
	}
	return o.Command
}

// now is the clock seam, defaulted the way internal/vaultsync defaults its own.
// It dates the receipt and mints the recovery entry's identity, and those two
// must come from one clock: a test that pins time is pinning both.
func (o Options) now() time.Time {
	if o.Now == nil {
		return time.Now().UTC()
	}
	return o.Now().UTC()
}

func (a *applier) saveState(record *State) error {
	return saveStateWith(a.root, record, a.opts.writeState)
}

// Result is what one applied package left behind here.
//
// It names the local recovery entry as well as the receipt, because the two are
// the same claim seen from either side: the receipt is what the peer is told,
// and the entry is what makes that claim answerable on this machine.
type Result struct {
	// PackageID is the package that was applied.
	PackageID string
	// OperationID is the peer operation it carried, which is what this vault's
	// later changes to the same paths name as their causal predecessor.
	OperationID string
	// OriginDevice is the peer that produced it.
	OriginDevice string
	// JournalEntry is the local recovery-history entry that can undo the
	// application. It was read back off the disk before the receipt was minted.
	JournalEntry string
	// Receipt is this vault's acknowledgment of the package: the one it minted
	// here, or — for a redelivery — the one it minted the first time. It is set
	// whenever an acknowledgment exists, including when publishing it failed, so
	// "this vault never acknowledged the package" and "the acknowledgment could not
	// be put where the peer reads it" stay distinguishable. A converged receipt
	// names no recovery entry because this vault wrote nothing.
	Receipt *vaultsync.Receipt
	// ReceiptPath is where it was published in this device's outgoing channel, and
	// is empty when the publication itself failed.
	ReceiptPath string
	// Cleanup is a housekeeping failure that did not prevent the application: a
	// transaction scratch file that could not be unlinked after its destination
	// was durably written, or the retained branches of a conflict this delivery
	// resolved. It is reported rather than returned as an error because every
	// path the package named holds exactly what it asked for, and the only action
	// it asks for is deleting the file or directory the message names.
	Cleanup error
	// Duplicate reports that this delivery changed nothing because the package
	// had already been applied here. Everything else in the Result describes that
	// earlier application, and the receipt is the one it minted rather than a new
	// one. A caller that counts applied changes must not count this; a caller
	// that forwards receipts still should, because a repeated delivery is usually
	// a peer saying it never got the first reply.
	Duplicate bool
	// Converged reports that this vault already held every change the package
	// carried, so nothing was written and no recovery entry exists. See
	// Record.Converged. JournalEntry is empty and Receipt carries the explicit
	// convergence acknowledgment; the package is remembered as applied so a
	// redelivery republishes that same receipt rather than re-inspecting the vault.
	Converged bool
	// AbandonedResolutions names the conflict decisions this call's recovery pass
	// dropped on the way in, with the reason for each — see
	// Recovery.AbandonedBecause, which is the same map. It is about other work
	// than this package and is carried here because the pass that discovered it
	// runs inside Apply, where a caller has no other way to hear about it. It
	// survives nowhere else: the promise is gone from the state file by the time
	// this returns, so a caller that drops the Result drops the only notice that
	// somebody's choice was not kept.
	AbandonedResolutions map[string]error
}

// Apply applies the package published at packageDir to the vault at root, and
// publishes the receipt this device then owes its peer.
//
// It is the whole consumer side in one call, and the order of its five steps is
// the contract rather than an arrangement: accept before reading, read before
// touching the vault, commit before recording provenance, record provenance
// before the snapshot, and the snapshot before the receipt. See doc.go for why
// each of those is the only safe direction.
//
// A package this vault disagrees with is refused as ErrConflict with the
// contested content untouched; a package this vault could not have accepted is
// refused under internal/vaultsync's own sentinels, unwrapped.
//
// A non-nil Result returned *with* a non-nil error means exactly one thing and
// only ever arises from the last step: the package is applied, committed and
// undoable, and the acknowledgment the peer is owed could not be minted or
// published. Result.JournalEntry is what undoes it. Result.Receipt is set when
// minting succeeded, while an empty Result.ReceiptPath says the receipt never
// reached the channel. A caller that treats every error as "nothing happened"
// will strand a real change; a caller that ignores the error will tell a peer
// about a receipt that was never published.
//
// Earlier failures that also leave the change on disk return a nil Result, and
// the two worth naming carry the entry identity in the error instead:
// recordProvenance names the entry, the operation and the origin device, and a
// *noteops.SnapshotError names the entry and says in so many words that the
// change is on disk and nothing was rolled back. The rest of finalize's failures
// — an operation that captured nothing, a manifest the journal could not write —
// name a path or nothing at all, but they are the defensive ones: a non-empty
// package always captures, so reaching them means the recovery history itself is
// failing, and the recovery history is what a Result would have pointed at.
// Acknowledgment is the one *ordinary* failure that would otherwise say nothing
// about what it left behind, which is why it is the one that carries a Result.
//
// Two things happen before the package is looked at, and both are about earlier
// runs rather than this one. An interrupted application is resolved first, so no
// vault ever holds two unfinished imports at once and so the paths this package
// is about are in a settled state before its preconditions are checked; an
// attempt that cannot be resolved refuses the import and says why, because
// applying a second package over an unexplained one would bury the evidence. And
// a package this vault has already applied is answered from the record of that
// application instead of being applied again — see answer. A conflict decision
// that pass had to drop is reported on Result.AbandonedResolutions, because
// dropping one is not a failure of this import and must not refuse it.
func Apply(root, packageDir string, opts Options) (*Result, error) {
	ctx, release, acquired, err := lockImports(root)
	if err != nil {
		return nil, err
	}
	defer release()
	if !acquired {
		return nil, fmt.Errorf("%w: %q was not applied", ErrImportBusy, packageDir)
	}
	opts.checkpointContext = ctx
	recovery, err := recoverLocked(root, opts)
	if err != nil {
		return nil, err
	}
	result, err := applyLocked(root, packageDir, opts)
	// Only onto a Result, and only here. Drain reaches applyLocked with a Recovery
	// of its own already in hand, so setting this any deeper would report the same
	// abandonment twice; and a failed application has nothing to hang it on, which
	// is why Recover is documented as the authority and this as the convenience.
	if result != nil {
		result.AbandonedResolutions = recovery.AbandonedBecause
	}
	return result, err
}

// applyLocked is the application itself, with the import guard held and any
// earlier attempt already resolved.
func applyLocked(root, packageDir string, opts Options) (*Result, error) {
	session, err := accept(root, packageDir, opts)
	if err != nil {
		return nil, err
	}
	if record, applied := session.state.Applied[session.manifest.PackageID]; applied {
		return session.answer(record)
	}
	if err := session.read(packageDir); err != nil {
		return nil, err
	}
	converged, err := session.decide()
	if err != nil {
		return nil, err
	}
	if converged {
		return session.converge()
	}
	entry, err := session.apply()
	if err != nil {
		return nil, session.retain(session.unwind(err))
	}
	result, ackErr := session.acknowledge(entry)
	importCrash(crashAcknowledged)
	if err := session.settle(entry.ID, result.Receipt); err != nil {
		return result, errors.Join(ackErr, err)
	}
	result.Cleanup = session.cleanup
	return result, ackErr
}

// decide is everything between reading the package and writing anything: what
// this disk disagrees with, what a resolution was planned to overrule, and which
// paths are already at the result. It reports whether there is anything left for
// a transaction to do.
//
// It is one step rather than four inline ones because every one of them is a
// refusal that must happen before a destination is touched, and keeping them
// together is what makes that ordering readable at the call site.
func (a *applier) decide() (bool, error) {
	found, err := a.inspect()
	if err != nil {
		return false, err
	}
	if len(found.conflicts) > 0 {
		if err := a.contested(found); err != nil {
			return false, err
		}
	}
	a.mark(found)
	if err := a.concede(); err != nil {
		return false, err
	}
	return found.settled() || a.settled(), nil
}

// decline holds a package whose changes this vault disagrees with, and returns
// the refusal naming every contested path.
//
// The hold is durable and the refusal is not, which is the asymmetry that
// matters: a caller sees the message once, and the next pass — in another
// process, after a reboot — has to be able to see the same thing. A hold that
// could not be written down is joined onto the refusal rather than replacing it,
// because the package is refused either way and the contested content is still
// untouched.
func (a *applier) decline(found *disposition) error {
	refusal := found.refusal(a.manifest.PackageID)
	if err := a.hold(found); err != nil {
		return errors.Join(refusal, err)
	}
	return refusal
}

// contested decides what to do about the conflicts inspect found: an ordinary
// import declines, and a resolution proceeds over exactly the ones it was
// planned against.
//
// The comparison is not a formality. A resolution is applied with the
// producer's expectations replaced by the branches its plan pinned, which is
// what lets the peer's version land over content it did not expect — so a plan
// that no longer describes this vault would be a licence to overwrite whatever
// is there now. Resolve checks the same facts before it starts; this is the
// check made again with the package read and the disk inspected, which is the
// last point before a transaction exists.
func (a *applier) contested(found *disposition) error {
	if a.opts.resolving == nil {
		return a.decline(found)
	}
	return a.opts.resolving.covers(found.conflicts)
}

// concede marks the contested paths a resolution has nothing to do about.
//
// One kind reaches here: the source of a divergent rename, which is contested
// precisely because it already holds what the operation leaves it holding —
// nothing. Staging it would ask the transaction to remove a file that is not
// there. The question is asked of every path rather than of that kind, because
// "the disk is already at the result" is the general statement and the rename is
// only the case that makes it true today.
func (a *applier) concede() error {
	if a.opts.resolving == nil {
		return nil
	}
	for i := range a.changes {
		if a.changes[i].converged {
			continue
		}
		state, err := diskState(a.abs(a.changes[i].change.Path))
		if err != nil {
			return err
		}
		if state == afterState(a.changes[i].change) {
			a.changes[i].converged = true
		}
	}
	return nil
}

// settled reports a resolution whose application has nothing left to stage,
// which disposition.settled cannot answer because it was decided before concede
// ran.
func (a *applier) settled() bool {
	if a.opts.resolving == nil {
		return false
	}
	for _, planned := range a.changes {
		if !planned.converged {
			return false
		}
	}
	return true
}

// mark records which changes need no transaction, so plan stages the rest and
// declare writes down what a recovery must not roll back.
func (a *applier) mark(found *disposition) {
	for i := range a.changes {
		a.changes[i].converged = found.converged[a.changes[i].change.Path]
	}
}

// converge acknowledges and remembers a package this vault had already applied
// the whole of, without applying anything.
//
// It is recorded as applied because that is what it is: every path the package
// names holds exactly what the package would have installed. Its receipt says
// Converged instead of naming a recovery entry, because noteops correctly made
// no entry for an operation that captured nothing. The path observations still
// have to cover the package exactly before that receipt can be published.
func (a *applier) converge() (*Result, error) {
	result, acknowledged, ackErr := a.acknowledgeConvergence()
	if !acknowledged {
		return nil, ackErr
	}
	if err := a.rememberConvergence(result.Receipt); err != nil {
		return result, errors.Join(ackErr, err)
	}
	result.Cleanup = a.cleanup
	return result, ackErr
}

func (a *applier) rememberConvergence(receipt *vaultsync.Receipt) error {
	next := a.state.clone()
	next.Applied[a.manifest.PackageID] = Record{
		OperationID:  a.operation.OperationID,
		OriginDevice: a.manifest.OriginDevice,
		Sequence:     a.manifest.Sequence,
		Converged:    true,
		Receipt:      receipt,
	}
	released := a.release(next)
	if err := a.saveState(next); err != nil {
		return err
	}
	a.state = next
	a.discard(released)
	return nil
}

// applier is one package's journey through this vault, from accepted manifest to
// published receipt.
type applier struct {
	root      string
	opts      Options
	local     *vaultsync.Enrollment
	state     *State
	manifest  *vaultsync.Manifest
	operation *vaultsync.Operation
	changes   []plannedChange
	// cleanup is a scratch file the transaction could not unlink after the
	// destination it belonged to was durably written. See Result.Cleanup.
	cleanup error
}

// plannedChange is one change with its payload bytes already read and verified,
// or no bytes at all when it is a tombstone.
type plannedChange struct {
	change vaultsync.Change
	data   []byte
	// converged means this path already holds what the change installs, so the
	// transaction skips it. It is still read back and acknowledged: the receipt
	// states what the vault holds, and it holds this. See applier.mark.
	converged bool
}

// accept answers the two questions that must be settled before this vault is
// touched: may this package be applied here at all, and is it one this applier
// can apply as a single local operation.
func accept(root, packageDir string, opts Options) (*applier, error) {
	local, err := vaultsync.Load(root)
	if err != nil {
		return nil, err
	}
	manifest, err := vaultsync.Accept(local, packageDir)
	if err != nil {
		return nil, err
	}
	operation, err := singleOperation(manifest)
	if err != nil {
		return nil, err
	}
	state, err := loadState(root, local)
	if err != nil {
		return nil, err
	}
	return &applier{
		root:      root,
		opts:      opts,
		local:     local,
		state:     state,
		manifest:  manifest,
		operation: operation,
	}, nil
}

// singleOperation returns the one operation the package carries.
//
// An empty package cannot reach here — Manifest.Validate refuses one — so the
// zero case is a second statement of that refusal rather than a live branch, and
// it is stated because the alternative is a nil dereference if that ever changes.
func singleOperation(m *vaultsync.Manifest) (*vaultsync.Operation, error) {
	switch len(m.Operations) {
	case 1:
		return &m.Operations[0], nil
	case 0:
		return nil, fmt.Errorf("%w: %s carries no operation", vaultsync.ErrMalformedPackage, m.PackageID)
	default:
		return nil, fmt.Errorf("%w: %s carries %d", ErrMultipleOperations, m.PackageID, len(m.Operations))
	}
}

// read loads every payload the operation needs, before the vault is touched.
//
// All of them, and up front. A package is applied as one transaction, so bytes
// that turn out to be unreadable halfway through would be discovered with some
// destinations already staged; reading here makes an incomplete delivery an
// ErrIncompletePackage that changed nothing. vaultsync.ReadPayload re-verifies
// each digest as it reads rather than trusting the VerifyPayloads that Accept
// just ran, because the transport is still writing into this directory.
func (a *applier) read(packageDir string) error {
	changes := make([]plannedChange, 0, len(a.operation.Changes))
	for _, change := range a.operation.Changes {
		planned := plannedChange{change: change}
		if change.Action == vaultsync.ActionWrite {
			data, err := vaultsync.ReadPayload(packageDir, a.manifest, change.Payload)
			if err != nil {
				return err
			}
			planned.data = data
		}
		changes = append(changes, planned)
	}
	a.changes = changes
	return nil
}

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

// retryConvergedAcknowledge answers a redelivery whose first convergence
// acknowledgment could not be published. It re-observes every package path,
// so a later edit refuses the new claim instead of being described as the state
// the package originally found.
func (a *applier) retryConvergedAcknowledge(record Record) (*Result, error) {
	result, acknowledged, ackErr := a.acknowledgeConvergence()
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

// plan builds the transaction: one Write or Remove per change, each carrying the
// producer's belief about the path as the precondition, plus the configuration
// layers pinned for the duration.
func (a *applier) plan() (*vault.FileTxn, error) {
	txn, err := vault.NewFileTxn(a.root)
	if err != nil {
		return nil, err
	}
	// Keyed on the folded name rather than the literal one. The Mac side of a
	// Mac/Pi pair is case-insensitive, so a package naming `BrainNotes.toml` is
	// writing the same file the pin would require unchanged — and a literal
	// comparison would arrange for the transaction to do both and conflict with
	// itself, refusing that package on that replica forever. vaultsync decides
	// path identity through CollisionKey everywhere else; this is the same
	// question, so it gets the same answer.
	//
	// The key is folded, not resolved, so it is deliberately the safe side of an
	// asymmetry rather than the exact answer. On a case-sensitive replica
	// `BrainNotes.toml` and `brainnotes.toml` really are two files, and this drops
	// a pin it could have kept: that import runs with the portable layer
	// unpinned. Weakening one guarantee for one exotic package beats refusing
	// every such package outright on the other replica, and the vault cannot be
	// asked which kind of filesystem it is on without a probe that writes.
	written := make(map[string]struct{}, len(a.changes))
	for _, planned := range a.changes {
		change := planned.change
		// A change this vault had already made stages nothing. Requiring it to
		// hold what it holds would be harmless; writing it would not be, because
		// the precondition is the producer's *baseline* and the path is already
		// past it, so the transaction would refuse a package there is nothing
		// left to do about. It is also left out of `written`, so a configuration
		// layer that converged is pinned rather than exempted.
		if planned.converged {
			continue
		}
		written[vaultsync.CollisionKey(change.Path)] = struct{}{}
		if change.Action == vaultsync.ActionDelete {
			err = txn.Remove(change.Path, a.expectation(change))
		} else {
			// A zero mode derives a new file's permissions from this machine's
			// umask and preserves an existing file's. Neither is carried in the
			// package: a mode is a fact about the device that holds the file, and
			// replicating one would let a peer's umask decide this vault's.
			err = txn.Write(change.Path, planned.data, 0, a.expectation(change))
		}
		if err != nil {
			return nil, fmt.Errorf("vaultimport: plan %q: %w", change.Path, err)
		}
	}
	if err := pinConfiguration(a.root, txn, written); err != nil {
		return nil, err
	}
	return txn, nil
}

// expectation is the precondition one change is staged under: the producer's
// belief about the path, or the branch a resolution pinned in its place.
//
// A resolution is the one thing entitled to overrule the producer, and it
// overrules it with a reading of this vault rather than with permission to write
// anything. The pin is the version somebody looked at when they chose, so the
// precondition still fails — under the path's own lock, with the content intact
// — if the note moved between the choice and the application. See Plan.pin.
func (a *applier) expectation(change vaultsync.Change) vault.FileExpectation {
	if a.opts.resolving != nil {
		if pinned, found := a.opts.resolving.pin(change.Path); found {
			return pinned
		}
	}
	return expectation(change)
}

// expectation turns the producer's belief about a path into the precondition the
// transaction verifies under that path's own lock.
//
// It is carried rather than recomputed, which is the whole reason a package
// states it: a path holding something else means two devices edited one note
// from one starting point, and only the producer knows what that starting point
// was. Manifest.Validate has already made ExpectExists and ExpectSHA256 agree,
// so ExpectHash here never degrades to "exists with any content".
func expectation(change vaultsync.Change) vault.FileExpectation {
	if !change.ExpectExists {
		return vault.ExpectMissing()
	}
	return vault.ExpectHash(change.ExpectSHA256)
}

// pinConfiguration requires both configuration layers to hold exactly what they
// hold now for the whole commit.
//
// This is session 3's composite pin applied to an import. The vault's
// declarations decide what the applied content means — which properties exist,
// what a note is required to carry — so a configuration edited between the plan
// and the commit would leave a set of notes behind under rules that never
// governed them.
//
// What this package supplies is the pin; when it is checked belongs to
// internal/vault, which verifies every requirement at staging and again before
// and after each individual change. The first of those is the door these pins
// are refused at in practice, and it costs nothing — staging happens before any
// destination is touched. The later re-checks are what make an edit that lands
// mid-commit a rollback rather than a half-governed vault, and they are
// internal/vault's guarantee rather than a behaviour this package arranges.
//
// The portable layer is skipped when the package changes it, because a
// transaction cannot both write a path and require it to stay unchanged. The
// overlay never can be: it is machine-local and vaultsync.Classify calls it
// private, so it is pinned unconditionally — including its absence, so an
// overlay created mid-import stops the commit rather than silently taking effect
// over it.
func pinConfiguration(root string, txn *vault.FileTxn, written map[string]struct{}) error {
	sources, err := config.ReadSources(root)
	if err != nil {
		return fmt.Errorf("vaultimport: read configuration: %w", err)
	}
	fingerprint := sources.Fingerprint()
	pins := []struct {
		path   string
		expect vault.FileExpectation
	}{
		{config.FileName, vault.ExpectHash(fingerprint.Portable)},
		{config.LocalFileName, overlayExpectation(fingerprint)},
	}
	for _, pin := range pins {
		if _, changed := written[vaultsync.CollisionKey(pin.path)]; changed {
			continue
		}
		if err := txn.Require(pin.path, pin.expect); err != nil {
			return fmt.Errorf("vaultimport: pin %q: %w", pin.path, err)
		}
	}
	return nil
}

// overlayExpectation pins the machine-local layer, distinguishing absent from
// present-and-empty the way config.Fingerprint does.
func overlayExpectation(fingerprint config.Fingerprint) vault.FileExpectation {
	if !fingerprint.LocalPresent {
		return vault.ExpectMissing()
	}
	return vault.ExpectHash(fingerprint.Local)
}

// commit applies the transaction and classifies what came back.
//
// The order of these arms is the classification rule the rest of this repository
// states and follows — noteops.frontmatterApplyError, noteops.captureShouldReplan,
// internal/web's mutation actions — and it is an *ordering* rather than a set.
// Two of the types nest: *vault.TxnRollbackError wraps the original commit
// failure and *vault.TxnRecoveryError wraps a join containing it, so
// IsTxnConflict — an errors.As predicate — answers yes to both. Asking that broad
// question first would report a half-applied package whose rollback failed as
// "nothing changed", and would bury the one message naming the temp file that
// holds another writer's only copy of a note. The cleanup and lock-busy types do
// not nest that way and would classify correctly in any order; they are grouped
// here so the whole rule reads in one place rather than in two.
//
// A *vault.TxnCleanupError is the one failure that is not a failure of the
// import, and it is stated first. It means every destination durably holds exactly
// what the package asked for and a scratch file beside one of them could not be
// unlinked: Commit returns one only from its final line, after every change
// applied and every parent directory was fsynced, so it cannot mean less than
// that. The interactive callers of Commit report it as a failed mutation, which
// is right for a command a person reruns. It is wrong here, because stopping
// would leave the peer's bytes on disk with no undo manifest, no snapshot, and
// no provenance record — the one state this package exists to make impossible —
// over a file that is safe to delete. The import goes on and the housekeeping
// problem is carried out to the caller.
//
// Displaced content, a failed rollback, and a lock that stayed busy are each
// returned as they are. The first two carry recovery instructions a person has
// to act on; the third means nothing was read, staged, or written, which is the
// same retryable contention as vaultgit's busy preflight, and calling it a
// conflict would invite somebody to resolve one that does not exist.
//
// A plain precondition failure is last and is the safe one: staging verifies
// every expectation before a single destination is touched, so the contested
// content and the content history are exactly as they were. One caveat the
// sentinel cannot carry — a conflict raised *after* staging is compensated back
// but has already captured the journal, so an un-finalized entry directory can
// survive it. See the package documentation.
func (a *applier) commit(txn *vault.FileTxn, operation *noteops.Journal) error {
	cleanup, refusal := classifyCommit(a.manifest.PackageID, txn.Commit(operation))
	a.cleanup = cleanup
	return refusal
}

// classifyCommit is the ordering itself, separated from the transaction so it
// can be exercised against each error type directly. Provoking all five through a
// live FileTxn would mean arranging five different filesystem failures, and the
// two that actually depend on the order are the two hardest of them.
//
// It returns the housekeeping failure to carry and the refusal to report, and at
// most one of the two is ever non-nil.
func classifyCommit(packageID string, err error) (cleanup, refusal error) {
	switch {
	case err == nil:
		return nil, nil
	case vault.IsTxnCleanupFailure(err):
		return err, nil
	case vault.IsTxnRecoveryNeeded(err), vault.IsTxnRollbackFailure(err), vault.IsTxnLockBusy(err):
		return nil, err
	case vault.IsTxnConflict(err):
		return nil, fmt.Errorf("%w: %s: %v", ErrConflict, packageID, err)
	default:
		return nil, err
	}
}

// recordProvenance writes the peer operation against the local journal entry
// that applying it produced, before that entry is finalized and committed.
//
// The ordering is vaultexport.RecordImport's documented contract and it is
// one-directional. Written after the commit, a crash in between leaves a commit
// the exporter reads as this device's own work and publishes straight back to
// the peer that sent it. Written before, the worst case is a ledger entry for a
// journal entry that never commits, which the exporter never looks up.
//
// The identity itself is knowable earlier — this package mints it and pins it
// onto the recorder before the transaction starts, because the attempt record
// has to name it before the first byte moves. What is only knowable here is
// whether there is an entry at all: noteops creates the recorder on the first
// Capture, so EntryID answers empty for an operation that changed nothing, and
// reading it after Commit is how this learns the application produced a real
// entry rather than assuming the one it named was used.
func (a *applier) recordProvenance(operation *noteops.Journal) error {
	entryID := operation.EntryID()
	if entryID == "" {
		return fmt.Errorf("vaultimport: applying %s captured no recovery entry", a.manifest.PackageID)
	}
	err := vaultexport.RecordImportContext(a.opts.checkpointContext, a.root, entryID, vaultexport.Import{
		OperationID:  a.operation.OperationID,
		OriginDevice: a.manifest.OriginDevice,
	})
	if err == nil {
		return nil
	}
	// Past the point where the peer's bytes are on this disk. Leaving them
	// outside the recovery history is the worse of the two failures — nothing
	// could undo them — so the entry is finalized anyway and both facts are
	// reported. No receipt is minted, so the peer keeps its own copy.
	//
	// This is the one branch here that ends in a state only a person can put
	// right: the change is applied, committed and undoable, and the ledger does
	// not know it came from a peer, so the next export publishes the peer's own
	// change back to it and the origin sees a spurious conflict on a change it
	// authored. The repair is one RecordImport call, so the error names every
	// argument it needs — the entry, the operation, and the device — rather than
	// leaving them to be reconstructed from a journal listing.
	_, finalizeErr := operation.Finalize()
	return errors.Join(
		fmt.Errorf("vaultimport: record import provenance for %s: entry %s applied operation %s from %s: %w",
			a.manifest.PackageID, entryID, a.operation.OperationID, a.manifest.OriginDevice, err),
		finalizeErr)
}

// finalize writes the undo manifest, takes the Git snapshot, and proves the
// entry is on disk.
//
// internal/vaultsync mints a receipt naming a recovery entry and documents that
// it checks the entry is *named* and never that it exists, because it is a leaf
// package and cannot import internal/journal. This is the layer that can, and an
// acknowledgment is a promise the peer acts on by discarding its only copy of a
// change.
//
// Mostly the read-back is defence in depth: a successful snapshot has already
// loaded the entry, because vaultgit.CommitJournalEntry loads it to learn which
// paths to stage. It is strictly load-bearing only where that does not run — a
// vault with no content history has no safety manager, and noteops.snapshot
// returns without committing anything. That vault still hands its peer a receipt
// whose only promise is the entry, so the entry is what gets checked.
func (a *applier) finalize(operation *noteops.Journal) (*journal.Entry, error) {
	entry, err := operation.Finalize()
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, fmt.Errorf("vaultimport: applying %s recorded no recovery entry", a.manifest.PackageID)
	}
	if _, err := journal.LoadEntry(noteops.JournalRoot(a.root), entry.ID); err != nil {
		return nil, fmt.Errorf("%w: %s names %s: %v",
			vaultsync.ErrNoRecoveryEntry, a.manifest.PackageID, entry.ID, err)
	}
	return entry, nil
}

// acknowledge mints and publishes the receipt this device owes for having
// applied the package.
//
// Every failure here returns the Result *alongside* the error, which is the only
// honest shape past this point. The package is applied, committed and undoable
// by the time this runs; failing with a bare error would tell a caller that
// nothing happened, and would withhold the entry identity — the one thing that
// makes what did happen reversible. Receipt being non-nil with an empty
// ReceiptPath says the statement was minted and retained but not published;
// Receipt being nil says it could not be minted at all.
//
// A receipt already in the channel is adopted rather than replaced, and that
// short-circuit is checked first, ahead of reading the paths back. It is what
// makes an interruption between publishing the receipt and remembering it
// survivable: publication is the moment the peer may drop its own copy, so by the
// time a recovery pass runs the statement has been made and cannot be revised.
// Re-minting would fail on the immutable name at best, and at worst would want to
// describe paths a person has edited since — which is a question this vault has
// already answered and must not answer twice.
func (a *applier) acknowledge(entry *journal.Entry) (*Result, error) {
	return a.acknowledgeWith(entry, a.observe)
}

// acknowledgeRecorded completes an old applied record whose first attempt did
// not retain the receipt it minted. Paths the application actually wrote are
// proven by the finalized recovery entry, whose post-command states predate any
// later package. A path absent from that entry was already converged and was not
// written, so the only proof an old record retains for it is the live vault; it
// is observed again and a later edit still refuses the acknowledgment.
func (a *applier) acknowledgeRecorded(entry *journal.Entry) (*Result, error) {
	return a.acknowledgeWith(entry, func() ([]vaultsync.AppliedPath, error) {
		return a.recordedApplication(entry)
	})
}

func (a *applier) acknowledgeWith(entry *journal.Entry, appliedPaths func() ([]vaultsync.AppliedPath, error)) (*Result, error) {
	result := &Result{
		PackageID:    a.manifest.PackageID,
		OperationID:  a.operation.OperationID,
		OriginDevice: a.manifest.OriginDevice,
		JournalEntry: entry.ID,
		Cleanup:      a.cleanup,
	}
	published, path, channelErr := a.adopt(entry)
	if published != nil {
		result.Receipt = published
		result.ReceiptPath = path
		return result, nil
	}
	applied, err := appliedPaths()
	if err != nil {
		return result, errors.Join(channelErr, err)
	}
	receipt, err := vaultsync.Acknowledge(a.local, a.manifest, vaultsync.Application{
		RecoveryEntry: entry.ID,
		Applied:       applied,
		Now:           a.opts.Now,
	})
	if err != nil {
		return result, errors.Join(channelErr, err)
	}
	result.Receipt = receipt
	if channelErr != nil {
		return result, channelErr
	}
	path, err = vaultsync.PublishReceipt(vaultsync.OutgoingDir(a.root), receipt)
	result.ReceiptPath = path
	if err != nil {
		return result, err
	}
	return result, nil
}

// acknowledgeConvergence publishes the distinct receipt for a package whose
// complete result was already present. It makes no recovery-entry promise, but
// it is held to the same read-back and exact-coverage checks as an application
// that wrote the package.
func (a *applier) acknowledgeConvergence() (*Result, bool, error) {
	result := &Result{
		PackageID:    a.manifest.PackageID,
		OperationID:  a.operation.OperationID,
		OriginDevice: a.manifest.OriginDevice,
		Converged:    true,
		Cleanup:      a.cleanup,
	}
	published, path, err := a.adoptConvergence()
	if err != nil {
		// A blocked or damaged channel does not erase the fact that every package
		// result is already present. Prove that fact independently before returning
		// the same applied-with-bookkeeping-error shape an ordinary import uses.
		receipt, proofErr := a.mintConvergence()
		if proofErr != nil {
			return result, false, errors.Join(err, proofErr)
		}
		result.Receipt = receipt
		return result, true, err
	}
	if published != nil {
		result.Receipt = published
		result.ReceiptPath = path
		return result, true, nil
	}
	receipt, err := a.mintConvergence()
	if err != nil {
		return result, false, err
	}
	result.Receipt = receipt
	path, err = vaultsync.PublishReceipt(vaultsync.OutgoingDir(a.root), receipt)
	result.ReceiptPath = path
	if err != nil {
		return result, true, err
	}
	return result, true, nil
}

func (a *applier) mintConvergence() (*vaultsync.Receipt, error) {
	applied, err := a.observe()
	if err != nil {
		return nil, err
	}
	return vaultsync.Acknowledge(a.local, a.manifest, vaultsync.Application{
		Converged: true,
		Applied:   applied,
		Now:       a.opts.Now,
	})
}

// adopt returns the receipt this device has already published for the package,
// with the path it sits at, or nil when the channel holds none.
//
// The channel is consulted rather than the ledger because the two can disagree in
// exactly one direction: PublishReceipt makes the receipt durable, and the write
// that records it in the ledger is a separate event. A process that died between
// them left a statement the peer can read and a vault that does not know it made
// one, and only the channel can say so.
//
// It is checked before adopting, twice. A receipt naming another device is not
// this vault's to stand behind, and one naming another recovery entry would mean
// the package was applied twice under two identities — neither is something to
// hand back as "already acknowledged", and both are refused loudly rather than
// republished. ReadReceipt has already checked the format, the identities, and
// that the package_id inside matches the name it was found under.
func (a *applier) adopt(entry *journal.Entry) (*vaultsync.Receipt, string, error) {
	channel := vaultsync.OutgoingDir(a.root)
	receipt, err := vaultsync.ReadReceipt(channel, a.manifest.PackageID)
	if errors.Is(err, vaultsync.ErrIncompletePackage) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	if receipt.Device != a.local.DeviceID {
		return nil, "", fmt.Errorf("%w: the receipt for %s in this vault's own channel was published by %s",
			vaultsync.ErrReceiptMismatch, a.manifest.PackageID, receipt.Device)
	}
	if receipt.RecoveryEntry != entry.ID {
		return nil, "", fmt.Errorf("%w: %s was acknowledged under recovery entry %s and is now %s",
			vaultsync.ErrReceiptMismatch, a.manifest.PackageID, receipt.RecoveryEntry, entry.ID)
	}
	path, err := vaultsync.ReceiptPath(channel, a.manifest.PackageID)
	if err != nil {
		return nil, "", err
	}
	return receipt, path, nil
}

// adoptConvergence is the crash-recovery half of acknowledgeConvergence. A
// receipt may be durable before the import ledger remembers it, so a repeated
// pass must adopt that exact statement rather than try to replace its immutable
// channel name.
func (a *applier) adoptConvergence() (*vaultsync.Receipt, string, error) {
	channel := vaultsync.OutgoingDir(a.root)
	receipt, err := vaultsync.ReadReceipt(channel, a.manifest.PackageID)
	if errors.Is(err, vaultsync.ErrIncompletePackage) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	if receipt.Device != a.local.DeviceID {
		return nil, "", fmt.Errorf("%w: the receipt for %s in this vault's own channel was published by %s",
			vaultsync.ErrReceiptMismatch, a.manifest.PackageID, receipt.Device)
	}
	if !receipt.Converged || strings.TrimSpace(receipt.RecoveryEntry) != "" {
		return nil, "", fmt.Errorf("%w: %s converged but its receipt names recovery entry %s",
			vaultsync.ErrReceiptMismatch, a.manifest.PackageID, receipt.RecoveryEntry)
	}
	path, err := vaultsync.ReceiptPath(channel, a.manifest.PackageID)
	if err != nil {
		return nil, "", err
	}
	return receipt, path, nil
}

// observe reports what each of the package's paths holds now, read back off this
// disk.
//
// Every hash here is computed from the file rather than copied from the change
// that asked for it. Acknowledge compares the two and refuses a mismatch, and a
// receipt assembled from the manifest would make that comparison compare the
// package against itself — an acknowledgment that is true by construction and
// says nothing about this vault.
//
// A tombstone is checked the only way it can be: the path has to be gone.
// Acknowledge can only compare an empty hash against an empty hash, so without
// this a delete that silently did not happen would still be acknowledged.
func (a *applier) observe() ([]vaultsync.AppliedPath, error) {
	applied := make([]vaultsync.AppliedPath, 0, len(a.changes))
	for _, planned := range a.changes {
		entry, err := a.observeChange(planned.change)
		if err != nil {
			return nil, err
		}
		applied = append(applied, entry)
	}
	return applied, nil
}

func (a *applier) observeChange(change vaultsync.Change) (vaultsync.AppliedPath, error) {
	entry := vaultsync.AppliedPath{Path: change.Path, Action: change.Action}
	if change.Action == vaultsync.ActionDelete {
		if err := requireAbsent(a.abs(change.Path)); err != nil {
			return vaultsync.AppliedPath{}, err
		}
		return entry, nil
	}
	data, err := os.ReadFile(a.abs(change.Path))
	if err != nil {
		return vaultsync.AppliedPath{}, fmt.Errorf("vaultimport: read back %q: %w", change.Path, err)
	}
	entry.SHA256 = vaultsync.HashBytes(data)
	return entry, nil
}

// recordedApplication reconstructs the proof an older nil-receipt record did
// not keep. The recovery entry is allowed to cover a subset of the package: a
// partially converged import journals only the paths it writes. Every journaled
// path must belong to this package and occur once. Unjournaled paths are read
// from the live vault, preserving the old fail-closed behavior for evidence the
// record never retained.
func (a *applier) recordedApplication(entry *journal.Entry) ([]vaultsync.AppliedPath, error) {
	changed := make(map[string]vaultsync.Change, len(a.changes))
	for _, planned := range a.changes {
		changed[planned.change.Path] = planned.change
	}
	recorded, err := recordedStates(entry, a.manifest.PackageID, changed)
	if err != nil {
		return nil, err
	}

	applied := make([]vaultsync.AppliedPath, 0, len(a.changes))
	for _, planned := range a.changes {
		change := planned.change
		after, found := recorded[change.Path]
		if !found {
			observed, err := a.observeChange(change)
			if err != nil {
				return nil, err
			}
			applied = append(applied, observed)
			continue
		}
		outcome, err := recordedOutcome(entry.ID, change, after)
		if err != nil {
			return nil, err
		}
		applied = append(applied, outcome)
	}
	return applied, nil
}

func recordedStates(entry *journal.Entry, packageID string, changed map[string]vaultsync.Change) (map[string]journal.FileState, error) {
	recorded := make(map[string]journal.FileState, len(entry.Files))
	for _, file := range entry.Files {
		if _, belongs := changed[file.Path]; !belongs {
			return nil, fmt.Errorf("%w: recovery entry %s includes %q, which package %s did not change",
				vaultsync.ErrIncompleteApplication, entry.ID, file.Path, packageID)
		}
		if _, duplicate := recorded[file.Path]; duplicate {
			return nil, fmt.Errorf("%w: recovery entry %s records %q twice",
				vaultsync.ErrIncompleteApplication, entry.ID, file.Path)
		}
		recorded[file.Path] = file.After
	}
	return recorded, nil
}

func recordedOutcome(entryID string, change vaultsync.Change, after journal.FileState) (vaultsync.AppliedPath, error) {
	outcome := vaultsync.AppliedPath{Path: change.Path, Action: change.Action}
	if change.Action == vaultsync.ActionDelete {
		if after.Kind != journal.KindMissing {
			return vaultsync.AppliedPath{}, fmt.Errorf("%w: recovery entry %s says %q ended as %s after deletion",
				vaultsync.ErrIncompleteApplication, entryID, change.Path, after.Kind)
		}
		return outcome, nil
	}
	if after.Kind != journal.KindFile {
		return vaultsync.AppliedPath{}, fmt.Errorf("%w: recovery entry %s says %q ended as %s after a write",
			vaultsync.ErrIncompleteApplication, entryID, change.Path, after.Kind)
	}
	outcome.SHA256 = after.SHA256
	return outcome, nil
}

// requireAbsent reports whether a tombstoned path is really gone. Lstat rather
// than Stat: a dangling symlink left at the path is still something occupying
// the name, and a receipt saying the note was removed would be wrong.
func requireAbsent(abs string) error {
	_, err := os.Lstat(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("vaultimport: check %q was removed: %w", abs, err)
	default:
		return fmt.Errorf("%w: %q still exists after its tombstone was applied",
			vaultsync.ErrIncompleteApplication, abs)
	}
}
