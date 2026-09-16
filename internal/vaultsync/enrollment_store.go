package vaultsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// enrollmentPerm is the mode of the enrollment record. It holds identities
// rather than secrets — a device identifier authorizes nothing on its own — so
// it is world-readable like every other state file in .bnotes.
const enrollmentPerm fs.FileMode = 0o644

// Options carry the two things every enrollment mutation needs from its caller.
// The zero value is usable: an empty Label is allowed and Now defaults to the
// wall clock.
type Options struct {
	// Label is the human name of the device being enrolled.
	Label string
	// Now is the clock seam every package in this repository accepts, so a test
	// can pin the instants a record records.
	Now func() time.Time
}

func (o Options) now() time.Time {
	if o.Now == nil {
		return time.Now().UTC()
	}
	return o.Now().UTC()
}

// Load reads the enrollment record for the vault at root.
//
// The three ways a load fails are distinct and stay distinct all the way out:
// absence is ErrNotEnrolled and is the ordinary state of a vault that
// synchronizes with nothing, undecodable or self-contradictory bytes are
// ErrCorruptState, and a record from a newer build is ErrUnsupportedVersion.
// Collapsing them would make "this vault does not synchronize" and "this
// vault's identity is damaged" look the same to the layer that has to decide
// whether to offer enrolling.
func Load(root string) (*Enrollment, error) {
	path := EnrollmentPath(root)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotEnrolled, path)
		}
		return nil, fmt.Errorf("vaultsync: read enrollment %q: %w", path, err)
	}
	return decodeEnrollment(path, data)
}

func decodeEnrollment(path string, data []byte) (*Enrollment, error) {
	var record Enrollment
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("%w: parse %q: %v", ErrCorruptState, path, err)
	}
	if err := record.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &record, nil
}

// Establish creates the baseline enrollment for the vault at root: a fresh
// vault identity, this device's identity, epoch 1, and no peers.
//
// It refuses a vault that is already enrolled. Replacing an identity a peer is
// already addressing would not reset synchronization — it would leave the peer
// publishing into a channel nobody reads, with no error anywhere.
//
// The baseline is *verified* rather than assumed: the record is read back from
// disk and compared field by field against what was written, and a mismatch is
// ErrUnverifiedEnrollment. Every later exchange is addressed by these bytes, so
// this is the one write where reading it back is worth the syscall.
func Establish(root string, opts Options) (*Enrollment, error) {
	vaultID, err := NewVaultID()
	if err != nil {
		return nil, err
	}
	deviceID, err := NewDeviceID()
	if err != nil {
		return nil, err
	}
	at := opts.now()
	record := &Enrollment{
		Version:        enrollmentVersion,
		VaultID:        vaultID,
		DeviceID:       deviceID,
		Label:          opts.Label,
		Epoch:          1,
		EnrolledAt:     at,
		EpochChangedAt: at,
		Peers:          []Peer{},
	}
	return createEnrollment(root, record)
}

// Join enrolls the vault at root as a second replica of the vault the
// invitation names. It mints this device's own identity, adopts the vault
// identity and the epoch from the invitation, and records the inviting device
// as its first peer.
//
// The inviting device must record this device in turn, through RecordPeer,
// before it will accept anything: acceptance is symmetric, and a one-sided
// enrollment is a vault that sends and is never answered.
func Join(root string, invite Invitation, opts Options) (*Enrollment, error) {
	if err := checkIdentity(ErrCorruptState, "vault_id", vaultPrefix, invite.VaultID); err != nil {
		return nil, err
	}
	if err := checkIdentity(ErrCorruptState, "device_id", devicePrefix, invite.DeviceID); err != nil {
		return nil, err
	}
	if invite.Epoch == 0 {
		return nil, fmt.Errorf("%w: invitation epoch must be at least 1", ErrCorruptState)
	}
	deviceID, err := NewDeviceID()
	if err != nil {
		return nil, err
	}
	at := opts.now()
	record := &Enrollment{
		Version:        enrollmentVersion,
		VaultID:        invite.VaultID,
		DeviceID:       deviceID,
		Label:          opts.Label,
		Epoch:          invite.Epoch,
		EnrolledAt:     at,
		EpochChangedAt: at,
		Peers: []Peer{{
			DeviceID:    invite.DeviceID,
			Label:       invite.Label,
			JoinedEpoch: invite.Epoch,
		}},
	}
	return createEnrollment(root, record)
}

// RecordPeer adds a device to the set this vault accepts packages from, and
// returns the updated record. Recording a peer that is already enrolled is a
// no-op rather than an error, so a repeated enrollment step is safe.
func RecordPeer(root string, peer Peer) (*Enrollment, error) {
	return updateEnrollment(root, func(record *Enrollment) error {
		if err := checkIdentity(ErrCorruptState, "device_id", devicePrefix, peer.DeviceID); err != nil {
			return err
		}
		if peer.DeviceID == record.DeviceID {
			return fmt.Errorf("%w: a vault cannot enroll itself as a peer", ErrCorruptState)
		}
		if _, known := record.Peer(peer.DeviceID); known {
			return nil
		}
		if peer.JoinedEpoch == 0 {
			peer.JoinedEpoch = record.Epoch
		}
		record.Peers = append(record.Peers, peer)
		return nil
	})
}

// AdvanceEpoch moves this vault into a new enrollment generation and returns
// the updated record.
//
// This is what a vault does after being restored from a backup. Its peer's
// outgoing channel may still hold packages whose expected content hashes
// describe notes this vault no longer has, and applying one would report
// success while writing the wrong bytes. Advancing the epoch makes every one of
// them ErrStaleEpoch instead, and the peer learns the new epoch the first time
// this vault publishes under it.
func AdvanceEpoch(root string, opts Options) (*Enrollment, error) {
	return updateEnrollment(root, func(record *Enrollment) error {
		record.Epoch++
		record.EpochChangedAt = opts.now()
		return nil
	})
}

// createEnrollment writes a record into a vault that has none, under the same
// lock every other enrollment mutation takes, so two processes establishing at
// once cannot both see an unenrolled vault.
func createEnrollment(root string, record *Enrollment) (*Enrollment, error) {
	path := EnrollmentPath(root)
	release, err := vault.LockPaths(root, path)
	if err != nil {
		return nil, fmt.Errorf("vaultsync: lock enrollment: %w", err)
	}
	defer release()

	switch _, err := Load(root); {
	case err == nil:
		return nil, fmt.Errorf("%w: %s", ErrAlreadyEnrolled, path)
	case errors.Is(err, ErrNotEnrolled):
	default:
		return nil, err
	}
	return writeEnrollmentLocked(root, record)
}

// updateEnrollment applies mutate to the current record and writes the result.
// The load, the mutation, and the write are one critical section: an epoch
// advanced twice concurrently must advance twice, not once.
func updateEnrollment(root string, mutate func(*Enrollment) error) (*Enrollment, error) {
	path := EnrollmentPath(root)
	release, err := vault.LockPaths(root, path)
	if err != nil {
		return nil, fmt.Errorf("vaultsync: lock enrollment: %w", err)
	}
	defer release()

	record, err := Load(root)
	if err != nil {
		return nil, err
	}
	if err := mutate(record); err != nil {
		return nil, err
	}
	return writeEnrollmentLocked(root, record)
}

// writeEnrollmentLocked validates, writes, and verifies. It must be called with
// the enrollment path locked.
func writeEnrollmentLocked(root string, record *Enrollment) (*Enrollment, error) {
	if err := record.Validate(); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("vaultsync: encode enrollment: %w", err)
	}
	data = append(data, '\n')

	path := EnrollmentPath(root)
	// Ahead of any artifact, like every other mutation in this repository: a
	// refusal must not leave the state directory behind on a vault that is not
	// going to be enrolled.
	if err := vault.GuardMutation(root, path); err != nil {
		return nil, err
	}
	// vault.WriteStateFile rather than vaultstate.Save, deliberately. Save's
	// third guarantee is a snapshot into the content history, and this path is
	// out of that history's scope by design: the enrollment record names this
	// machine, and internal/vaultgit's allowlist does not include it.
	//
	// Save would not *refuse* it — CommitPaths drops an out-of-scope path and
	// returns an empty result, so Save runs a vaultgit preflight and then
	// reports success with its third guarantee silently inapplicable. That is
	// the reason to stay out of it rather than a reason it would be caught: a
	// caller reading Save's contract here would believe the record was
	// snapshotted. The guard above and the path-continuous replacement below
	// are the two guarantees that do apply, and they are taken here rather
	// than skipped.
	if err := vault.WriteStateFile(path, data, enrollmentPerm); err != nil {
		return nil, fmt.Errorf("vaultsync: write enrollment: %w", err)
	}

	written, err := Load(root)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnverifiedEnrollment, err)
	}
	if !written.equal(record) {
		return nil, fmt.Errorf("%w: %s does not read back as it was written", ErrUnverifiedEnrollment, path)
	}
	return written.clone(), nil
}
