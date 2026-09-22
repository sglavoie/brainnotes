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
// Nine properties define it, and each is a refusal rather than a convention.
//
// Nothing is applied that was not accepted. Apply starts at vaultsync.Accept,
// so a malformed manifest, a package addressed to another vault, one from an
// unenrolled device, one from another epoch, and one whose payload bytes have
// not all arrived are each refused before any of this vault's content is
// touched. The digest of every payload is then verified a *second* time as it is
// read, because the transport is still running while a package is being applied
// and "checked a moment ago" is not "checked". Two things do happen first, and
// both are about earlier runs rather than this package: the import guard is
// taken, so a package delivered while another import is running is refused with
// ErrImportBusy before it is looked at, and an interrupted attempt is resolved,
// so the paths this package is about are settled before its preconditions are
// checked.
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
// ErrConflict, which is a fact about two devices rather than a fault. The hold
// it leaves keeps every version of the path, and is what a resolution is made
// against later.
//
// The one qualification is where the disagreement is noticed. internal/vault
// re-verifies its read-only requirements again before and after each individual
// change, so a configuration pin that fails mid-commit raises the same sentinel
// after the journal has been captured: the content is compensated back, and the
// un-finalized entry directory left behind is removed by the same resolution an
// interrupted application gets, run in-process before the failure is returned.
// Nothing reads such an entry — it has no manifest, so it is neither undoable
// nor prunable, and journal.ListEntries cannot see it — so leaving one would be
// residue nothing in this vault could ever account for or collect.
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
// local journal entry is minted before the operation begins and pinned onto the
// recorder, so it is knowable at every point after that; it is recorded through
// vaultexport.RecordImport *before* the entry is finalized and snapshotted.
// Written afterwards, a crash in between would leave a commit the exporter reads
// as this device's own work and publishes straight back to the peer that sent it.
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
// An ordinary acknowledgment is only ever minted for a durable, undoable
// change. The receipt's recovery entry is read back from disk with
// journal.LoadEntry before the receipt exists, which is the check vaultsync
// documents that it cannot make itself — it is a leaf and cannot import
// internal/journal, so it verifies that an entry is named and never that one
// exists. A wholly converged package is the distinct honest case: it wrote
// nothing, so its receipt explicitly says convergence and names no recovery
// entry. Each acknowledged hash is computed from what this disk holds now rather
// than copied from the package's claim, and a tombstone is acknowledged only
// once the path is really gone. Failing to mint or publish one does not unmake
// the change it would have described, so Apply returns the Result alongside the
// error in that case, with the journal entry named for ordinary applications and
// the receipt nil.
//
// An interruption resolves to "never happened" or "happened completely", and
// never to anything else. An import is six durable events — the peer's bytes,
// the provenance record, the undo manifest, the commit, the receipt, and the
// memory of all of it — and no two of them can be made one, so a process that
// dies in between leaves a vault that is perfectly consistent and completely
// unexplained. Apply writes an attempt record before the first byte moves,
// naming the package and the recovery entry it will use, and Recover is the only
// thing that reads it. Recovery runs ahead of every import and can be called on
// its own; it is idempotent, so a second pass finds nothing to do.
//
// The decision turns on one durable fact, the undo manifest, because that is the
// moment the change stops being something this vault is doing and becomes
// something it did: it is what `bn undo` reads, what authorizes committing a
// deletion, and what the receipt promises the peer. Before it the application is
// rolled back, since acknowledging a change this vault could not undo is the one
// thing a peer must never be told; after it the application is rolled forward,
// since the provenance record is already durable and the change is already this
// vault's own history. Every step on the forward path is idempotent by
// construction — RecordImport is a no-op for an entry it already holds, the
// snapshot is skipped when the content history already has the entry, and a
// published receipt is adopted rather than re-minted.
//
// One thing recovery will not do: roll back over a path holding neither what the
// package replaced nor what it installed. That refusal is ErrStrandedImport. It
// keeps every artifact — the attempt, the entry and its snapshots, and the
// content — and *blocks every further import* until a person resolves it, so the
// evidence cannot be buried by the next package the transport delivers. A
// persistent provenance or snapshot failure blocks the same way and for the same
// reason. Because the price of that refusal is the whole import path, a rollback
// that has already put the content back records the fact rather than leaving it
// to be inferred from paths that by then read exactly like untouched ones. That
// record covers every step after it, including discarding the entry directory,
// which is a recursive removal rather than one write; what it cannot cover is the
// instant between the restoration and itself, because a content transaction and a
// state file cannot be made one write and the other order would have the next
// pass skip a restoration that never happened.
//
// The forward direction has no equivalent refusal: an acknowledgment that cannot
// be minted fails the message, not the vault, and the attempt is settled with no
// receipt.
//
// A redelivery is answered, not applied. The transport moves whole directories
// and knows nothing about receipts, so the same package reappears whenever a
// channel is re-copied, a peer is restored from a backup, or an acknowledgment
// goes missing — and applying it again would meet preconditions this vault no
// longer satisfies, because the package itself changed them, and report the peer
// as being in conflict with its own change. Both halves are answered from the
// import ledger instead: what was applied is described as it was applied, and
// the acknowledgment the peer is missing is the *original* receipt republished
// rather than a new one minted now. A receipt is a statement the peer may drop
// its only copy of a change on, so repeating one is honest where making a second
// one would not be. A package that was applied and never successfully
// acknowledged is the single exception, and it is held to every check a first
// acknowledgment is held to.
//
// A resolution is two commits, and the peer's version is the one in between.
// Preview reads a held package against this disk and answers with a Plan: every
// path the decision touches — including the ones a choice derives, such as the
// copy keep-both writes beside the note — with the branch each will end at and
// both branches pinned as transaction preconditions, so the person is told what
// they are about to change before they change it. Resolve then applies the held
// package exactly as any other import, because a receipt is only honest about a
// disk that really holds what the package installed and a peer told nothing
// redelivers forever; and it records the choice as a revision of that
// application — this device's own commit, publishing with the peer's operation
// as its predecessor and the peer's own content as what it expects to find. The
// peer holds exactly that, so the revision applies there with nothing left to
// decide, and the rejected version is in the content history and the recovery
// history rather than nowhere. A plan the vault has moved past — a late local
// edit, a competing resolution, a hold that is gone — is ErrStalePreview with
// nothing written, and a choice the conflict cannot take is
// ErrResolutionUnavailable before anything is staged. The plan is durable before
// the application starts, so an interruption between the two commits is finished
// by the next pass and one before the application landed is abandoned back to a
// hold.
//
// What it is not: a transport (Syncthing moves the channel), a merge algorithm
// (a resolution records a decision a person made, including merged content they
// supply; nothing here computes one), and a scheduler of its own channel beyond
// causal order (Drain applies what has arrived, and never goes looking for a
// package).
package vaultimport
