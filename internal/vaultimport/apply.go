package vaultimport

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
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
