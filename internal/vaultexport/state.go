package vaultexport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/checkpoint"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// stateVersion is the schema version of the export record. Checked exactly,
// never as a floor, for the reason the enrollment record gives: a newer build
// may carry a field this one would drop on the next write, and dropping a
// cursor or a ledger entry silently is worse than refusing to run.
const stateVersion = 1

// stateFileName is the record's name inside the synchronization state
// directory.
const stateFileName = "export.json"

// statePerm matches the enrollment record's. The file holds identities, object
// names, and path names — no secrets — and lives beside a state directory that
// is already world-readable.
const statePerm fs.FileMode = 0o644

// StatePath returns the absolute path of the export record for the vault at
// root.
//
// It is derived from vaultsync.StateDir rather than spelled out, which is what
// keeps the record inside the directory the portable policy excludes wholesale.
// A literal `.bnotes/sync/export.json` beside a derived StateDir would let the
// two drift: move the synchronization state and this file stays behind, at which
// point a cursor naming every path in the vault becomes portable content.
func StatePath(root string) string {
	return filepath.Join(root, filepath.FromSlash(path.Join(vaultsync.StateDir(), stateFileName)))
}

// State is everything a producer must remember between runs.
//
// It is one file rather than a cursor file and a ledger file, because the two
// are written together or not at all. Advancing the cursor past a commit and
// consuming the import records that commit used are the same decision, and a
// crash between two files would either replay an operation or lose the
// provenance that stops it echoing.
type State struct {
	// Version is stateVersion.
	Version int `json:"version"`
	// VaultID and DeviceID are the enrollment identities this record was
	// written under. They are checked on load: a state file copied from another
	// replica would otherwise make this device continue somebody else's stream.
	VaultID  string `json:"vault_id"`
	DeviceID string `json:"device_id"`
	// Epoch is the enrollment generation the cursor was recorded under.
	Epoch uint64 `json:"epoch"`
	// Branch is the full ref the cursor follows, as in `refs/heads/main`.
	Branch string `json:"branch"`
	// Commit is the last commit fully accounted for, empty before the first
	// export. Every commit up to and including it has either been published or
	// deliberately skipped.
	Commit string `json:"commit,omitempty"`
	// Baseline is the commit an unfinished first export is describing, empty
	// otherwise. A baseline can span several packages, and it is pinned to one
	// commit rather than re-read from the tip each pass: resuming against a
	// newer tip would skip every path already sent, so an edit made to one of
	// them in between would never travel at all. With the commit pinned, the
	// baseline finishes describing the vault as it was, sets Commit to it, and
	// the ordinary incremental pass carries that edit as the commit it was.
	Baseline string `json:"baseline,omitempty"`
	// Sequence is the last sequence number this device used. The next package
	// takes Sequence+1.
	Sequence uint64 `json:"sequence"`
	// Pending is a sequence number and package identity reserved before a
	// publication was attempted, and cleared once its outcome is known. It is
	// what makes an interrupted publication recoverable rather than ambiguous.
	Pending *Reservation `json:"pending,omitempty"`
	// Paths maps a portable vault-relative path to the operation that last
	// changed it, which is what a later change to that path names as its causal
	// predecessor. An entry survives a deletion: a path re-created after a
	// tombstone must name the tombstone, or the peer cannot tell a resurrection
	// from a change that never saw the delete.
	Paths map[string]string `json:"paths"`
	// Imported maps a local journal entry identity to the peer operation that
	// produced it. An entry is written by the applier before it commits and
	// consumed by the exporter when it reaches the resulting commit, which is
	// how an applied change is recognized as the peer's and not sent back.
	Imported map[string]Import `json:"imported"`
}

// Reservation is a sequence number and package identity claimed before a
// publication, so the outcome of an interrupted one can be determined.
type Reservation struct {
	// Sequence is the number claimed for the package.
	Sequence uint64 `json:"sequence"`
	// PackageID is the directory name the package will be published under,
	// which is what makes the attempt findable in the channel afterwards.
	PackageID string `json:"package_id"`
	// Commit is the commit the package describes.
	Commit string `json:"commit"`
	// Baseline is whether the package is one slice of a first export rather
	// than a whole commit. It decides what adopting a recovered publication
	// means: an incremental package that landed advances the cursor to its
	// commit, while a baseline slice that landed only records the paths it
	// carried, because the rest of the baseline has not been sent.
	Baseline bool `json:"baseline,omitempty"`
}

// Import is one applied peer operation, recorded against the local journal
// entry that applying it produced.
type Import struct {
	// OperationID is the peer's operation identity. It becomes the causal
	// predecessor of this vault's later changes to the same paths, so the peer
	// can see that this device had seen its change before making its own.
	OperationID string `json:"operation_id"`
	// OriginDevice is the peer that produced the operation, kept for
	// diagnostics: a ledger entry naming a device this vault no longer enrolls
	// is how a stale import shows itself.
	OriginDevice string `json:"origin_device"`
}

// newState returns the record a vault that has never exported starts from.
func newState(local *vaultsync.Enrollment) *State {
	return &State{
		Version:  stateVersion,
		VaultID:  local.VaultID,
		DeviceID: local.DeviceID,
		Epoch:    local.Epoch,
		Paths:    map[string]string{},
		Imported: map[string]Import{},
	}
}

// validate checks the record against itself. It is deliberately strict about
// shapes that would turn into a wrong query rather than a failure: a cursor that
// is not an object name must fail here, not inside a rev-list argument.
//
// The four parts are separated because they answer different questions — may
// this build read the record at all, whose record is it, what does it claim to
// have sent, and what does it remember about individual paths — and the first
// two must be settled before the rest is worth reading.
func (s *State) validate() error {
	if s == nil {
		return fmt.Errorf("%w: no record", ErrCorruptState)
	}
	if err := s.validateIdentity(); err != nil {
		return err
	}
	if err := s.validateCursor(); err != nil {
		return err
	}
	if err := s.Pending.validate(); err != nil {
		return err
	}
	return s.validateLedgers()
}

// validateIdentity checks what the record is and whose it is.
func (s *State) validateIdentity() error {
	switch {
	case s.Version != stateVersion:
		return fmt.Errorf("%w: record is version %d, this build writes %d", ErrUnsupportedVersion, s.Version, stateVersion)
	case strings.TrimSpace(s.VaultID) == "", strings.TrimSpace(s.DeviceID) == "":
		return fmt.Errorf("%w: record names no vault or device", ErrCorruptState)
	case s.Epoch == 0:
		return fmt.Errorf("%w: record epoch must be at least 1", ErrCorruptState)
	}
	return nil
}

// validateCursor checks what the record claims to have sent. A cursor and an
// unfinished baseline are mutually exclusive: the baseline *is* the claim that
// the commit it names has not been fully sent yet.
func (s *State) validateCursor() error {
	switch {
	case s.Commit != "" && !hexObjectName(s.Commit):
		return fmt.Errorf("%w: cursor %q is not a Git object name", ErrCorruptState, s.Commit)
	case s.Baseline != "" && !hexObjectName(s.Baseline):
		return fmt.Errorf("%w: baseline %q is not a Git object name", ErrCorruptState, s.Baseline)
	case s.Commit != "" && s.Baseline != "":
		return fmt.Errorf("%w: record has both a cursor and an unfinished baseline", ErrCorruptState)
	case (s.Commit != "" || s.Baseline != "") && strings.TrimSpace(s.Branch) == "":
		return fmt.Errorf("%w: cursor names no branch", ErrCorruptState)
	}
	return nil
}

// validateLedgers checks the two per-path memories: what this device last sent
// for a path, and which local journal entries came from a peer.
func (s *State) validateLedgers() error {
	for pathName, operationID := range s.Paths {
		if !vaultsync.Portable(pathName) {
			return fmt.Errorf("%w: ledger names %q, which is not portable content", ErrCorruptState, pathName)
		}
		if strings.TrimSpace(operationID) == "" {
			return fmt.Errorf("%w: ledger entry for %q names no operation", ErrCorruptState, pathName)
		}
	}
	for entryID, record := range s.Imported {
		if strings.TrimSpace(entryID) == "" || strings.TrimSpace(record.OperationID) == "" {
			return fmt.Errorf("%w: import ledger holds an unnamed entry", ErrCorruptState)
		}
	}
	return nil
}

func (r *Reservation) validate() error {
	if r == nil {
		return nil
	}
	switch {
	case r.Sequence == 0:
		return fmt.Errorf("%w: reservation has no sequence", ErrCorruptState)
	case strings.TrimSpace(r.PackageID) == "":
		return fmt.Errorf("%w: reservation names no package", ErrCorruptState)
	case !hexObjectName(r.Commit):
		return fmt.Errorf("%w: reservation commit %q is not a Git object name", ErrCorruptState, r.Commit)
	}
	return nil
}

// clone returns a record sharing no mutable state with the receiver, so an
// export pass can build up the next record and discard it wholesale if a
// package fails to publish.
func (s *State) clone() *State {
	out := *s
	if s.Pending != nil {
		reservation := *s.Pending
		out.Pending = &reservation
	}
	out.Paths = make(map[string]string, len(s.Paths))
	for k, v := range s.Paths {
		out.Paths[k] = v
	}
	out.Imported = make(map[string]Import, len(s.Imported))
	for k, v := range s.Imported {
		out.Imported[k] = v
	}
	return &out
}

// loadState reads the export record for the vault at root, returning a fresh one
// for a vault that has never exported.
//
// Absence is not an error and is not distinguishable from a first run, which is
// correct: both mean this device has told the peer nothing, and the remedy for
// both is a baseline.
func loadState(root string, local *vaultsync.Enrollment) (*State, error) {
	statePath := StatePath(root)
	data, err := os.ReadFile(statePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return newState(local), nil
		}
		return nil, fmt.Errorf("vaultexport: read export state %q: %w", statePath, err)
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
	if record.Paths == nil {
		record.Paths = map[string]string{}
	}
	if record.Imported == nil {
		record.Imported = map[string]Import{}
	}
	return &record, nil
}

// saveState writes the export record. The caller must hold the state path's
// lock.
//
// vault.WriteStateFile rather than vaultstate.Save, for the reason the
// enrollment record states: Save's third guarantee is a snapshot into the
// content history, and `.bnotes/sync/` is out of that history's scope by design.
// Save would not refuse the path — it would drop it and report success — so a
// reader of Save's contract here would believe this file was snapshotted. The
// two guarantees that do apply, the storage-location guard and the
// path-continuous replacement, are taken directly.
//
// It is also what keeps this package out of internal/audit's approved list: no
// production file here opens, creates, renames, or removes anything, so there is
// no direct-write marker to justify and no second copy of the durability
// argument to keep in step.
func saveState(root string, record *State) error {
	if err := record.validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("vaultexport: encode export state: %w", err)
	}
	data = append(data, '\n')

	statePath := StatePath(root)
	// Ahead of any artifact, like every other mutation in this repository.
	if err := vault.GuardMutation(root, statePath); err != nil {
		return err
	}
	if err := vault.WriteStateFile(statePath, data, statePerm); err != nil {
		return fmt.Errorf("vaultexport: write export state: %w", err)
	}
	return nil
}

// LoadState returns a copy of the export record for the vault at root, for a
// caller reporting synchronization status. It is read-only: nothing here
// advances a cursor.
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

// RecordImport durably records that applying a peer operation produced the local
// journal entry journalEntryID.
//
// The ordering is a contract and it is one-directional: this must be durable
// *before* the applying operation's journal entry is finalized and committed.
// Written after the commit, a crash in between leaves a commit the exporter
// cannot recognize as the peer's, and it publishes the peer's own change back to
// it under this device's identity — an echo the peer will apply, commit, and
// (having no record either) send back. Written before, the failure is a ledger
// entry for a journal entry that never commits, which costs one stale map entry
// and changes nothing: the exporter only ever consults the ledger for entries
// named by a commit it is looking at.
//
// Recording the same entry twice is a no-op when the operation matches and an
// error when it does not, so a retried apply is safe and a confused one is not
// silently overwritten.
func RecordImport(root, journalEntryID string, record Import) error {
	return RecordImportContext(context.Background(), root, journalEntryID, record)
}

// RecordImportContext is RecordImport for an import that already owns the
// vault-wide checkpoint admission.
func RecordImportContext(ctx context.Context, root, journalEntryID string, record Import) error {
	if strings.TrimSpace(journalEntryID) == "" {
		return fmt.Errorf("vaultexport: journal entry identity is required")
	}
	if strings.TrimSpace(record.OperationID) == "" {
		return fmt.Errorf("vaultexport: imported operation identity is required")
	}
	local, err := vaultsync.Load(root)
	if err != nil {
		return err
	}
	_, barrier, err := joinBarrierContext(ctx, root)
	if err != nil {
		return err
	}
	defer barrier()
	release, err := vault.LockPaths(root, StatePath(root))
	if err != nil {
		return fmt.Errorf("vaultexport: lock export state: %w", err)
	}
	defer release()

	current, err := loadState(root, local)
	if err != nil {
		return err
	}
	if existing, known := current.Imported[journalEntryID]; known {
		if existing != record {
			return fmt.Errorf("%w: journal entry %s is already recorded as operation %s",
				ErrCorruptState, journalEntryID, existing.OperationID)
		}
		return nil
	}
	next := current.clone()
	next.Imported[journalEntryID] = record
	return saveState(root, next)
}

// Reset clears the cursor, the path ledger, and any reservation, keeping the
// identity and the sequence.
//
// It is the remedy for the two halts that are not faults — a rewritten history
// and an advanced epoch — and it re-baselines rather than resuming: the next
// export describes the whole portable tree as if the peer held nothing. The
// sequence is *kept* rather than restarted, because a peer that saw the old
// stream must see the new one as later, and reusing a number the peer already
// has would make two different packages the same position in this device's
// history.
//
// A reservation is dropped but its number is *spent*, not released. Reset cannot
// tell which of resolveReservation's three outcomes that publication had — that
// answer is in the channel, and Reset does not look there — so it takes the only
// safe one. Releasing the number would hand it to the next pass while a complete
// package carrying it may already be on its way to the peer, which is precisely
// the collision the sequence is kept for.
//
// The import ledger is kept too. An entry is written before the commit that
// applies it exists and is consumed only when a replay reaches that commit, so
// Reset can land in between — and the commits after the new baseline's pin may
// themselves be applied imports. Dropping the ledger would make the next replay
// read one of those commits as a local change and send the peer its own operation
// straight back.
func Reset(root string) error {
	local, err := vaultsync.Load(root)
	if err != nil {
		return err
	}
	barrier, err := joinBarrier(root)
	if err != nil {
		return err
	}
	defer barrier()
	release, err := vault.LockPaths(root, StatePath(root))
	if err != nil {
		return fmt.Errorf("vaultexport: lock export state: %w", err)
	}
	defer release()

	current, err := loadState(root, local)
	if err != nil {
		return err
	}
	next := current.clone()
	next.Epoch = local.Epoch
	next.Branch = ""
	next.Commit = ""
	next.Baseline = ""
	if current.Pending != nil && current.Pending.Sequence > next.Sequence {
		next.Sequence = current.Pending.Sequence
	}
	next.Pending = nil
	next.Paths = map[string]string{}
	return saveState(root, next)
}

// hexObjectName reports whether s is a full lowercase hex Git object name.
// Restated here rather than reached for across the package boundary: this is a
// check on bytes read back off a disk, and it has to hold whether or not
// internal/vaultgit is ever consulted about them.
func hexObjectName(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// joinBarrier enlists a synchronization-state write in the vault's checkpoint
// barrier, which every such write takes before it takes the state lock. The
// order is the one internal/checkpoint documents and is not negotiable: a writer
// that joined after a vault lock would close a cycle against a checkpoint's
// settle pass.
func joinBarrier(root string) (func(), error) {
	_, release, err := joinBarrierContext(context.Background(), root)
	return release, err
}

func joinBarrierContext(ctx context.Context, root string) (context.Context, func(), error) {
	joinedCtx, release, err := checkpoint.JoinContext(ctx, root)
	if err != nil {
		return ctx, nil, fmt.Errorf("vaultexport: %w", err)
	}
	return joinedCtx, release, nil
}
