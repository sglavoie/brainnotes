// Package vaultimport applies a peer replica's package to this vault.
//
// It is the consumer half of synchronization, and the mirror of
// internal/vaultexport: that package turns this vault's committed history into
// packages, this one turns a peer's package into this vault's own history.
// internal/vaultsync still decides what a package is and who may accept one;
// nothing here re-answers those questions, and nothing here writes a byte that
// did not go through internal/vault's transaction, internal/noteops' operation
// lifecycle, and internal/vaultgit's snapshot.
//
// Six properties define it, and each is a refusal rather than a convention.
//
// Nothing is applied that was not accepted. Apply starts at vaultsync.Accept,
// so a malformed manifest, a package addressed to another vault, one from an
// unenrolled device, one from another epoch, and one whose payload bytes have
// not all arrived are each refused before this vault is touched. The digest of
// every payload is then verified a *second* time as it is read, because the
// transport is still running while a package is being applied and "checked a
// moment ago" is not "checked".
//
// One package is one local operation. A package carrying more than one
// operation is refused with ErrMultipleOperations rather than applied in pieces.
// The reason is structural, not a simplification for later: a receipt names
// exactly one local recovery entry, and the exporter's import ledger maps one
// journal entry to one peer operation and refuses a commit carrying two of them.
// Applying two operations under one journal entry would make both of those
// records ambiguous, and applying them under two would mint two commits for one
// package — a package the peer would then have to be acknowledged for twice.
// vaultexport publishes exactly one operation per package, so this refusal
// cannot fire against a package this project produces.
//
// A precondition failure leaves the contested content alone. Every change
// carries what the producer believed the path held, and that belief becomes a
// vault.FileExpectation the transaction verifies under the path's own lock.
// vault.FileTxn stages every replacement and checks every precondition before it
// touches a single destination, and the journal is captured only after staging
// succeeds — so a package that disagrees with this disk is refused with no
// partial write, no journal entry, and no commit. That is reported as
// ErrConflict, which is a fact about two devices rather than a fault, and
// holding or resolving it belongs to a later layer.
//
// The one qualification is where the disagreement is noticed. internal/vault
// re-verifies its read-only requirements again before and after each individual
// change, so a configuration pin that fails mid-commit raises the same sentinel
// after the journal has been captured: the content is compensated back, but an
// un-finalized entry directory can survive the refusal. Nothing reads such an
// entry — it has no manifest, so it is neither undoable nor prunable — and
// cleaning it up belongs to the session that owns interrupted applications.
//
// The configuration that governs the content is pinned for the whole commit.
// Both layers are required to stay exactly as they were — the portable
// brainnotes.toml unless the package itself changes it, and the machine-local
// overlay always, including its absence. A vault whose declarations changed
// halfway through an import would have applied a peer's notes under rules that
// never governed them. This package supplies the pins; internal/vault decides
// when they are checked, and checks them both at staging and around every change.
//
// Provenance is durable before the commit that needs it. The identity of the
// local journal entry is read off the recorder after the transaction commits and
// recorded through vaultexport.RecordImport *before* the entry is finalized and
// snapshotted. Written afterwards, a crash in between would leave a commit the
// exporter reads as this device's own work and publishes straight back to the
// peer that sent it.
//
// When the ledger write itself fails there is no ordering left to choose: the
// peer's bytes are already on this disk, so the entry is finalized and
// snapshotted anyway — an applied change outside the recovery history would be
// the worse failure — and both problems are reported together. No receipt is
// minted, so the peer keeps its copy. That vault has an undoable commit the
// ledger does not recognize as imported, and the error names the entry, the
// operation, and the origin device, which is every argument the repairing
// RecordImport call needs.
//
// An acknowledgment is only ever minted for a durable, undoable change. The
// receipt's recovery entry is read back from disk with journal.LoadEntry before
// the receipt exists, which is the check vaultsync documents that it cannot make
// itself — it is a leaf and cannot import internal/journal, so it verifies that
// an entry is named and never that one exists. Each acknowledged hash is
// computed from what this disk holds now rather than copied from the package's
// claim, and a tombstone is acknowledged only once the path is really gone.
// Failing to mint or publish one does not unmake the change it would have
// described, so Apply returns the Result alongside the error in that case, with
// the journal entry named and the receipt nil.
//
// What it is not: a transport (Syncthing moves the channel), a conflict resolver
// (it reports a conflict and leaves both versions where they are), a scheduler
// (it applies the package it is handed, and never goes looking for one), and not
// idempotent across a redelivery of a package it already applied — a second
// delivery meets preconditions this vault no longer satisfies and is reported as
// a conflict. See session 7's handoff for where that boundary is drawn.
package vaultimport
