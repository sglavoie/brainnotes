package vaultimport

import (
	"fmt"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultexport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

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
// this application is pinned to, before the attempt is declared and before the
// first byte moves.
//
// The ordering is vaultexport.RecordImport's documented contract and it is
// one-directional: the ledger must know the entry before the commit carrying it
// exists, or the exporter reads that commit as this device's own work and
// publishes it straight back to the peer that sent it. Recording it here rather
// than between the transaction and the snapshot is what makes the contract hold
// on every path, failure included. Recorded after the content landed, a ledger
// that could not be written — an export holding the record's lock past the
// budget, a full disk — left a choice between an applied change outside the
// recovery history and an undoable commit the exporter would echo, and this
// package took the second. Recorded here, the same failure refuses an import
// that has touched nothing: no attempt, no entry, no content, and the package
// stays in the channel for the next pass.
//
// The identity is knowable this early because this package mints it and pins it
// onto the recorder; see recorderOptions. The worst a record made here can cost
// is a ledger entry for an entry that never commits — the transaction refused,
// or the process died before it — which the exporter never looks up, since it
// consults the ledger only for entries a commit it is reading names.
func (a *applier) recordProvenance(entryID string) error {
	err := vaultexport.RecordImportContext(a.opts.checkpointContext, a.root, entryID, vaultexport.Import{
		OperationID:  a.operation.OperationID,
		OriginDevice: a.manifest.OriginDevice,
	})
	if err != nil {
		return fmt.Errorf("vaultimport: record import provenance for %s: entry %s applying operation %s from %s: %w",
			a.manifest.PackageID, entryID, a.operation.OperationID, a.manifest.OriginDevice, err)
	}
	return nil
}

// requirePinnedEntry checks that the transaction recorded under the entry the
// ledger and the attempt both name.
//
// noteops creates the recorder on the first Capture, so EntryID answers empty
// for an operation that changed nothing, and reading it after Commit is how
// this learns the application produced a real entry rather than assuming the
// one it named was used. A different identity would be an entry the provenance
// recorded above does not describe, so it is refused rather than finalized —
// the recovery pass that unwinds the attempt is the one that can put it back.
func (a *applier) requirePinnedEntry(operation *noteops.Journal, entryID string) error {
	switch got := operation.EntryID(); got {
	case entryID:
		return nil
	case "":
		return fmt.Errorf("vaultimport: applying %s captured no recovery entry", a.manifest.PackageID)
	default:
		return fmt.Errorf("vaultimport: applying %s recorded entry %s, but provenance names %s",
			a.manifest.PackageID, got, entryID)
	}
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
