package vaultsync

import (
	"fmt"
	"time"
)

// enrollmentVersion is the schema version of the enrollment record. It is
// checked exactly, never as a floor: a record written by a newer build may
// carry a field this one would drop on the next write, and silently dropping a
// peer or an epoch is worse than refusing to run.
const enrollmentVersion = 1

// Enrollment is one replica's durable synchronization identity.
//
// It is the answer to three questions a received package asks: which vault am
// I, who am I, and whom do I accept from. Everything else about a package —
// what it changed, in what order, depending on what — is in the package.
type Enrollment struct {
	// Version is enrollmentVersion.
	Version int `json:"version"`
	// VaultID is shared by every replica of one vault and never changes. It is
	// what makes a package addressed to somebody else's vault a refusal rather
	// than a merge.
	VaultID string `json:"vault_id"`
	// DeviceID is this replica's own identity, minted here and never reused by
	// another replica. It is the origin stamped on every package this device
	// publishes and the acknowledging device on every receipt it mints.
	DeviceID string `json:"device_id"`
	// Label is a human name for this device ("mac", "pi"). It is for messages
	// only: nothing routes, matches, or authorizes on it.
	Label string `json:"label"`
	// Epoch is the enrollment generation. It starts at 1 and advances only
	// through AdvanceEpoch, which is how a vault restored from a backup tells
	// its peer that every package still in flight describes a history this
	// vault no longer has.
	Epoch uint64 `json:"epoch"`
	// EnrolledAt is when the baseline was established.
	EnrolledAt time.Time `json:"enrolled_at"`
	// EpochChangedAt is when Epoch last moved. Equal to EnrolledAt at baseline.
	EpochChangedAt time.Time `json:"epoch_changed_at"`
	// Peers are the devices this replica accepts packages from. A device not
	// listed here is refused, including this device itself: a package that
	// arrived in the incoming channel claiming to be from this device is a
	// misconfigured transport, not a loopback.
	Peers []Peer `json:"peers"`
}

// Peer is another enrolled replica of the same vault.
type Peer struct {
	// DeviceID is the peer's identity, learned from its invitation.
	DeviceID string `json:"device_id"`
	// Label is the peer's human name, for messages only.
	Label string `json:"label"`
	// JoinedEpoch is the epoch the peer was recorded under. It is history
	// rather than policy — acceptance compares a package against the vault's
	// current epoch — and exists so a person can tell when a device joined.
	JoinedEpoch uint64 `json:"joined_epoch"`
}

// Invitation is what one enrolled device hands another so the two end up
// replicas of the same vault.
//
// It carries identity and nothing secret. Device pairing and transport
// authorization are Syncthing's, and an invitation that leaked would let
// somebody name this vault correctly in a package they still have no way to
// deliver.
type Invitation struct {
	// VaultID is the vault the invited device joins.
	VaultID string `json:"vault_id"`
	// Epoch is the inviting device's epoch at the time of the invitation. The
	// joining device adopts it, so the two agree from their first exchange.
	Epoch uint64 `json:"epoch"`
	// DeviceID is the inviting device, which the joining device records as its
	// first peer.
	DeviceID string `json:"device_id"`
	// Label is the inviting device's human name.
	Label string `json:"label"`
}

// Invite returns the invitation another device joins this vault with.
func (e *Enrollment) Invite() Invitation {
	return Invitation{
		VaultID:  e.VaultID,
		Epoch:    e.Epoch,
		DeviceID: e.DeviceID,
		Label:    e.Label,
	}
}

// Peer returns the enrolled peer with this device identity.
func (e *Enrollment) Peer(deviceID string) (Peer, bool) {
	for _, peer := range e.Peers {
		if peer.DeviceID == deviceID {
			return peer, true
		}
	}
	return Peer{}, false
}

// clone returns a deep copy, so a caller holding a loaded record cannot mutate
// the slice a later verification compares against.
func (e *Enrollment) clone() *Enrollment {
	if e == nil {
		return nil
	}
	copied := *e
	copied.Peers = append([]Peer(nil), e.Peers...)
	return &copied
}

// equal reports whether two records describe the same enrollment. It is what
// makes a written baseline *verified*: the record is read back from disk and
// compared field by field against what was written.
func (e *Enrollment) equal(other *Enrollment) bool {
	switch {
	case e == nil || other == nil:
		return e == other
	case e.Version != other.Version, e.VaultID != other.VaultID:
		return false
	case e.DeviceID != other.DeviceID, e.Label != other.Label:
		return false
	case e.Epoch != other.Epoch:
		return false
	case !e.EnrolledAt.Equal(other.EnrolledAt), !e.EpochChangedAt.Equal(other.EpochChangedAt):
		return false
	case len(e.Peers) != len(other.Peers):
		return false
	}
	for i, peer := range e.Peers {
		if peer != other.Peers[i] {
			return false
		}
	}
	return true
}

// Validate reports whether the record is internally consistent. It runs on
// every load and on every write, so a record that would be refused on the next
// read is refused before it reaches the disk.
func (e *Enrollment) Validate() error {
	if e == nil {
		return fmt.Errorf("%w: no enrollment record", ErrCorruptState)
	}
	if e.Version != enrollmentVersion {
		return fmt.Errorf("%w: got %d want %d", ErrUnsupportedVersion, e.Version, enrollmentVersion)
	}
	if err := checkIdentity(ErrCorruptState, "vault_id", vaultPrefix, e.VaultID); err != nil {
		return err
	}
	if err := checkIdentity(ErrCorruptState, "device_id", devicePrefix, e.DeviceID); err != nil {
		return err
	}
	if e.Epoch == 0 {
		return fmt.Errorf("%w: epoch must be at least 1", ErrCorruptState)
	}
	if e.EnrolledAt.IsZero() {
		return fmt.Errorf("%w: enrolled_at is unset", ErrCorruptState)
	}
	return e.validatePeers()
}

func (e *Enrollment) validatePeers() error {
	seen := make(map[string]struct{}, len(e.Peers))
	for i, peer := range e.Peers {
		if err := checkIdentity(ErrCorruptState, fmt.Sprintf("peers[%d].device_id", i), devicePrefix, peer.DeviceID); err != nil {
			return err
		}
		if peer.DeviceID == e.DeviceID {
			return fmt.Errorf("%w: this device is listed as its own peer", ErrCorruptState)
		}
		if _, duplicate := seen[peer.DeviceID]; duplicate {
			return fmt.Errorf("%w: peer %q is listed twice", ErrCorruptState, peer.DeviceID)
		}
		seen[peer.DeviceID] = struct{}{}
	}
	return nil
}
