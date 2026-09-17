package vaultimport

import "errors"

// The sentinels here are the ones internal/vaultsync's vocabulary has no word
// for, and there are only two. Everything an applier can refuse about a package
// *as a package* — malformed, misaddressed, incomplete, from an unknown peer,
// from another epoch — already has a name in vaultsync, and those are returned
// unchanged rather than re-wrapped: the remedy is the same wherever the refusal
// is noticed, and a second name for one would only make the same condition look
// like two different problems.
var (
	// ErrConflict reports a path whose content is not what the package expected
	// it to be. Two devices edited the same note from the same starting point,
	// and nothing here is entitled to decide which one wins.
	//
	// It is deliberately not a failure of the import. The transaction verifies
	// every precondition at staging, before it touches any destination, so the
	// ordinary conflict leaves the contested content exactly as it was with no
	// journal entry and no commit. The package stays in the channel and the peer,
	// having had no receipt, still holds its own copy — which is what lets a later
	// layer hold the operation and present both branches instead of losing one.
	//
	// "No journal entry" is a claim about that staging check and only about it.
	// internal/vault re-verifies its requirements again before and after each
	// individual change, so a configuration layer edited mid-commit raises the
	// same sentinel from a point where the journal has already been captured.
	// The content is still restored, but an un-finalized entry directory can
	// survive the refusal. The contested-content half of this promise holds
	// either way; the nothing-on-disk half does not.
	ErrConflict = errors.New("vaultimport: package expects content this vault does not hold")

	// ErrMultipleOperations reports a package carrying more than one operation
	// boundary.
	//
	// A receipt names one local recovery entry and the export ledger maps one
	// journal entry to one peer operation, so there is no honest way to record
	// two operations applied together. Refusing is the conservative half: the
	// package stays in the channel, undamaged, for a build that can split it.
	ErrMultipleOperations = errors.New("vaultimport: package carries more than one operation")
)

// The five below are about this device's own memory of what it has applied,
// which the two above have no word for: they describe a package, and these
// describe the record beside it. Three of them — ErrCorruptState,
// ErrUnsupportedVersion, ErrIdentityChanged — mirror internal/vaultexport's
// state sentinels exactly, because the file is the same kind of file and the
// remedies are the same remedies. The other two have no counterpart there:
// a producer publishes without a guard like ErrImportBusy's and never has to
// decide what a dead process left half-applied.
var (
	// ErrCorruptState reports import state that cannot be decoded, or decodes
	// into a record that contradicts itself. Distinct from
	// ErrUnsupportedVersion: this is damage, that is a newer writer.
	//
	// It is never self-healed. Rebuilding it would mean guessing whether a
	// package was applied, and both guesses are destructive: guessing "no"
	// applies a package twice, guessing "yes" acknowledges one that never landed.
	ErrCorruptState = errors.New("vaultimport: corrupt import state")

	// ErrUnsupportedVersion reports readable import state written under a version
	// this build does not know.
	ErrUnsupportedVersion = errors.New("vaultimport: unsupported import state version")

	// ErrIdentityChanged reports import state naming a vault or device other than
	// the one the enrollment record names. It means a state file was copied
	// between vaults, which is the one way a device can claim to have applied a
	// package it never saw.
	ErrIdentityChanged = errors.New("vaultimport: import state belongs to another vault or device")

	// ErrImportBusy reports another import already running in this vault.
	//
	// It is retryable contention and not a fault, the same answer vaultgit's
	// preflight gives: nothing was read, staged, or written. Refusing rather than
	// waiting is deliberate. The lock is also what tells a recovery pass that the
	// unresolved attempt it can see belongs to a process that is still alive, so
	// a waiter would be a process queuing up to recover a transaction that is
	// about to finish on its own.
	ErrImportBusy = errors.New("vaultimport: another import is running in this vault")

	// ErrStrandedImport reports an interrupted application that could not be
	// resolved, with everything it left behind deliberately preserved.
	//
	// It is the refusal that protects the one thing a rollback must never do:
	// write over content it cannot account for. Two conditions raise it, and both
	// mean the rollback would be working from something it cannot trust. The paths
	// hold neither what the package replaced nor what it installed — somebody
	// edited them after the interruption, and rolling back would destroy that
	// newer edit. Or the journal entry's snapshot of a path does not hash to what
	// the package said it was replacing, which means the entry is not the one the
	// attempt names and restoring from it would install some other operation's
	// idea of that path. Either way the attempt stays pending, the journal entry
	// stays on disk, and no receipt is minted, so the peer keeps its copy and a
	// person has every version of the content to choose between — at the price of
	// every later import refusing until somebody does.
	//
	// Roll-forward has no counterpart to this. An acknowledgment that cannot be
	// minted is a failed message rather than an unresolvable vault: the
	// application is already this vault's history, so the attempt is settled and
	// the ledger records it with no receipt. See resolver.complete.
	ErrStrandedImport = errors.New("vaultimport: an interrupted import cannot be resolved without losing content")
)
