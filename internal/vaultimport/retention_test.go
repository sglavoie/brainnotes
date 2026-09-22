package vaultimport

import (
	"errors"
	"os"
	"reflect"
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

func TestDrainSurvivesUnsupportedRetirementRecord(t *testing.T) {
	requireDrainSurvivesRetirementProblem(t, vaultsync.ErrUnsupportedFormat, func(t *testing.T, r *replicas, _ uint64) {
		t.Helper()
		path := vaultsync.RetirementPath(vaultsync.IncomingDir(r.root()))
		if err := os.WriteFile(path, []byte(`{"format":"brainnotes-sync-v2"}`), 0o644); err != nil {
			t.Fatalf("write unsupported retirement record: %v", err)
		}
	})
}

func TestDrainSurvivesUnverifiableRetirementRecord(t *testing.T) {
	requireDrainSurvivesRetirementProblem(t, vaultsync.ErrUnknownPeer, func(t *testing.T, r *replicas, through uint64) {
		t.Helper()
		_, err := vaultsync.PublishRetirement(vaultsync.IncomingDir(r.root()), &vaultsync.Retirement{
			Format:       vaultsync.Format,
			VaultID:      r.local.VaultID,
			OriginDevice: r.local.DeviceID,
			Epoch:        r.local.Epoch,
			Through:      through,
		})
		if err != nil {
			t.Fatalf("publish unverifiable retirement record: %v", err)
		}
	})
}

func requireDrainSurvivesRetirementProblem(
	t *testing.T,
	wantProblem error,
	plant func(*testing.T, *replicas, uint64),
) {
	t.Helper()
	r := enroll(t)
	retired, retiredDir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/AlreadyApplied.md", []byte("already applied\n"), vaultsync.Absent())
	})
	r.mustApply(t, retiredDir)
	if err := os.RemoveAll(retiredDir); err != nil {
		t.Fatalf("remove already-applied package: %v", err)
	}
	before, err := LoadState(r.root())
	if err != nil {
		t.Fatalf("LoadState before drain: %v", err)
	}
	prior, found := before.Applied[retired.PackageID]
	if !found {
		t.Fatalf("applied ledger lost prior record for %s before the retirement test", retired.PackageID)
	}
	plant(t, r, retired.Sequence)

	arriving, _ := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Arriving.md", []byte("arrived\n"), vaultsync.Absent())
	})
	drained, err := Drain(r.root(), Options{Now: r.clock.Now})
	if err != nil {
		t.Fatalf("Drain with unusable retirement record: %v", err)
	}
	if len(drained.Applied) != 1 || drained.Applied[0].PackageID != arriving.PackageID {
		t.Fatalf("applied packages = %v, want only %s", appliedIDs(drained), arriving.PackageID)
	}
	if len(drained.Held) != 0 {
		t.Fatalf("held packages = %v, want none", heldIDs(drained))
	}
	if !errors.Is(drained.Cleanup, wantProblem) {
		t.Fatalf("cleanup problem = %v, want it to carry %v", drained.Cleanup, wantProblem)
	}
	if got := r.pair.Local.Read(t, "Notes/Arriving.md"); got != "arrived\n" {
		t.Fatalf("arriving note = %q, want delivered content", got)
	}

	after, err := LoadState(r.root())
	if err != nil {
		t.Fatalf("LoadState after drain: %v", err)
	}
	if len(after.Applied) != len(before.Applied)+1 {
		t.Fatalf("applied ledger has %d records, want %d", len(after.Applied), len(before.Applied)+1)
	}
	if !reflect.DeepEqual(after.Applied[retired.PackageID], prior) {
		t.Fatalf("unusable retirement record changed prior applied evidence for %s", retired.PackageID)
	}
	requireAppliedRecord(t, r, arriving.PackageID, true)
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
