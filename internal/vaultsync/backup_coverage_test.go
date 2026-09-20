package vaultsync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBackupCoverageRequiresTheExactManifest(t *testing.T) {
	root := t.TempDir()
	packageID, err := NewPackageID()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(OutgoingDir(root), packageID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := []byte("{\"package_id\":\"" + packageID + "\"}\n")
	if err := os.WriteFile(filepath.Join(dir, ManifestName), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	coverage := BackupCoverage{
		Format:     BackupCoverageFormat,
		BackupID:   "private-20260919T120000Z",
		VerifiedAt: time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC),
		Packages: []BackupCoveredPackage{{
			PackageID:      packageID,
			ManifestSHA256: HashBytes(manifest),
		}},
	}
	if covered, err := coverage.Covers(dir, &Manifest{PackageID: packageID}); err != nil || !covered {
		t.Fatalf("exact manifest covered=%v err=%v", covered, err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), append(manifest, ' '), 0o644); err != nil {
		t.Fatal(err)
	}
	if covered, err := coverage.Covers(dir, &Manifest{PackageID: packageID}); err != nil || covered {
		t.Fatalf("changed manifest covered=%v err=%v", covered, err)
	}
}

func TestLoadBackupCoverageFailsClosedAndTreatsAbsenceAsNoCoverage(t *testing.T) {
	root := t.TempDir()
	coverage, err := LoadBackupCoverage(root)
	if err != nil || coverage.Format != BackupCoverageFormat || len(coverage.Packages) != 0 {
		t.Fatalf("missing coverage = %+v, %v", coverage, err)
	}
	if err := os.MkdirAll(filepath.Dir(BackupCoveragePath(root)), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := BackupCoverage{Format: BackupCoverageFormat, BackupID: "backup", VerifiedAt: time.Now(),
		Packages: []BackupCoveredPackage{{PackageID: "../escape", ManifestSHA256: "00"}}}
	data, err := json.Marshal(bad)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(BackupCoveragePath(root), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBackupCoverage(root); err == nil {
		t.Fatal("invalid backup coverage loaded")
	}
}
