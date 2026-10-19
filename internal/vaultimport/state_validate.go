package vaultimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// newState returns the record a vault that has never imported starts from.
func newState(local *vaultsync.Enrollment) *State {
	return &State{
		Version:     stateVersion,
		VaultID:     local.VaultID,
		DeviceID:    local.DeviceID,
		Applied:     map[string]Record{},
		Conflicts:   map[string]*ConflictRecord{},
		Resolutions: map[string]*Plan{},
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
	return s.validateLedgers()
}

// validateLedgers checks the two per-package maps against themselves and against
// each other.
//
// The cross-check is the one that matters: applied and held are contradictory
// answers to "what happened to this package", and a record holding both would
// make a redelivery answered from one map while a drain pass re-decides it from
// the other.
func (s *State) validateLedgers() error {
	for packageID, record := range s.Applied {
		if err := record.validate(packageID); err != nil {
			return err
		}
	}
	for packageID, held := range s.Conflicts {
		if err := held.validate(packageID); err != nil {
			return err
		}
		if _, applied := s.Applied[packageID]; applied {
			return fmt.Errorf("%w: package %s is recorded as both applied and held",
				ErrCorruptState, packageID)
		}
	}
	for packageID, promised := range s.Resolutions {
		if err := promised.validate(packageID); err != nil {
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
	// Held to the same rule Restored is, and for a sharper reason: a converged
	// path is one recovery will *not* restore, so a name the operation never
	// changed would quietly exempt something from the rollback. Naming a path the
	// package does not carry is the signature of a record that is not this
	// attempt's.
	if err := namesChanged(operation, "converged", a.Converged); err != nil {
		return err
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
	return namesChanged(operation, "restoring", r.Paths)
}

// namesChanged refuses a list of paths that is not a subset of what the
// operation changed. role names what the list is, so the refusal says which of
// the attempt's two path lists is wrong.
func namesChanged(operation *vaultsync.Operation, role string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	changed := make(map[string]struct{}, len(operation.Changes))
	for _, change := range operation.Changes {
		changed[change.Path] = struct{}{}
	}
	for _, rel := range paths {
		if _, ok := changed[rel]; !ok {
			return fmt.Errorf("%w: pending attempt records %s %q, which operation %s never changed",
				ErrCorruptState, role, rel, operation.OperationID)
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
	}
	if err := r.validateConvergence(packageID); err != nil {
		return err
	}
	if r.Receipt != nil && r.Receipt.PackageID != packageID {
		return fmt.Errorf("%w: applied package %s holds a receipt for %s",
			ErrCorruptState, packageID, r.Receipt.PackageID)
	}
	return nil
}

// validateConvergence checks the pairing that separates a converged record from
// an ordinary applied one.
//
// Convergence is exactly the application that produced no recovery entry,
// because there was nothing to undo. A receipt is allowed only when it carries
// the matching convergence claim; an ordinary receipt still requires an entry.
// These are not independent fields that happen to agree: reading a contradictory
// record would answer a redelivery with a promise the vault cannot keep.
func (r Record) validateConvergence(packageID string) error {
	if r.Converged {
		return r.validateConvergedRecord(packageID)
	}
	return r.validateAppliedRecord(packageID)
}

func (r Record) validateConvergedRecord(packageID string) error {
	if strings.TrimSpace(r.JournalEntry) != "" {
		return fmt.Errorf("%w: package %s converged and names journal entry %s",
			ErrCorruptState, packageID, r.JournalEntry)
	}
	if r.Receipt != nil && !r.Receipt.Converged {
		return fmt.Errorf("%w: package %s converged and holds an ordinary receipt", ErrCorruptState, packageID)
	}
	return nil
}

func (r Record) validateAppliedRecord(packageID string) error {
	if strings.TrimSpace(r.JournalEntry) == "" {
		return fmt.Errorf("%w: applied package %s names no journal entry", ErrCorruptState, packageID)
	}
	if r.Receipt != nil && r.Receipt.Converged {
		return fmt.Errorf("%w: applied package %s holds a converged receipt", ErrCorruptState, packageID)
	}
	return nil
}

// clone returns a record a caller can build the next one up from and discard
// wholesale. The attempt's manifest is shared rather than copied: a package is
// immutable by definition and nothing here or in internal/vaultsync ever writes
// through one.
//
// A conflict record is shared for the same reason and with the same rule behind
// it: a hold is replaced wholesale on every pass rather than edited in place, so
// no caller has one to write through. What is copied is the map, which is what
// lets a caller drop a resolved hold without touching the record it is building
// the next state from.
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
	out.Conflicts = make(map[string]*ConflictRecord, len(s.Conflicts))
	for k, v := range s.Conflicts {
		out.Conflicts[k] = v
	}
	out.Resolutions = make(map[string]*Plan, len(s.Resolutions))
	for k, v := range s.Resolutions {
		out.Resolutions[k] = v
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
	if record.Conflicts == nil {
		record.Conflicts = map[string]*ConflictRecord{}
	}
	if record.Resolutions == nil {
		record.Resolutions = map[string]*Plan{}
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
	return saveStateWith(root, record, nil)
}

// saveStateWith permits one application to replace the durable state writer.
// Production passes nil. The recovery qualification uses it to force ENOSPC at
// an exact state transition without a mutable package-global test seam.
func saveStateWith(root string, record *State, write func(string, []byte, fs.FileMode) error) error {
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
	if write == nil {
		write = vault.WriteStateFile
	}
	if err := write(statePath, data, statePerm); err != nil {
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

// ResetAfterReviewContext starts an empty consumer ledger under the reviewed
// enrollment. Restored acknowledgments and applied high-water marks are never
// carried across this boundary; doing so would let a peer mistake a position in
// the old device stream for one in the newly reviewed stream.
//
// It takes the import guard like every other writer of this record, not the
// record's path lock: the guard is what excludes an import, and a reset that an
// import could interleave with would be written over by that import's next
// state write. And it refuses a record still holding unresolved work rather than
// discarding it. The review coordinator archives the record before calling this,
// so the record found here is one written since; RequireResolved is checked
// again because it is this write, not the coordinator, that would destroy it.
func ResetAfterReviewContext(ctx context.Context, root string) error {
	local, err := vaultsync.Load(root)
	if err != nil {
		return err
	}
	_, release, acquired, err := lockImportsContext(ctx, root)
	if err != nil {
		return err
	}
	defer release()
	if !acquired {
		return fmt.Errorf("%w: the import record was not reset", ErrImportBusy)
	}
	if err := RequireResolved(root); err != nil {
		return err
	}
	return saveState(root, newState(local))
}

// RequireResolved refuses while the import record at root holds work an empty
// record would discard: an interrupted application, a package held on a
// conflict, or a conflict decision still owed its revision. It reads the record
// without the identity check loadState makes, because re-enrollment asks it
// across an identity change, and the question — is anything here unfinished —
// does not depend on whose record it is. Absence is the answer "nothing".
func RequireResolved(root string) error {
	statePath := StatePath(root)
	data, err := os.ReadFile(statePath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("vaultimport: read import state %q: %w", statePath, err)
	}
	var record State
	if err := json.Unmarshal(data, &record); err != nil {
		return fmt.Errorf("%w: parse %q: %v", ErrCorruptState, statePath, err)
	}
	if err := record.validate(); err != nil {
		return fmt.Errorf("%s: %w", statePath, err)
	}
	return record.requireResolved()
}

// requireResolved names the first kind of unresolved work the record holds, and
// what clears it. Every vaultimport entry point resolves an interrupted attempt
// and owed revisions before doing anything else, so recovery is "let one run";
// a held package needs a person's decision.
func (s *State) requireResolved() error {
	if s.Pending != nil {
		return fmt.Errorf("%w: package %s was being applied under entry %s when it was interrupted; run import recovery to finish it, then rebuild the inventory",
			ErrUnresolvedImport, s.Pending.PackageID(), s.Pending.JournalEntry)
	}
	if len(s.Conflicts) > 0 {
		return fmt.Errorf("%w: %d package(s) are held on conflicts (%s); resolve them, then rebuild the inventory",
			ErrUnresolvedImport, len(s.Conflicts), strings.Join(sortedKeys(s.Conflicts), ", "))
	}
	if len(s.Resolutions) > 0 {
		return fmt.Errorf("%w: a conflict decision for %s is still owed its revision; run import recovery to make it, then rebuild the inventory",
			ErrUnresolvedImport, strings.Join(sortedKeys(s.Resolutions), ", "))
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
