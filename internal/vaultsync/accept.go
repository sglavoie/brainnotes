package vaultsync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Accept reads the package published at dir and reports whether this vault may
// apply it.
//
// The three checks run in a fixed order, and the order is the answer to "what
// is this package": a manifest this build cannot parse is not a package at all,
// a package addressed elsewhere is not this vault's to judge further, and only
// then is it worth hashing megabytes of payload.
//
// A successful Accept says the package is well-formed, addressed here, current,
// and completely delivered. It says nothing about whether the changes apply
// cleanly — every expected content hash in it is still a claim about a vault
// state the applier has to check for itself.
func Accept(local *Enrollment, dir string) (*Manifest, error) {
	manifest, err := ReadManifest(dir)
	if err != nil {
		return nil, err
	}
	if err := manifest.CheckEnrollment(local); err != nil {
		return nil, err
	}
	if err := VerifyPayloads(dir, manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

// ReadManifest reads and validates the manifest of the package at dir.
//
// A package directory with no manifest is incomplete rather than malformed: it
// is what a package still being delivered looks like, and the remedy is to wait
// rather than to quarantine.
func ReadManifest(dir string) (*Manifest, error) {
	path := filepath.Join(dir, ManifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s has no %s yet", ErrIncompletePackage, dir, ManifestName)
		}
		return nil, fmt.Errorf("vaultsync: read manifest %q: %w", path, err)
	}
	if err := checkRecordFormat(data, path, ErrMalformedPackage); err != nil {
		return nil, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("%w: parse %q: %v", ErrMalformedPackage, path, err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &manifest, nil
}

// recordFormat is the only field a record of *any* version of this protocol is
// promised to carry under this name and this type.
type recordFormat struct {
	Format string `json:"format"`
}

// checkRecordFormat reads the format token out of a record's bytes before
// anything attempts to decode the rest of them. Both records this package reads
// back off a disk — the manifest and the receipt — go through it, differing only
// in which sentinel they call unreadable bytes.
//
// Validate checks the token first and alone for exactly this reason, but by the
// time it runs, encoding/json has already had to fit the whole document into
// this build's structs. A later format is free to reuse a field name with a
// different type — `epoch` as a string, `operations` as an object — and the
// decode would fail on that before the token was ever compared. The receiver
// would report ErrMalformedPackage: this is not a package, stop waiting. For a
// package whose only problem is being newer than this build, that is the wrong
// answer and an unrecoverable one. Refusing on the token first makes it
// ErrUnsupportedFormat instead, and the package stays in the channel for a build
// that understands it.
func checkRecordFormat(data []byte, path string, malformed error) error {
	var probe recordFormat
	if err := json.Unmarshal(data, &probe); err != nil {
		return fmt.Errorf("%w: parse %q: %v", malformed, path, err)
	}
	if probe.Format != Format {
		return fmt.Errorf("%w: %s got %q want %q", ErrUnsupportedFormat, path, probe.Format, Format)
	}
	return nil
}

// CheckEnrollment reports whether local may accept this package.
//
// It answers three independent questions, and each has its own remedy. A
// package for another vault is somebody else's and is left alone. A package
// from a device this vault did not enroll — including one claiming to be from
// this vault itself, which means a transport is delivering a channel back into
// its own owner — is refused. A package from another epoch is refused in both
// directions: older means it describes a history this vault has replaced, newer
// means this vault has not learned of a re-enrollment yet and is not entitled
// to an opinion about what the package assumes.
func (m *Manifest) CheckEnrollment(local *Enrollment) error {
	if local == nil {
		return fmt.Errorf("%w: no local enrollment", ErrNotEnrolled)
	}
	if err := local.Validate(); err != nil {
		return err
	}
	if m.VaultID != local.VaultID {
		return fmt.Errorf("%w: package names %q, this vault is %q", ErrWrongVault, m.VaultID, local.VaultID)
	}
	if m.OriginDevice == local.DeviceID {
		return fmt.Errorf("%w: package claims to originate from this device %q", ErrUnknownPeer, local.DeviceID)
	}
	if _, enrolled := local.Peer(m.OriginDevice); !enrolled {
		return fmt.Errorf("%w: %q", ErrUnknownPeer, m.OriginDevice)
	}
	switch {
	case m.Epoch < local.Epoch:
		return fmt.Errorf("%w: package epoch %d, this vault is at %d", ErrStaleEpoch, m.Epoch, local.Epoch)
	case m.Epoch > local.Epoch:
		return fmt.Errorf("%w: package epoch %d, this vault is at %d", ErrEpochAhead, m.Epoch, local.Epoch)
	}
	return nil
}

// VerifyPayloads reports whether the package at dir carries exactly the bytes
// its inventory lists.
//
// Every failure here is ErrIncompletePackage, including an entry the inventory
// does not list. That is deliberate: a file transfer in progress leaves both
// shapes behind — a payload not yet copied, and the transport's own temporary
// file beside the ones that were — and both mean the same thing to a receiver.
// Wait. A package whose bytes are wrong rather than absent is indistinguishable
// from one whose bytes have not all arrived, and treating it as corruption
// would quarantine packages that were merely early.
func VerifyPayloads(dir string, m *Manifest) error {
	payloadDir := filepath.Join(dir, PayloadsName)
	for _, payload := range m.Payloads {
		if err := verifyPayloadFile(payloadDir, payload); err != nil {
			return err
		}
	}
	return verifyNoExtraPayloads(payloadDir, m)
}

// statPayloadFile inspects one inventory entry on disk and returns the path to
// read it from, refusing anything that is not a regular file of exactly the
// declared size.
//
// Lstat, and a regular-file requirement, because a symlink in a package
// directory is a path chosen by whoever produced the package and resolved on
// whoever consumes it — and because a FIFO in that position would hang the
// reader rather than fail it. The size check belongs here rather than beside
// each caller's own hashing: it is what stops a reader from committing memory or
// I/O proportional to bytes the inventory never declared. Every path that opens
// a payload goes through this, so no caller can be the one that forgot.
func statPayloadFile(payloadDir string, payload Payload) (string, error) {
	path := filepath.Join(payloadDir, payload.Name)
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: payload %q has not arrived", ErrIncompletePackage, payload.Name)
		}
		return "", fmt.Errorf("vaultsync: inspect payload %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: payload %q is not a regular file", ErrIncompletePackage, payload.Name)
	}
	if info.Size() != payload.Size {
		return "", fmt.Errorf("%w: payload %q is %d bytes, inventory says %d", ErrIncompletePackage, payload.Name, info.Size(), payload.Size)
	}
	return path, nil
}

func verifyPayloadFile(payloadDir string, payload Payload) error {
	path, err := statPayloadFile(payloadDir, payload)
	if err != nil {
		return err
	}
	digest, err := hashFile(path)
	if err != nil {
		return err
	}
	if digest != payload.SHA256 {
		return fmt.Errorf("%w: payload %q hashes to %s, inventory says %s", ErrIncompletePackage, payload.Name, digest, payload.SHA256)
	}
	return nil
}

func verifyNoExtraPayloads(payloadDir string, m *Manifest) error {
	entries, err := os.ReadDir(payloadDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// An inventory-less package is legal: a package of nothing but
			// tombstones carries no bytes and needs no directory.
			if len(m.Payloads) == 0 {
				return nil
			}
			return fmt.Errorf("%w: %s has no %s directory", ErrIncompletePackage, m.PackageID, PayloadsName)
		}
		return fmt.Errorf("vaultsync: read payload directory %q: %w", payloadDir, err)
	}
	listed := m.payloadIndex()
	for _, entry := range entries {
		if _, ok := listed[entry.Name()]; !ok {
			return fmt.Errorf("%w: %s carries %q, which its inventory does not list", ErrIncompletePackage, m.PackageID, entry.Name())
		}
	}
	return nil
}

// ReadPayload returns the bytes of one inventory entry of the package at dir,
// re-verifying the digest rather than trusting a VerifyPayloads that ran
// earlier. The transport is still running while a package is being applied, so
// "checked a moment ago" is not "checked".
//
// That re-check is also why the file is stat'ed before it is read rather than
// after: os.ReadFile sizes its buffer from the stat it does itself and follows
// symlinks, so a payload that grew, or became a link to a device or a FIFO,
// after VerifyPayloads passed would be read in full — or block forever — and only
// then be judged.
func ReadPayload(dir string, m *Manifest, name string) ([]byte, error) {
	payload, listed := m.payloadIndex()[name]
	if !listed {
		return nil, fmt.Errorf("%w: %s does not list payload %q", ErrMalformedPackage, m.PackageID, name)
	}
	path, err := statPayloadFile(filepath.Join(dir, PayloadsName), payload)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: payload %q has not arrived", ErrIncompletePackage, payload.Name)
		}
		return nil, fmt.Errorf("vaultsync: read payload %q: %w", path, err)
	}
	if int64(len(data)) != payload.Size || HashBytes(data) != payload.SHA256 {
		return nil, fmt.Errorf("%w: payload %q does not match its inventory entry", ErrIncompletePackage, payload.Name)
	}
	return data, nil
}

// HashBytes returns the hex SHA-256 of data, which is the digest every hash in
// this protocol is.
func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("vaultsync: open payload %q: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", fmt.Errorf("vaultsync: hash payload %q: %w", path, err)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
