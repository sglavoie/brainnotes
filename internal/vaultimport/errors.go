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
