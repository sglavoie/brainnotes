package vaultrecovery

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultimport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// TestReenrollmentRefusesWhileAConflictIsHeld covers the survivor holding a
// package on a conflict when the review starts. Acceptance archives the import
// record and starts an empty one, so a hold accepted across it would stop being
// anything this vault acts on — the contested versions it names would sit in the
// archive and the package would never be decided. The refusal comes at both
// doors: the inventory, so the operator learns before carrying it to the peer,
// and acceptance, before the survivor quarantines itself with a marker for a
// decision it cannot take.
func TestReenrollmentRefusesWhileAConflictIsHeld(t *testing.T) {
	r := newReviewPair(t)
	local := r.prepare(t, r.pair.Local.Root)
	survivor := mustInventory(t, r.pair.Peer.Root)
	_, peerPlan := reviewPlans(t, r, local, survivor)
	holdConflict(t, r.pair.Peer.Root, r.peer)

	if _, err := BuildInventory(r.pair.Peer.Root); !errors.Is(err, vaultimport.ErrUnresolvedImport) {
		t.Fatalf("inventory over a held conflict: got %v, want ErrUnresolvedImport", err)
	}
	if err := ApplyPlan(r.pair.Peer.Root, peerPlan, r.clock.Tick); !errors.Is(err, vaultimport.ErrUnresolvedImport) {
		t.Fatalf("acceptance over a held conflict: got %v, want ErrUnresolvedImport", err)
	}
	if review, err := vaultsync.LoadRestoreReview(r.pair.Peer.Root); err != nil || review != nil {
		t.Fatalf("a refused survivor was quarantined anyway: %+v, %v", review, err)
	}
	if enrollment := mustEnrollment(t, r.pair.Peer.Root); enrollment.Epoch == peerPlan.TargetEpoch {
		t.Fatal("a refused survivor was re-enrolled")
	}
}

// holdConflict writes an import record holding one package, the way a drain
// that met a contested note leaves it.
func holdConflict(t *testing.T, root string, local *vaultsync.Enrollment) {
	t.Helper()
	packageID, err := vaultsync.NewPackageID()
	if err != nil {
		t.Fatal(err)
	}
	record := map[string]any{
		"version":   3,
		"vault_id":  local.VaultID,
		"device_id": local.DeviceID,
		"applied":   map[string]any{},
		"conflicts": map[string]any{
			packageID: map[string]any{
				"package_id": packageID,
				"paths":      []map[string]any{{"path": "Alpha.md"}},
			},
		},
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vaultimport.StatePath(root), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if state, err := vaultimport.LoadState(root); err != nil || len(state.Conflicts) != 1 {
		t.Fatalf("the hand-written hold does not load as one: %+v, %v", state, err)
	}
}
