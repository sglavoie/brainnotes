package vaultimport

import (
	"context"
	"errors"
	"fmt"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// sweep drops holds for packages that are no longer in the channel, and removes
// every retained branch directory no hold names.
//
// Both halves are needed and they answer different failures. A package that was
// deleted from the channel while it was held leaves a record describing a
// conflict nobody can act on, since the remote branch it names is gone. And a
// hold that was dropped by a process that died before it could remove the
// retained bytes leaves a directory nothing points at. Neither is dangerous;
// both grow without bound, and the record is the thing a person reads.
//
// It does nothing at all when the channel directory is absent, because every
// package is then "not present" and one sweep would drop every hold in the vault
// — over a directory that says only that nothing has ever been delivered here.
func (d *drain) sweep() error {
	if !d.channel {
		return nil
	}
	// Load the enrollment once here because the same identity validates both the
	// import state and the peer retirement promise. Failure to load local state is
	// still a vault failure; only the peer-owned record degrades to housekeeping.
	local, err := vaultsync.Load(d.root)
	if err != nil {
		return err
	}
	state, err := loadState(d.root, local)
	if err != nil {
		return err
	}
	present := make(map[string]struct{}, len(d.pending))
	for _, found := range d.pending {
		present[found.name()] = struct{}{}
	}
	next := state.clone()
	for packageID := range state.Conflicts {
		if _, stillThere := present[packageID]; !stillThere {
			delete(next.Conflicts, packageID)
		}
	}
	// Pruning is an optimisation over already-durable application evidence. An
	// unreadable or unverifiable peer-owned retirement record cannot make it safe
	// to forget any of that evidence, but it cannot make an unrelated package
	// unsafe to apply either. Keep the ledger intact and report the problem through
	// the drain's existing non-fatal housekeeping channel.
	d.pruneApplied(next, present, local)
	if len(next.Conflicts) != len(state.Conflicts) || len(next.Applied) != len(state.Applied) {
		if err := saveStateWith(d.root, next, d.opts.writeState); err != nil {
			return err
		}
	}
	// A directory that could not be collected is not a reason to refuse every
	// import in the vault — see Drained.Cleanup.
	d.cleanup = errors.Join(d.cleanup, collectRetained(d.root, next.Conflicts))
	return nil
}

// pruneApplied removes only records the sending peer has made impossible to
// redeliver. Incoming absence is necessary but insufficient: a person may have
// removed a receive-only copy which the transport will restore. The peer's
// retirement watermark is the durable sender-owned promise, and the presence
// check keeps a watermark delivered ahead of its deletion from making this pass
// treat the still-visible package as new.
func (d *drain) pruneApplied(next *State, present map[string]struct{}, local *vaultsync.Enrollment) {
	retirement, err := vaultsync.ReadRetirement(vaultsync.IncomingDir(d.root))
	if err != nil {
		d.cleanup = errors.Join(d.cleanup, fmt.Errorf("vaultimport: prune applied ledger: %w", err))
		return
	}
	if retirement == nil {
		return
	}
	if err := retirement.VerifyFor(local); err != nil {
		d.cleanup = errors.Join(d.cleanup, fmt.Errorf("vaultimport: prune applied ledger: %w", err))
		return
	}
	for packageID, record := range next.Applied {
		if record.OriginDevice != retirement.OriginDevice || record.Sequence > retirement.Through ||
			retirement.Retains(packageID) {
			continue
		}
		if _, stillDelivered := present[packageID]; stillDelivered {
			continue
		}
		delete(next.Applied, packageID)
	}
}

// passes applies what it can, repeatedly, until a pass changes nothing.
//
// The loop is what makes a chain arrive in one drain rather than in one drain
// per link: applying a package can satisfy the dependency another was held on,
// and re-deciding is cheap next to leaving the second one in the channel until
// somebody runs the command again. It stops the moment a pass applies nothing,
// which is the only termination condition that does not depend on the shape of
// the dependency graph — a cycle, which no honest producer can emit, stalls
// instead of spinning.
func (d *drain) passes() error {
	for {
		progress, err := d.pass()
		if err != nil {
			return err
		}
		if !progress {
			return nil
		}
	}
}

// drainStopped reports a caller's stop as the error a drain returns. A nil
// context never stops.
func drainStopped(ctx context.Context) error {
	if ctx == nil || ctx.Err() == nil {
		return nil
	}
	return fmt.Errorf("vaultimport: drain stopped before the next package: %w", context.Cause(ctx))
}

func (d *drain) pass() (bool, error) {
	state, err := d.load()
	if err != nil {
		return false, err
	}
	progress := false
	for _, found := range d.pending {
		if found.done {
			continue
		}
		found.reason, found.detail, found.blocker, found.conflict = "", "", "", nil
		if held := d.hold(found, state); held {
			continue
		}
		// The one place a stop is honoured: before a package starts, never while
		// it is being applied. See DrainContext.
		if err := drainStopped(d.ctx); err != nil {
			return progress, err
		}
		applied, err := d.attempt(found)
		if err != nil {
			return progress, err
		}
		if applied {
			progress = true
		}
	}
	return progress, nil
}

// attempt applies one package and classifies what came back.
//
// Only conditions about the *vault* end the whole drain; everything else,
// including a conflict and a package this vault cannot accept, is a hold on that
// package and nothing more. Two of those conditions are sentinels the
// application returns (fatalToDrain), and the third is a state the application
// can leave behind without naming it, which is what unresolved asks about.
func (d *drain) attempt(found *candidate) (bool, error) {
	result, err := applyLocked(d.root, found.dir, d.opts)
	switch {
	case fatalToDrain(err):
		// The package was attempted and it failed; what stops the drain is what the
		// failure says about the vault, not about this package. Saying so here is
		// what keeps it out of the not-attempted bucket the rest of the pass lands in.
		found.reason, found.detail, found.conflict = HoldFailed, err.Error(), nil
		return false, err
	case result != nil:
		// A result is the one fact that outranks whatever the error says. The package
		// landed — committed, journaled, snapshotted — so it is recorded as applied
		// and the bookkeeping failure is carried out separately; see
		// Drained.Unfinished. Classifying on the error first would be the expensive
		// mistake, because some of the errors below can arrive *after* the content is
		// durable, and the package would then be reported as still in the channel.
		found.done = true
		d.applied = append(d.applied, result)
		d.unfinished = errors.Join(d.unfinished, unfinishedWork(result, err))
	case errors.Is(err, ErrConflict):
		found.reason, found.detail = HoldConflict, err.Error()
		found.conflict = d.recordedConflict(found)
	case errors.Is(err, vaultsync.ErrIncompletePackage):
		// The scan read a manifest, so this package looked whole; Accept then found a
		// payload the transport has not finished copying. That is the same situation
		// as a package with no manifest yet and gets the same answer — calling it a
		// failure would send somebody looking for a broken vault.
		found.reason, found.detail, found.conflict = HoldUnreadable, err.Error(), nil
	case rejected(err):
		found.reason, found.detail, found.conflict = HoldRejected, err.Error(), nil
	default:
		// The conflict this package was last held on, if it was, is over: it failed
		// for another reason entirely, and reporting the stale record would name
		// contested paths and retained branches that no longer describe anything.
		found.reason, found.detail, found.conflict = HoldFailed, err.Error(), nil
	}
	if unresolved := d.unresolved(); unresolved != nil {
		return found.done, errors.Join(err, unresolved)
	}
	return found.done, nil
}

// rejected reports the refusals no amount of waiting fixes: the package is not
// one this build understands, not one this vault is addressed by, or not one
// whose paths are portable vault content at all.
//
// It is asked in two places — of what the scan could not read, and of what the
// application refused — because the same refusal surfaces in either depending on
// whether it is a fact about the manifest or a fact about the enrollment. What
// it decides is only the *class of remedy*; vaultsync keeps these sentinels
// deliberately fine-grained because their individual remedies differ, and
// Held.Detail carries the one that fired, so nothing here collapses them.
func rejected(err error) bool {
	for _, refusal := range []error{
		vaultsync.ErrUnsupportedFormat, vaultsync.ErrMalformedPackage,
		vaultsync.ErrPathTraversal, vaultsync.ErrPrivatePath, vaultsync.ErrPathCollision,
		vaultsync.ErrWrongVault, vaultsync.ErrUnknownPeer,
		vaultsync.ErrStaleEpoch, vaultsync.ErrEpochAhead,
		ErrMultipleOperations,
	} {
		if errors.Is(err, refusal) {
			return true
		}
	}
	return false
}

// unfinishedWork names the package behind an application whose content landed
// and whose bookkeeping did not, and answers nothing for the ordinary case.
//
// The identity is added because the error comes from the receipt and state
// machinery and says nothing about which of the pass's packages it was about.
// Which of the two shapes it is, is read from ReceiptPath rather than guessed
// from the error. Receipt may contain a statement retained after publication
// failed; ReceiptPath is set only once the peer's channel actually holds it. A
// failure after that point belongs to later bookkeeping, while an empty path
// still sends the operator to the channel the peer reads.
func unfinishedWork(result *Result, err error) error {
	if err == nil {
		return nil
	}
	if result.ReceiptPath != "" {
		return fmt.Errorf("vaultimport: %s was applied and acknowledged and the work after that did not finish: %w",
			result.PackageID, err)
	}
	return fmt.Errorf("vaultimport: %s was applied and not acknowledged: %w", result.PackageID, err)
}

// fatalToDrain reports the refusals that are about this vault rather than about
// one package: another import holds the guard — impossible here, since this
// holds it, but reported rather than swallowed if it ever is — an interrupted
// application that cannot be resolved without losing content, and a record that
// cannot be read or does not belong to this vault. None of them gets better by
// trying the next package.
func fatalToDrain(err error) bool {
	switch {
	case errors.Is(err, ErrImportBusy), errors.Is(err, ErrStrandedImport):
		return true
	case errors.Is(err, ErrCorruptState), errors.Is(err, ErrUnsupportedVersion), errors.Is(err, ErrIdentityChanged):
		return true
	}
	return false
}

// unresolved stops a drain that has left an application pending, and it is the
// one check here that exists because *this* caller is a loop.
//
// Apply resolves an interrupted attempt before every single application, so it
// can never hold two. A drain applies many under one guard and resolves once, at
// the top — so an application that fails in a way its own in-process recovery
// also cannot finish (a snapshot the autosave watcher's lock refuses twice)
// leaves State.Pending set and returns an
// error that is none of the fatal sentinels. Carrying on would be destructive
// rather than merely wrong: the next package's declare overwrites State.Pending,
// and the record naming the first package's journal entry and manifest is the
// only route back to a change that is already on the disk. Stopping leaves it
// exactly where the next drain's recovery pass will find it.
func (d *drain) unresolved() error {
	state, err := d.load()
	if err != nil {
		return err
	}
	if state.Pending == nil {
		return nil
	}
	return fmt.Errorf("vaultimport: the application of %s is unresolved; no further package was attempted",
		state.Pending.PackageID())
}

// recordedConflict reads back the hold the refusal just wrote, so the report
// carries the retained branches rather than only the message.
func (d *drain) recordedConflict(found *candidate) *ConflictRecord {
	state, err := d.load()
	if err != nil {
		return nil
	}
	return state.Conflicts[found.packageID()]
}

// scanVerdict splits what the scan could not read into the two answers a person
// acts on differently.
//
// The scan reads a manifest, so most of what it cannot read is a directory the
// transport is still filling and the answer is to wait. The rest is a package
// this build will never read — a format token it does not know, a manifest that
// contradicts itself, a path that is not portable vault content, more than one
// operation in one package — and calling that a wait is how a peer that has
// moved to a newer format goes unnoticed for as long as nobody looks. Anything
// else, an I/O failure on a directory the transport owns, reads as a wait,
// because that is usually what it is.
func scanVerdict(err error) HoldReason {
	if rejected(err) {
		return HoldRejected
	}
	return HoldUnreadable
}
