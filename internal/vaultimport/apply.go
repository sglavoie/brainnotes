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
