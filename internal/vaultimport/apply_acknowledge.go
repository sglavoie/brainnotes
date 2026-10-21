package vaultimport

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/sglavoie/brainnotes/internal/journal"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

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
	return a.acknowledgeConvergenceWith(a.mintConvergence)
}

func (a *applier) acknowledgeConvergenceWith(mint func() (*vaultsync.Receipt, error)) (*Result, bool, error) {
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
		receipt, proofErr := mint()
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
	receipt, err := mint()
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

// mintRecordedConvergence reconstructs the statement an old build proved and
// then lost when publication failed. rememberConvergence could only have written
// this ledger shape after mintConvergence succeeded, so the manifest results are
// evidence already checked at that time rather than claims being trusted now.
func (a *applier) mintRecordedConvergence() (*vaultsync.Receipt, error) {
	applied := make([]vaultsync.AppliedPath, 0, len(a.changes))
	for _, planned := range a.changes {
		change := planned.change
		entry := vaultsync.AppliedPath{Path: change.Path, Action: change.Action}
		if change.Action != vaultsync.ActionDelete {
			entry.SHA256 = change.ResultSHA256
		}
		applied = append(applied, entry)
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
