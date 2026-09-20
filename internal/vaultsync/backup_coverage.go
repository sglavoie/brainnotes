package vaultsync

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"
)

// BackupCoverageFormat is the deployment backup's retention-evidence schema.
//
// The private backup writer publishes this record only after it has copied and
// verified the whole live installation. Retirement treats the record as an
// additional necessary condition, never as a replacement for a peer receipt.
const BackupCoverageFormat = "brainnotes-backup-coverage-v1"

const backupCoverageName = "backup-coverage.json"

// BackupCoverage records the exact immutable package manifests present in one
// verified private backup. It deliberately names manifest digests rather than
// only package identities: a damaged or replaced package must not inherit the
// evidence for bytes that used to occupy its directory.
type BackupCoverage struct {
	Format     string                 `json:"format"`
	BackupID   string                 `json:"backup_id"`
	VerifiedAt time.Time              `json:"verified_at"`
	Packages   []BackupCoveredPackage `json:"packages"`
}

// BackupCoveredPackage is one outgoing package captured by the verified backup.
type BackupCoveredPackage struct {
	PackageID      string `json:"package_id"`
	ManifestSHA256 string `json:"manifest_sha256"`
}

// BackupCoveragePath returns the machine-local record used by retirement.
func BackupCoveragePath(root string) string {
	return filepath.Join(root, filepath.FromSlash(path.Join(StateDir(), backupCoverageName)))
}

// LoadBackupCoverage reads and validates the most recently published coverage
// record. A machine with no verified backup has an empty record and no error;
// malformed evidence fails closed so the companion reports the problem rather
// than silently retaining forever under a record nobody can use.
func LoadBackupCoverage(root string) (*BackupCoverage, error) {
	fresh := &BackupCoverage{Format: BackupCoverageFormat}
	data, err := os.ReadFile(BackupCoveragePath(root))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fresh, nil
		}
		return nil, fmt.Errorf("vaultsync: read backup coverage: %w", err)
	}
	var coverage BackupCoverage
	if err := json.Unmarshal(data, &coverage); err != nil {
		return nil, fmt.Errorf("vaultsync: parse backup coverage: %w", err)
	}
	if err := coverage.Validate(); err != nil {
		return nil, err
	}
	return &coverage, nil
}

// Validate checks that every coverage claim is canonical and unambiguous.
func (c *BackupCoverage) Validate() error {
	if c == nil || c.Format != BackupCoverageFormat {
		return fmt.Errorf("vaultsync: unsupported backup coverage format")
	}
	if c.BackupID == "" || c.VerifiedAt.IsZero() {
		return fmt.Errorf("vaultsync: incomplete backup coverage record")
	}
	seen := make(map[string]bool, len(c.Packages))
	for _, pkg := range c.Packages {
		if !PackageName(pkg.PackageID) || !lowerHexDigest(pkg.ManifestSHA256) || seen[pkg.PackageID] {
			return fmt.Errorf("vaultsync: invalid backup coverage package")
		}
		seen[pkg.PackageID] = true
	}
	return nil
}

// Covers reports whether this verified backup contains the exact manifest now
// present in packageDir.
func (c *BackupCoverage) Covers(packageDir string, manifest *Manifest) (bool, error) {
	if c == nil || manifest == nil {
		return false, nil
	}
	data, err := os.ReadFile(filepath.Join(packageDir, ManifestName))
	if err != nil {
		return false, fmt.Errorf("vaultsync: read covered manifest: %w", err)
	}
	digest := HashBytes(data)
	for _, pkg := range c.Packages {
		if pkg.PackageID == manifest.PackageID && pkg.ManifestSHA256 == digest {
			return true, nil
		}
	}
	return false, nil
}

func lowerHexDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}
