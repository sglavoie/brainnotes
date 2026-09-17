package vaultimport

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// stateVersion is the schema version of the import record. Compared exactly for
// the reason the export record gives: a newer build may carry a field this one
// would drop on the next write, and dropping an unresolved attempt silently is
// worse than refusing to run.
const stateVersion = 1

// stateFileName is the record's name inside the synchronization state
// directory, beside the exporter's.
const stateFileName = "import.json"

// statePerm matches the export record's: identities and path names, no secrets,
// in a directory that is already world-readable.
const statePerm fs.FileMode = 0o644

// StatePath returns the absolute path of the import record for the vault at
// root.
//
// Derived from vaultsync.StateDir rather than spelled out, so it cannot drift
// out of the directory the portable policy excludes wholesale. A record naming
// every path an import touched is exactly the thing that must never become
// portable content.
func StatePath(root string) string {
	return filepath.Join(root, filepath.FromSlash(path.Join(vaultsync.StateDir(), stateFileName)))
}

// State is what this vault remembers about packages it has applied, and about
// the one it may have been applying when it stopped.
//
// It exists because none of the six steps an import goes through can be made
// atomic with the next one. The peer's bytes land, the journal manifest lands,
// the commit lands, the receipt lands — four separate durability events, and a
// process can die between any two. Without a record written *before* the first
// of them, a vault that comes back up cannot tell an import that never started
// from one that half finished, and cannot tell either from a package being
// delivered a second time.
//
// It is one file rather than an attempt file and a ledger file, for the reason
// the export record is one file: clearing the attempt and recording what it
// applied are the same decision, and a crash between two files would either
// strand the attempt or forget the application.
type State struct {
	// Version is stateVersion.
	Version int `json:"version"`
	// VaultID and DeviceID are the enrollment identities this record was
	// written under, checked on load. A record copied from the peer would
	// otherwise make this device believe it had applied the peer's imports.
	VaultID  string `json:"vault_id"`
	DeviceID string `json:"device_id"`
	// Pending is the application that was started and whose outcome is not yet
	// known, written before this vault is touched and cleared once it is
	// resolved. It is the only thing that makes an interrupted import
	// recoverable rather than a mystery: the paths, what they held, and what
	// they were going to hold are all in it, so a later process can decide
	// between rolling forward and rolling back without the package.
	Pending *Attempt `json:"pending,omitempty"`
	// Applied maps a package identity to what applying it produced here. It is
	// what makes a redelivery idempotent: the transport may deliver one package
	// any number of times, and every delivery after the first must answer from
	// this map instead of touching the vault again.
	Applied map[string]Record `json:"applied"`
}

// Attempt is one application in progress, made durable before it begins.
//
// It is self-contained on purpose. A recovering process has nothing else to work
// from: the journal entry may have no manifest to read, the peer is not
// reachable, and the package directory is a caller's argument that this record
// has no claim on — Apply takes the path it is handed, and by the time recovery
// runs the channel may have been swept, re-delivered, or mounted somewhere else.
// So the accepted manifest is kept whole here rather than re-read, and every
// question recovery asks is answered out of these two fields.
type Attempt struct {
	// JournalEntry is the local undo entry the application will record under.
	// It is minted before the recorder exists and pinned, which is what lets a
	// recovering process find a half-built entry it never saw created.
	JournalEntry string `json:"journal_entry"`
	// Manifest is the package as vaultsync.Accept approved it. Both ends of
	// every change matter to recovery: the expected prior state is what a
	// rollback restores to, and the resulting state is what says the change
	// landed at all. It is also the only thing that can mint the receipt the
	// peer is owed, since an acknowledgment is checked against the package it
	// answers.
	Manifest *vaultsync.Manifest `json:"manifest"`
	// Restored is the rollback's record of itself, written the moment the
	// content is back and before the two steps that follow it. Nil means no
	// rollback has finished.
	//
	// It exists because the rollback's own decision is not repeatable. A plan is
	// made by comparing each path against what the package expected to find and
	// what it installed, and once the content has been put back every path reads
	// as the first of those — indistinguishable from a path that was never
	// written. That is harmless while nothing else touches the vault, and wrong
	// the moment something does: a person who edits one of those notes before the
	// interrupted rollback is finished makes it read as neither, which is the
	// signature of an edit that has to be refused rather than overwritten. Without
	// this the refusal would be aimed at a rollback that had already happened, and
	// a finished rollback would be treated as an unresolvable one for the rest of
	// the vault's life.
	Restored *Rollback `json:"restored,omitempty"`
}

// Rollback is a completed restoration: what a rollback put back, kept so the
// steps after it never have to decide again.
type Rollback struct {
	// Paths are the vault-relative paths whose content was restored, in package
	// order. Empty is a real answer and the commonest one — the interruption
	// came before anything was written — and is why this is a pointer: absent
	// and "restored nothing" are different facts.
	Paths []string `json:"paths"`
}

// PackageID is the package this attempt was applying.
func (a *Attempt) PackageID() string {
	if a == nil || a.Manifest == nil {
		return ""
	}
	return a.Manifest.PackageID
}

// Record is one applied package's outcome.
type Record struct {
	// OperationID is the peer operation that was applied.
	OperationID string `json:"operation_id"`
	// OriginDevice is the peer that produced it.
	OriginDevice string `json:"origin_device"`
	// JournalEntry is the local recovery entry that can undo it.
	JournalEntry string `json:"journal_entry"`
	// Receipt is the acknowledgment minted for the peer, kept whole.
	//
	// Stored rather than re-derived, and that is a correctness decision. A
	// receipt states what each path held when the package was applied; minting a
	// replacement now would state what they hold today, and a note edited since
	// would make the two disagree — so a peer that lost the first receipt could
	// never be given an honest second one. Keeping the original means a lost
	// reply is answered by republishing the same bytes, whatever has happened to
	// the vault since.
	//
	// Nil means the package was applied and never successfully acknowledged,
	// which is the state a redelivery or a recovery pass completes.
	Receipt *vaultsync.Receipt `json:"receipt,omitempty"`
}

// newState returns the record a vault that has never imported starts from.
func newState(local *vaultsync.Enrollment) *State {
	return &State{
		Version:  stateVersion,
		VaultID:  local.VaultID,
		DeviceID: local.DeviceID,
		Applied:  map[string]Record{},
	}
}

// validate checks the record against itself, strictly about shapes that would
// become a wrong action rather than a failure: an attempt naming no journal
// entry must fail here, not inside a rollback that then has nothing to restore
// from.
func (s *State) validate() error {
	if s == nil {
		return fmt.Errorf("%w: no record", ErrCorruptState)
	}
	if s.Version != stateVersion {
		return fmt.Errorf("%w: record is version %d, this build writes %d",
			ErrUnsupportedVersion, s.Version, stateVersion)
	}
	if strings.TrimSpace(s.VaultID) == "" || strings.TrimSpace(s.DeviceID) == "" {
		return fmt.Errorf("%w: record names no vault or device", ErrCorruptState)
	}
	if err := s.Pending.validate(); err != nil {
		return err
	}
	for packageID, record := range s.Applied {
		if err := record.validate(packageID); err != nil {
			return err
		}
	}
	return nil
}

// validate checks a pending attempt through the package's own validator rather
// than through a second set of rules written here.
//
// Every property recovery relies on — portable paths, an expectation that agrees
// with itself, exactly one operation — is already vaultsync.Manifest.Validate's,
// and restating them would create a record this package accepts and vaultsync
// does not. These bytes came off a disk and are fed back into a transaction that
// writes them, so they are re-checked on the way in rather than trusted for
// having been checked on the way out.
func (a *Attempt) validate() error {
	if a == nil {
		return nil
	}
	if strings.TrimSpace(a.JournalEntry) == "" {
		return fmt.Errorf("%w: pending attempt names no journal entry", ErrCorruptState)
	}
	if a.Manifest == nil {
		return fmt.Errorf("%w: pending attempt carries no package", ErrCorruptState)
	}
	if err := a.Manifest.Validate(); err != nil {
		return fmt.Errorf("%w: pending attempt: %v", ErrCorruptState, err)
	}
	operation, err := singleOperation(a.Manifest)
	if err != nil {
		return fmt.Errorf("%w: pending attempt: %v", ErrCorruptState, err)
	}
	return a.Restored.validate(operation)
}

// validate checks a rollback record against the operation it claims to have
// undone, which is the one shape here that would become a wrong action rather
// than a failure.
//
// A record that is merely present makes the next pass skip the restoration
// entirely, delete the journal entry holding the only copy of the pre-change
// bytes, and settle the attempt. So a record naming a path this package never
// changed did not come from this package's rollback, and believing it would
// destroy the evidence for an application that is still on the disk. Refusing is
// the conservative half: ErrCorruptState is never self-healed, and a person with
// the entry still in place can see what happened.
func (r *Rollback) validate(operation *vaultsync.Operation) error {
	if r == nil {
		return nil
	}
	changed := make(map[string]struct{}, len(operation.Changes))
	for _, change := range operation.Changes {
		changed[change.Path] = struct{}{}
	}
	for _, path := range r.Paths {
		if _, ok := changed[path]; !ok {
			return fmt.Errorf("%w: pending attempt records restoring %q, which operation %s never changed",
				ErrCorruptState, path, operation.OperationID)
		}
	}
	return nil
}

func (r Record) validate(packageID string) error {
	switch {
	case strings.TrimSpace(packageID) == "":
		return fmt.Errorf("%w: applied ledger holds an unnamed package", ErrCorruptState)
	case strings.TrimSpace(r.OperationID) == "":
		return fmt.Errorf("%w: applied package %s names no operation", ErrCorruptState, packageID)
	case strings.TrimSpace(r.JournalEntry) == "":
		return fmt.Errorf("%w: applied package %s names no journal entry", ErrCorruptState, packageID)
	}
	if r.Receipt != nil && r.Receipt.PackageID != packageID {
		return fmt.Errorf("%w: applied package %s holds a receipt for %s",
			ErrCorruptState, packageID, r.Receipt.PackageID)
	}
	return nil
}

// clone returns a record a caller can build the next one up from and discard
// wholesale. The attempt's manifest is shared rather than copied: a package is
// immutable by definition and nothing here or in internal/vaultsync ever writes
// through one.
func (s *State) clone() *State {
	out := *s
	if s.Pending != nil {
		attempt := *s.Pending
		out.Pending = &attempt
	}
	out.Applied = make(map[string]Record, len(s.Applied))
	for k, v := range s.Applied {
		out.Applied[k] = v
	}
	return &out
}

// loadState reads the import record for the vault at root, returning a fresh one
// for a vault that has never imported.
//
// Absence is not an error and is indistinguishable from a first run, which is
// correct: both mean this device has applied nothing and has nothing to recover.
func loadState(root string, local *vaultsync.Enrollment) (*State, error) {
	statePath := StatePath(root)
	data, err := os.ReadFile(statePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return newState(local), nil
		}
		return nil, fmt.Errorf("vaultimport: read import state %q: %w", statePath, err)
	}
	var record State
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("%w: parse %q: %v", ErrCorruptState, statePath, err)
	}
	if err := record.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", statePath, err)
	}
	if record.VaultID != local.VaultID || record.DeviceID != local.DeviceID {
		return nil, fmt.Errorf("%w: %s names %s/%s, this vault is %s/%s",
			ErrIdentityChanged, statePath, record.VaultID, record.DeviceID, local.VaultID, local.DeviceID)
	}
	if record.Applied == nil {
		record.Applied = map[string]Record{}
	}
	return &record, nil
}

// saveState writes the import record. The caller must hold the import lock.
//
// vault.WriteStateFile rather than vaultstate.Save, for the reason the export
// record states: Save's third guarantee is a snapshot into the content history,
// and `.bnotes/sync/` is out of that history's scope by design. Save would not
// refuse the path — it would drop it and report success — so a reader of Save's
// contract here would believe this file was snapshotted.
//
// It is also what keeps this package out of internal/audit's approved list: no
// production file here opens, creates, renames, or removes anything.
func saveState(root string, record *State) error {
	if err := record.validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("vaultimport: encode import state: %w", err)
	}
	data = append(data, '\n')

	statePath := StatePath(root)
	// Ahead of any artifact, like every other mutation in this repository.
	if err := vault.GuardMutation(root, statePath); err != nil {
		return err
	}
	if err := vault.WriteStateFile(statePath, data, statePerm); err != nil {
		return fmt.Errorf("vaultimport: write import state: %w", err)
	}
	return nil
}

// LoadState returns a copy of the import record for the vault at root, for a
// caller reporting synchronization status. It is read-only: nothing here
// resolves an attempt.
func LoadState(root string) (*State, error) {
	local, err := vaultsync.Load(root)
	if err != nil {
		return nil, err
	}
	record, err := loadState(root, local)
	if err != nil {
		return nil, err
	}
	return record.clone(), nil
}
