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
	// A *resolution's* unwind raises the second condition too, and leaves a
	// different vault behind — read which one raised it before acting on it. By
	// then the package has been applied and acknowledged, on purpose: a resolution
	// is two commits and the receipt belongs to the first. So there is no pending
	// attempt to find, State.Applied names the package, the peer has been told to
	// drop its copy, and what is preserved is the promised revision, its staged
	// content and the manifest-less entry of the interrupted revision commit. The
	// vault holds the peer's version of the contested paths, which is the window
	// the design opens deliberately, and the decision that was made about it is
	// still recorded and still owed.
	//
	// That one has no remedy through this package. The import-side condition
	// clears when a person puts the edited paths back; this one is read from an
	// immutable snapshot, so nothing a person does to the notes changes the
	// answer, and every entry point runs the resolution pass first and so fails
	// with it. Clearing it means removing the package's entry from State.Resolutions
	// by hand. That is a worse position than the import-side refusal and is stated
	// here rather than discovered, because the alternative — abandoning the promise
	// — destroys the staged decision and orphans the half-made revision.
	//
	// Roll-forward has no counterpart to this. An acknowledgment that cannot be
	// minted is a failed message rather than an unresolvable vault: the
	// application is already this vault's history, so the attempt is settled and
	// the ledger records it with no receipt. See resolver.complete.
	ErrStrandedImport = errors.New("vaultimport: an interrupted import cannot be resolved without losing content")

	// ErrUnresolvedImport reports an import record still holding work: an
	// interrupted application, a package held on a conflict, or a conflict
	// decision whose revision is still owed. Re-enrollment refuses it, because
	// re-enrollment starts an empty record and would discard the only copy of
	// that work — the attempt that can roll an application back, the hold naming
	// where each contested version was kept, the choice somebody made.
	ErrUnresolvedImport = errors.New("vaultimport: the import record holds unresolved work")
)

// The three below are about resolving a hold, and each names a different reason
// a decision cannot be carried out. They are separate from the sentinels above
// because none of them describes damage or contention: a resolution is a person
// acting on a conflict, and the ways that act can be refused are all ordinary.
var (
	// ErrNoConflict reports a package this vault is not holding. It is the answer
	// to resolving a conflict that resolved itself — somebody edited the note back
	// to what the package expected, a later drain applied it, and the hold went
	// with the application.
	ErrNoConflict = errors.New("vaultimport: no conflict is held for this package")

	// ErrStalePreview reports a plan describing a conflict the vault has moved
	// past.
	//
	// A preview is a reading of two branches at one instant, and a person chooses
	// between them at another. Between those two instants the note can be edited,
	// the hold can be released, or a second process can resolve the same package —
	// and a plan that names a local branch which is no longer there would, if it
	// were carried out, revise the vault back to a version nobody chose.
	//
	// It is raised before anything is written, and it is the outer of two guards
	// rather than the only one: every branch a plan names is also pinned as a
	// transaction precondition, so an edit that lands after this check is refused
	// under the path's own lock. This one exists to refuse cheaply and to say
	// which path changed, which a precondition failure cannot.
	ErrStalePreview = errors.New("vaultimport: the resolution was planned against a state this vault no longer holds")

	// ErrResolutionUnavailable reports a choice that cannot be carried out for the
	// conflict it was made about.
	//
	// Not every remedy fits every kind. Keeping both versions of the vault's
	// declarations would leave a copy of a configuration file that governs
	// nothing; keeping both sides of a rename this vault diverged on would create
	// the second copy the conflict exists to prevent; and a name collision has to
	// be cleared before either version can be named at all. Refusing by name is
	// what lets a caller offer the choices that do fit rather than discovering the
	// misfit halfway through.
	//
	// It also reports the bytes of a chosen branch being unreachable — a merge
	// supplied to one process and resolved from another, a local version deleted
	// between the preview and the resolution, or a staged revision whose file has
	// gone missing before the promise could be kept.
	//
	// The last case it reports is a caller pairing a plan with a package that is
	// not the one it resolves. That is a mistake about which conflict is being
	// answered rather than about which remedy fits it, but the consequence is the
	// same one this sentinel names — there is no resolution to carry out — and
	// the alternative is applying one decision's content under another's
	// authorization.
	ErrResolutionUnavailable = errors.New("vaultimport: that resolution cannot be carried out for this conflict")
)
