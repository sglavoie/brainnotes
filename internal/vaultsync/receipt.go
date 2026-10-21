package vaultsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/brainnotes/internal/vault"
)

// receiptSuffix is the extension of a published receipt.
const receiptSuffix = ".json"

// Receipt is one device's acknowledgment that it durably holds a package's
// result.
//
// It is the only statement in this protocol that a change has actually landed.
// A file transfer completing says a byte sequence reached a disk; a package
// directory appearing says a publication finished; neither says a note in a
// live vault changed, and the producer must not drop its own copy of a change
// on either. A receipt does say that. When the package changed the vault, the
// receipt names the local recovery-history entry that can undo it, so the claim
// is answerable rather than merely asserted — by an operator, and by the layer
// that owns the recovery history. When the vault already held every result, the
// receipt instead says Converged and names no entry because there was nothing to
// undo. In both cases Applied proves the package's complete result is present.
// This package cannot import internal/journal, so it checks the ordinary entry
// is named and never that it exists.
type Receipt struct {
	// Format is the wire format token, always Format.
	Format string `json:"format"`
	// VaultID is the vault both replicas share.
	VaultID string `json:"vault_id"`
	// PackageID is the package being acknowledged.
	PackageID string `json:"package_id"`
	// OriginDevice is the device that published the package. Carried so the
	// producer can match a receipt to its own stream without opening the
	// package it is about.
	OriginDevice string `json:"origin_device"`
	// OriginSequence is that package's position in the origin's stream, which
	// is what lets a producer advance a high-water mark from receipts alone.
	OriginSequence uint64 `json:"origin_sequence"`
	// Device is the acknowledging device — the one that applied the package.
	Device string `json:"device"`
	// Epoch is the enrollment epoch the application happened under. A receipt
	// from a superseded epoch acknowledges an application to a history that no
	// longer exists.
	Epoch uint64 `json:"epoch"`
	// AppliedAt is when the application became durable here.
	AppliedAt time.Time `json:"applied_at"`
	// RecoveryEntry is the identity of the local recovery-history entry that
	// can undo the application. It is required unless Converged is true.
	RecoveryEntry string `json:"recovery_entry,omitempty"`
	// Converged reports that this vault already held every result the package
	// describes, so the acknowledgment needs no recovery entry: no package byte
	// was written and there is nothing to undo.
	Converged bool `json:"converged,omitempty"`
	// Applied is what landed, one entry per path the package changed.
	Applied []AppliedPath `json:"applied"`
}

// AppliedPath is one path's outcome on the receiving device.
type AppliedPath struct {
	// Path is the vault-relative path that changed.
	Path string `json:"path"`
	// Action is what happened to it.
	Action Action `json:"action"`
	// SHA256 is the content the path holds now, empty for a tombstone. It is
	// the hash of what is on *this* disk, not a copy of the package's claim:
	// the two being compared is the point.
	SHA256 string `json:"sha256,omitempty"`
}

// Application is what the layer that applied a package hands back to mint a
// receipt for it.
type Application struct {
	// RecoveryEntry identifies the local recovery-history entry that can undo
	// the application. It is empty only when Converged is true.
	RecoveryEntry string
	// Converged reports that the vault already held every package result and the
	// application therefore wrote nothing.
	Converged bool
	// Applied is the per-path outcome, which must cover the package exactly.
	Applied []AppliedPath
	// Now is the clock seam.
	Now func() time.Time
}

func (a Application) now() time.Time {
	if a.Now == nil {
		return time.Now().UTC()
	}
	return a.Now().UTC()
}

// Acknowledge mints the receipt local owes for durably holding m's result.
//
// It refuses three things, and each one is a way an acknowledgment could be a
// lie the peer would act on by discarding its only copy of a change: a package
// this vault could not have accepted in the first place, an application with no
// recovery entry behind it unless it explicitly converged, and an application
// that does not cover every path the package changed with the bytes the package
// said those paths would hold.
func Acknowledge(local *Enrollment, m *Manifest, applied Application) (*Receipt, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if err := m.CheckEnrollment(local); err != nil {
		return nil, err
	}
	switch {
	case applied.Converged && strings.TrimSpace(applied.RecoveryEntry) != "":
		return nil, fmt.Errorf("%w: converged package %s names recovery entry %s",
			ErrReceiptMismatch, m.PackageID, applied.RecoveryEntry)
	case !applied.Converged && strings.TrimSpace(applied.RecoveryEntry) == "":
		return nil, fmt.Errorf("%w: package %s", ErrNoRecoveryEntry, m.PackageID)
	}
	if err := coversPackage(m, applied.Applied); err != nil {
		return nil, err
	}
	receipt := &Receipt{
		Format:         Format,
		VaultID:        local.VaultID,
		PackageID:      m.PackageID,
		OriginDevice:   m.OriginDevice,
		OriginSequence: m.Sequence,
		Device:         local.DeviceID,
		Epoch:          local.Epoch,
		AppliedAt:      applied.now(),
		RecoveryEntry:  applied.RecoveryEntry,
		Converged:      applied.Converged,
		Applied:        append([]AppliedPath(nil), applied.Applied...),
	}
	if err := receipt.Validate(); err != nil {
		return nil, err
	}
	return receipt, nil
}

// coversPackage reports whether applied accounts for every change in m, and
// only for those.
func coversPackage(m *Manifest, applied []AppliedPath) error {
	expected := make(map[string]Change, len(m.Paths()))
	for _, change := range m.Changes() {
		expected[change.Path] = change
	}
	seen := make(map[string]struct{}, len(applied))
	for _, entry := range applied {
		change, wanted := expected[entry.Path]
		if !wanted {
			return fmt.Errorf("%w: %q is not changed by %s", ErrIncompleteApplication, entry.Path, m.PackageID)
		}
		if _, duplicate := seen[entry.Path]; duplicate {
			return fmt.Errorf("%w: %q is acknowledged twice", ErrIncompleteApplication, entry.Path)
		}
		seen[entry.Path] = struct{}{}
		if err := matchesChange(entry, change); err != nil {
			return err
		}
	}
	return missingPaths(m, seen)
}

func matchesChange(entry AppliedPath, change Change) error {
	if entry.Action != change.Action {
		return fmt.Errorf("%w: %q was %s, the package says %s", ErrIncompleteApplication, entry.Path, entry.Action, change.Action)
	}
	if entry.SHA256 != change.ResultSHA256 {
		return fmt.Errorf("%w: %q holds %q, the package says %q", ErrIncompleteApplication, entry.Path, entry.SHA256, change.ResultSHA256)
	}
	return nil
}

// missingPaths reports the first package path no acknowledgment covers, in
// manifest order so the message is the same on every run.
func missingPaths(m *Manifest, seen map[string]struct{}) error {
	for _, path := range m.Paths() {
		if _, ok := seen[path]; !ok {
			return fmt.Errorf("%w: %q was not acknowledged", ErrIncompleteApplication, path)
		}
	}
	return nil
}

// Validate reports whether the receipt is internally consistent.
func (r *Receipt) Validate() error {
	if r == nil {
		return fmt.Errorf("%w: no receipt", ErrReceiptMismatch)
	}
	if r.Format != Format {
		return fmt.Errorf("%w: got %q want %q", ErrUnsupportedFormat, r.Format, Format)
	}
	if err := validateReceiptIdentities(r); err != nil {
		return err
	}
	if r.OriginSequence == 0 || r.Epoch == 0 {
		return fmt.Errorf("%w: origin_sequence and epoch must be at least 1", ErrReceiptMismatch)
	}
	if r.Device == r.OriginDevice {
		return fmt.Errorf("%w: a device cannot acknowledge its own package", ErrReceiptMismatch)
	}
	if err := validateReceiptRecovery(r); err != nil {
		return err
	}
	if r.AppliedAt.IsZero() {
		return fmt.Errorf("%w: applied_at is unset", ErrReceiptMismatch)
	}
	return nil
}

func validateReceiptIdentities(r *Receipt) error {
	for _, field := range []struct{ name, prefix, value string }{
		{"vault_id", vaultPrefix, r.VaultID},
		{"package_id", packagePrefix, r.PackageID},
		{"origin_device", devicePrefix, r.OriginDevice},
		{"device", devicePrefix, r.Device},
	} {
		if err := checkIdentity(ErrReceiptMismatch, field.name, field.prefix, field.value); err != nil {
			return err
		}
	}
	return nil
}

func validateReceiptRecovery(r *Receipt) error {
	switch {
	case r.Converged && strings.TrimSpace(r.RecoveryEntry) != "":
		return fmt.Errorf("%w: converged package %s names recovery entry %s",
			ErrReceiptMismatch, r.PackageID, r.RecoveryEntry)
	case !r.Converged && strings.TrimSpace(r.RecoveryEntry) == "":
		return fmt.Errorf("%w: package %s", ErrNoRecoveryEntry, r.PackageID)
	}
	return nil
}

// VerifyAgainst is the producer's check when it reads a receipt the peer left
// in its channel: does this answer the package I published, from the device I
// enrolled, under the epoch I am living in.
//
// A receipt that fails here is not evidence of anything and the producer keeps
// its copy of the change. That is the safe direction by construction: the cost
// of an unrecognized receipt is a redundant re-send, and the cost of an
// accepted wrong one is a change nobody has.
func (r *Receipt) VerifyAgainst(m *Manifest, origin *Enrollment) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if origin == nil {
		return fmt.Errorf("%w: no local enrollment", ErrNotEnrolled)
	}
	if err := origin.Validate(); err != nil {
		return err
	}
	if r.VaultID != origin.VaultID {
		return fmt.Errorf("%w: receipt names vault %q, this vault is %q", ErrWrongVault, r.VaultID, origin.VaultID)
	}
	if r.OriginDevice != origin.DeviceID {
		return fmt.Errorf("%w: receipt answers a package from %q, not from this device", ErrReceiptMismatch, r.OriginDevice)
	}
	if _, enrolled := origin.Peer(r.Device); !enrolled {
		return fmt.Errorf("%w: %q", ErrUnknownPeer, r.Device)
	}
	if err := receiptEpoch(r, origin); err != nil {
		return err
	}
	return r.matchesManifest(m)
}

func receiptEpoch(r *Receipt, origin *Enrollment) error {
	switch {
	case r.Epoch < origin.Epoch:
		return fmt.Errorf("%w: receipt epoch %d, this vault is at %d", ErrStaleEpoch, r.Epoch, origin.Epoch)
	case r.Epoch > origin.Epoch:
		return fmt.Errorf("%w: receipt epoch %d, this vault is at %d", ErrEpochAhead, r.Epoch, origin.Epoch)
	default:
		return nil
	}
}

// matchesManifest is the last of VerifyAgainst's checks: the receipt answers
// *this* package, and it answers all of it.
//
// A nil manifest is a refusal rather than a skipped check. The producer calls
// VerifyAgainst to decide whether it may drop its own copy of a change, and the
// manifest is the only thing in the call that says which change that is. Reading
// nil as "nothing to compare against, so it matches" would turn a caller's
// missing lookup into a receipt that verifies for any package at all.
//
// The coverage check is here rather than only in Acknowledge for the same
// reason. Acknowledge runs coversPackage on the applying device, against an
// Application that device had in memory — which makes a receipt this build mints
// honest, and says nothing about the receipt the producer is holding. That one
// arrived as a file, over a transport, possibly from another build. A receipt
// naming the right package with an empty or partial applied list would otherwise
// pass every identity check here and be read as "all of it landed", which is the
// one mistake this whole file exists to prevent: the producer drops its only
// copy of a change neither replica has.
func (r *Receipt) matchesManifest(m *Manifest) error {
	if m == nil {
		return fmt.Errorf("%w: no package for receipt %s to answer", ErrReceiptMismatch, r.PackageID)
	}
	if r.PackageID != m.PackageID {
		return fmt.Errorf("%w: receipt answers %s, not %s", ErrReceiptMismatch, r.PackageID, m.PackageID)
	}
	if r.OriginSequence != m.Sequence {
		return fmt.Errorf("%w: receipt says sequence %d, package is %d", ErrReceiptMismatch, r.OriginSequence, m.Sequence)
	}
	if r.VaultID != m.VaultID {
		return fmt.Errorf("%w: receipt names vault %q, package names %q", ErrWrongVault, r.VaultID, m.VaultID)
	}
	return coversPackage(m, r.Applied)
}

// PublishReceipt writes a receipt into the acknowledging device's own outgoing
// channel, durably and immutably, and returns the path it was published at.
//
// A receipt goes out through the same channel packages do, because the same
// transport is the only thing that connects the two devices. It is immutable
// for the same reason a package is: the peer may already have acted on it by
// discarding its copy of the change, and a second version under one package
// identity would leave the two devices disagreeing about what was applied.
func PublishReceipt(channelDir string, r *Receipt) (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	path := receiptPath(channelDir, r.PackageID)
	if err := vault.GuardMutation("", path); err != nil {
		return "", err
	}
	if err := ensureChannel(channelDir); err != nil {
		return "", err
	}
	data, err := encodeRecord(r)
	if err != nil {
		return "", err
	}
	if err := publishOnce(path, data); err != nil {
		if errors.Is(err, ErrPackageExists) {
			return "", fmt.Errorf("%w: receipt for %s", ErrPackageExists, r.PackageID)
		}
		return "", err
	}
	// Past the commit point — the receipt is linked and the producer may already
	// be reading it — so a durability failure is reported with the path, the
	// same way Publish reports one. Unlinking a receipt somebody may have acted
	// on is worse than an un-fsynced directory entry.
	receiptsDir := filepath.Dir(path)
	if err := vault.SyncDir(receiptsDir); err != nil {
		return path, fmt.Errorf("vaultsync: sync receipts directory %q: %w", receiptsDir, err)
	}
	// And then the channel that names it, for the reason syncPackage fsyncs two
	// levels: a name is durable only once the directory holding it is.
	if err := vault.SyncDir(channelDir); err != nil {
		return path, fmt.Errorf("vaultsync: sync channel %q: %w", channelDir, err)
	}
	return path, nil
}

// ReadReceipt reads the receipt for packageID from channelDir. A receipt that
// has not arrived is reported as ErrIncompletePackage, the same answer a
// package still in transit gets, because the remedy is the same: wait.
//
// The receipt's own package_id is compared against the one asked for, rather
// than assumed from the file name. The name is how the receipt was found; the
// field is what it claims to acknowledge, and a caller that asked about one
// package must not be handed a statement about another because a file landed
// under the wrong name.
func ReadReceipt(channelDir, packageID string) (*Receipt, error) {
	// Before the identity is joined onto a directory, not after: receiptPath's
	// containment rests on the name being a checked identity, and packageID here
	// is an ordinary argument from an exported call rather than a directory entry
	// this package listed itself.
	if err := checkIdentity(ErrReceiptMismatch, "package_id", packagePrefix, packageID); err != nil {
		return nil, err
	}
	path := receiptPath(channelDir, packageID)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: no receipt for %s", ErrIncompletePackage, packageID)
		}
		return nil, fmt.Errorf("vaultsync: read receipt %q: %w", path, err)
	}
	if err := checkRecordFormat(data, path, ErrReceiptMismatch); err != nil {
		return nil, err
	}
	var receipt Receipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return nil, fmt.Errorf("%w: parse %q: %v", ErrReceiptMismatch, path, err)
	}
	if err := receipt.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if receipt.PackageID != packageID {
		return nil, fmt.Errorf("%w: %s holds a receipt for %s", ErrReceiptMismatch, path, receipt.PackageID)
	}
	return &receipt, nil
}

// ReceiptPath reports where the receipt for packageID lives in channelDir,
// without reading it or requiring it to exist.
//
// It is for a caller that already knows a receipt was published and needs to say
// where: an applier answering for a package it applied on an earlier run, whose
// receipt is already in the channel and whose PublishReceipt would therefore
// refuse with ErrPackageExists rather than hand a path back. Asking here keeps
// the layout one package's business, which is the same reason ReceiptsDir exists.
//
// The identity is checked before it is joined onto a directory, for the reason
// ReadReceipt states: containment rests on the name being a checked identity.
func ReceiptPath(channelDir, packageID string) (string, error) {
	if err := checkIdentity(ErrReceiptMismatch, "package_id", packagePrefix, packageID); err != nil {
		return "", err
	}
	return receiptPath(channelDir, packageID), nil
}

// receiptPath is the file one package's receipt is published at. The package
// identity is validated before it is ever joined onto a directory, so the name
// cannot be a path.
func receiptPath(channelDir, packageID string) string {
	return filepath.Join(ReceiptsDir(channelDir), packageID+receiptSuffix)
}
