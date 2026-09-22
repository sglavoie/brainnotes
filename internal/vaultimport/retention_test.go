package vaultimport

import (
	"os"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

func TestAppliedLedgerNeedsSenderRetirementBeforePruning(t *testing.T) {
	r := enroll(t)
	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Retired.md", []byte("retired\n"), vaultsync.Absent())
	})
	r.mustApply(t, dir)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove delivered package: %v", err)
	}

	if _, err := Drain(r.root(), Options{Now: r.clock.Now}); err != nil {
		t.Fatalf("Drain without retirement evidence: %v", err)
	}
	requireAppliedRecord(t, r, manifest.PackageID, true)

	publishPeerRetirement(t, r, manifest.Sequence)
	if _, err := Drain(r.root(), Options{Now: r.clock.Now}); err != nil {
		t.Fatalf("Drain with retirement evidence: %v", err)
	}
	requireAppliedRecord(t, r, manifest.PackageID, false)
}

func TestAppliedLedgerWaitsForTheRetiredDeliveryToLeave(t *testing.T) {
	r := enroll(t)
	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Ordered.md", []byte("ordered\n"), vaultsync.Absent())
	})
	r.mustApply(t, dir)
	publishPeerRetirement(t, r, manifest.Sequence)

	if _, err := Drain(r.root(), Options{Now: r.clock.Now}); err != nil {
		t.Fatalf("Drain while package remains: %v", err)
	}
	requireAppliedRecord(t, r, manifest.PackageID, true)

	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove delivered package: %v", err)
	}
	if _, err := Drain(r.root(), Options{Now: r.clock.Now}); err != nil {
		t.Fatalf("Drain after package left: %v", err)
	}
	requireAppliedRecord(t, r, manifest.PackageID, false)
}

func TestAppliedLedgerPrunesPastAnOlderRetainedPackage(t *testing.T) {
	r := enroll(t)
	first, firstDir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/StillInFlight.md", []byte("first\n"), vaultsync.Absent())
	})
	second, secondDir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/RetiredLater.md", []byte("second\n"), vaultsync.Absent())
	})
	r.mustApply(t, firstDir)
	r.mustApply(t, secondDir)
	if err := os.RemoveAll(firstDir); err != nil {
		t.Fatalf("remove first delivery: %v", err)
	}
	if err := os.RemoveAll(secondDir); err != nil {
		t.Fatalf("remove second delivery: %v", err)
	}
	publishPeerRetirement(t, r, second.Sequence, vaultsync.RetainedPackage{
		PackageID: first.PackageID,
		Sequence:  first.Sequence,
	})

	if _, err := Drain(r.root(), Options{Now: r.clock.Now}); err != nil {
		t.Fatalf("Drain with retained exception: %v", err)
	}
	requireAppliedRecord(t, r, first.PackageID, true)
	requireAppliedRecord(t, r, second.PackageID, false)
}

func publishPeerRetirement(t *testing.T, r *replicas, through uint64, retained ...vaultsync.RetainedPackage) {
	t.Helper()
	_, err := vaultsync.PublishRetirement(vaultsync.IncomingDir(r.root()), &vaultsync.Retirement{
		Format:       vaultsync.Format,
		VaultID:      r.peer.VaultID,
		OriginDevice: r.peer.DeviceID,
		Epoch:        r.peer.Epoch,
		Through:      through,
		Retained:     retained,
	})
	if err != nil {
		t.Fatalf("PublishRetirement: %v", err)
	}
}

func requireAppliedRecord(t *testing.T, r *replicas, packageID string, want bool) {
	t.Helper()
	state, err := LoadState(r.root())
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	_, found := state.Applied[packageID]
	if found != want {
		t.Fatalf("applied record for %s present = %t, want %t", packageID, found, want)
	}
}
