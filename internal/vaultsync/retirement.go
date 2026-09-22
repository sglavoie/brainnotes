package vaultsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// RetirementName is the mutable high-water record in a device's outgoing
// channel. Packages at or below Through have left that sender permanently.
const RetirementName = "retirement.json"

// Retirement is a sender-authored promise that positions in one origin stream
// can no longer be delivered. It is a high-water mark rather than one tombstone
// per package, so the evidence itself stays bounded over the vault's lifetime.
type Retirement struct {
	Format       string `json:"format"`
	VaultID      string `json:"vault_id"`
	OriginDevice string `json:"origin_device"`
	Epoch        uint64 `json:"epoch"`
	Through      uint64 `json:"through"`
	// Retained is the exact set of packages at or below Through the sender still
	// owns and can redeliver. Its size follows outstanding transport work rather
	// than vault lifetime; everything covered but absent from this set is retired.
	Retained []RetainedPackage `json:"retained,omitempty"`
}

// RetainedPackage is one exception to a retirement high-water mark.
type RetainedPackage struct {
	PackageID string `json:"package_id"`
	Sequence  uint64 `json:"sequence"`
}

// Validate checks the record without deciding whether its origin is enrolled by
// a particular receiver.
func (r *Retirement) Validate() error {
	if r == nil {
		return fmt.Errorf("%w: no retirement record", ErrMalformedPackage)
	}
	if err := r.validateIdentity(); err != nil {
		return err
	}
	return r.validateRetained()
}

func (r *Retirement) validateIdentity() error {
	if r.Format != Format {
		return fmt.Errorf("%w: retirement record format %q", ErrUnsupportedFormat, r.Format)
	}
	for _, field := range []struct {
		name, prefix, value string
	}{
		{"vault_id", vaultPrefix, r.VaultID},
		{"origin_device", devicePrefix, r.OriginDevice},
	} {
		if err := checkIdentity(ErrMalformedPackage, field.name, field.prefix, field.value); err != nil {
			return err
		}
	}
	if r.Epoch == 0 || r.Through == 0 {
		return fmt.Errorf("%w: retirement epoch and through must be at least 1", ErrMalformedPackage)
	}
	return nil
}

func (r *Retirement) validateRetained() error {
	seen := make(map[string]bool, len(r.Retained))
	for _, retained := range r.Retained {
		if err := checkIdentity(ErrMalformedPackage, "retained package_id", packagePrefix, retained.PackageID); err != nil {
			return err
		}
		if retained.Sequence == 0 || retained.Sequence > r.Through {
			return fmt.Errorf("%w: retained package %s has sequence %d beyond high-water mark %d",
				ErrMalformedPackage, retained.PackageID, retained.Sequence, r.Through)
		}
		if seen[retained.PackageID] {
			return fmt.Errorf("%w: retained package %s is listed twice", ErrMalformedPackage, retained.PackageID)
		}
		seen[retained.PackageID] = true
	}
	return nil
}

// Retains reports whether the sender can still redeliver packageID.
func (r *Retirement) Retains(packageID string) bool {
	for _, retained := range r.Retained {
		if retained.PackageID == packageID {
			return true
		}
	}
	return false
}

// VerifyFor checks that this receiver may trust the retirement promise. Channel
// ownership is the authority: only the enrolled origin writes its outgoing
// channel, which the transport mirrors into this receiver's incoming channel.
func (r *Retirement) VerifyFor(receiver *Enrollment) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if receiver == nil {
		return fmt.Errorf("%w: no local enrollment", ErrNotEnrolled)
	}
	if err := receiver.Validate(); err != nil {
		return err
	}
	if r.VaultID != receiver.VaultID {
		return fmt.Errorf("%w: retirement record names vault %q, this vault is %q",
			ErrWrongVault, r.VaultID, receiver.VaultID)
	}
	if r.OriginDevice == receiver.DeviceID {
		return fmt.Errorf("%w: a device cannot retire its own incoming packages", ErrUnknownPeer)
	}
	if _, enrolled := receiver.Peer(r.OriginDevice); !enrolled {
		return fmt.Errorf("%w: %q", ErrUnknownPeer, r.OriginDevice)
	}
	switch {
	case r.Epoch < receiver.Epoch:
		return fmt.Errorf("%w: retirement epoch %d, this vault is at %d", ErrStaleEpoch, r.Epoch, receiver.Epoch)
	case r.Epoch > receiver.Epoch:
		return fmt.Errorf("%w: retirement epoch %d, this vault is at %d", ErrEpochAhead, r.Epoch, receiver.Epoch)
	}
	return nil
}

// RetirementPath returns the single mutable retirement record in channelDir.
func RetirementPath(channelDir string) string {
	return filepath.Join(channelDir, RetirementName)
}

// PublishRetirement advances the sender's durable retirement high-water mark.
// It refuses regressions within an epoch because a receiver may already have
// pruned every application record covered by the higher value.
func PublishRetirement(channelDir string, record *Retirement) (string, error) {
	if err := record.Validate(); err != nil {
		return "", err
	}
	record = record.cloneSorted()
	current, err := ReadRetirement(channelDir)
	if err != nil {
		return "", err
	}
	unchanged, err := validateRetirementAdvance(current, record)
	if err != nil {
		return "", err
	}
	if unchanged {
		return RetirementPath(channelDir), nil
	}
	return writeRetirement(channelDir, record)
}

func validateRetirementAdvance(current, next *Retirement) (bool, error) {
	if current == nil {
		return false, nil
	}
	if current.VaultID != next.VaultID || current.OriginDevice != next.OriginDevice {
		return false, fmt.Errorf("%w: retirement record changed origin", ErrMalformedPackage)
	}
	if next.Epoch < current.Epoch {
		return false, fmt.Errorf("%w: retirement epoch regressed from %d to %d",
			ErrStaleEpoch, current.Epoch, next.Epoch)
	}
	if next.Epoch > current.Epoch {
		return false, nil
	}
	if next.Through < current.Through {
		return false, fmt.Errorf("%w: retirement high-water mark regressed from %d to %d",
			ErrMalformedPackage, current.Through, next.Through)
	}
	if reintroducesRetired(current, next) {
		return false, fmt.Errorf("%w: retirement record makes a retired package redeliverable",
			ErrMalformedPackage)
	}
	return next.Through == current.Through && sameRetained(current, next), nil
}

func writeRetirement(channelDir string, record *Retirement) (string, error) {
	path := RetirementPath(channelDir)
	if err := vault.GuardMutation("", path); err != nil {
		return "", err
	}
	if err := ensureChannel(channelDir); err != nil {
		return "", err
	}
	data, err := encodeRecord(record)
	if err != nil {
		return "", err
	}
	if err := vault.WriteStateFile(path, data, publishPerm); err != nil {
		return "", fmt.Errorf("vaultsync: publish retirement record %q: %w", path, err)
	}
	return path, nil
}

func (r *Retirement) cloneSorted() *Retirement {
	out := *r
	out.Retained = append([]RetainedPackage(nil), r.Retained...)
	sort.Slice(out.Retained, func(i, j int) bool {
		if out.Retained[i].Sequence != out.Retained[j].Sequence {
			return out.Retained[i].Sequence < out.Retained[j].Sequence
		}
		return out.Retained[i].PackageID < out.Retained[j].PackageID
	})
	return &out
}

func reintroducesRetired(current, next *Retirement) bool {
	retained := make(map[string]uint64, len(current.Retained))
	for _, item := range current.Retained {
		retained[item.PackageID] = item.Sequence
	}
	for _, item := range next.Retained {
		if item.Sequence <= current.Through && retained[item.PackageID] != item.Sequence {
			return true
		}
	}
	return false
}

func sameRetained(a, b *Retirement) bool {
	if len(a.Retained) != len(b.Retained) {
		return false
	}
	for i := range a.Retained {
		if a.Retained[i] != b.Retained[i] {
			return false
		}
	}
	return true
}

// ReadRetirement returns nil for a channel that has not retired a position yet.
func ReadRetirement(channelDir string) (*Retirement, error) {
	path := RetirementPath(channelDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("vaultsync: read retirement record %q: %w", path, err)
	}
	if err := checkRecordFormat(data, path, ErrMalformedPackage); err != nil {
		return nil, err
	}
	var record Retirement
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("%w: parse %q: %v", ErrMalformedPackage, path, err)
	}
	if err := record.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &record, nil
}
