package vaultimport

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"syscall"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// TestRecoveryQualificationDiskExhaustionBeforeApplication covers capacity
// loss at the first durable state boundary. No vault content or acknowledgment
// may appear when the attempt itself cannot be recorded.
func TestRecoveryQualificationDiskExhaustionBeforeApplication(t *testing.T) {
	r := enroll(t)
	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Capacity.md", []byte("must not land\n"), vaultsync.Absent())
	})
	writer := exhaustImportStateWrite(func(state *State) bool {
		return state.Pending != nil
	})

	result, err := applyWithStateWriter(r, dir, writer)
	if !errors.Is(err, syscall.ENOSPC) || result != nil {
		t.Fatalf("Apply = (%+v, %v), want nil result with ENOSPC", result, err)
	}
	if exists(t, r.pair.Local, "Notes/Capacity.md") {
		t.Fatal("capacity failure before declaration still changed vault content")
	}
	if receipt, err := vaultsync.ReadReceipt(vaultsync.OutgoingDir(r.root()), manifest.PackageID); err == nil || receipt != nil {
		t.Fatalf("capacity failure before application published receipt %+v with error %v", receipt, err)
	}
	if entries := r.entries(t); len(entries) != 0 {
		t.Fatalf("capacity failure before application left %d recovery entries", len(entries))
	}
	requireApplicationAfterCapacityReturns(t, r, manifest, dir)
}

func requireApplicationAfterCapacityReturns(
	t *testing.T,
	r *replicas,
	manifest *vaultsync.Manifest,
	dir string,
) {
	t.Helper()
	result, err := r.apply(t, dir)
	if err != nil {
		t.Fatalf("Apply after capacity returned: %v", err)
	}
	if result == nil || result.Receipt == nil || result.PackageID != manifest.PackageID {
		t.Fatalf("Apply after capacity returned = %+v, want acknowledged %s", result, manifest.PackageID)
	}
	if got := r.pair.Local.Read(t, "Notes/Capacity.md"); got != "must not land\n" {
		t.Fatalf("content after capacity returned = %q", got)
	}
}

// TestRecoveryQualificationDiskExhaustionAfterAcknowledgment covers the other
// import-state boundary: the peer has received a durable receipt, but the
// consumer cannot record that fact in its own state. A restart must
// adopt the receipt, finish the pending application, and never apply the package
// twice. The injected syscall error is the exact error a full filesystem
// returns; only the filesystem boundary is replaced.
func TestRecoveryQualificationDiskExhaustionAfterAcknowledgment(t *testing.T) {
	r := enroll(t)
	manifest, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Capacity.md", []byte("survives a full disk\n"), vaultsync.Absent())
	})
	writer := exhaustImportStateWrite(func(state *State) bool {
		return state.Pending == nil && len(state.Applied) > 0
	})

	result, err := applyWithStateWriter(r, dir, writer)
	requireExhaustedApplication(t, r, manifest, result, err)
	requireCapacityRecovery(t, r, manifest, dir)
}

func exhaustImportStateWrite(
	fail func(*State) bool,
) func(string, []byte, fs.FileMode) error {
	return func(path string, data []byte, perm fs.FileMode) error {
		var state State
		if err := json.Unmarshal(data, &state); err != nil {
			return fmt.Errorf("decode import state at capacity seam: %w", err)
		}
		if fail(&state) {
			return syscall.ENOSPC
		}
		return vault.WriteStateFile(path, data, perm)
	}
}

func applyWithStateWriter(
	r *replicas,
	dir string,
	write func(string, []byte, fs.FileMode) error,
) (*Result, error) {
	return Apply(r.root(), dir, Options{
		Command:    "bn sync apply --capacity-qualification",
		Now:        r.clock.Now,
		writeState: write,
	})
}

func requireExhaustedApplication(
	t *testing.T,
	r *replicas,
	manifest *vaultsync.Manifest,
	result *Result,
	err error,
) {
	t.Helper()
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("Apply error = %v, want ENOSPC", err)
	}
	if result == nil || result.Receipt == nil {
		t.Fatalf("Apply result = %+v, want the durable receipt published before ENOSPC", result)
	}
	if got := r.pair.Local.Read(t, "Notes/Capacity.md"); got != "survives a full disk\n" {
		t.Fatalf("applied content = %q, want the packaged branch", got)
	}
	requirePublishedReceipt(t, r, manifest, result)
	state, err := LoadState(r.root())
	if err != nil {
		t.Fatalf("LoadState after ENOSPC: %v", err)
	}
	if state.Pending == nil || state.Pending.Manifest.PackageID != manifest.PackageID {
		t.Fatalf("pending attempt = %+v, want %s", state.Pending, manifest.PackageID)
	}
	if _, recorded := state.Applied[manifest.PackageID]; recorded {
		t.Fatalf("the failed state write recorded %s as complete", manifest.PackageID)
	}
}

func requirePublishedReceipt(t *testing.T, r *replicas, manifest *vaultsync.Manifest, result *Result) {
	t.Helper()
	if err := result.Receipt.VerifyAgainst(manifest, r.peer); err != nil {
		t.Fatalf("published receipt is not valid evidence: %v", err)
	}
	published, err := vaultsync.ReadReceipt(vaultsync.OutgoingDir(r.root()), manifest.PackageID)
	if err != nil {
		t.Fatalf("read published receipt after ENOSPC: %v", err)
	}
	if err := published.VerifyAgainst(manifest, r.peer); err != nil {
		t.Fatalf("on-disk receipt is not valid evidence: %v", err)
	}
	if result.ReceiptPath == "" {
		t.Fatal("acknowledged application reported no published receipt path")
	}
}

func requireCapacityRecovery(t *testing.T, r *replicas, manifest *vaultsync.Manifest, dir string) {
	t.Helper()
	failed, err := Recover(r.root(), Options{
		Command: "bn sync recover --capacity-qualification",
		Now:     r.clock.Now,
		writeState: exhaustImportStateWrite(func(state *State) bool {
			return state.Pending == nil && len(state.Applied) > 0
		}),
	})
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("Recover while capacity remains exhausted = (%+v, %v), want ENOSPC", failed, err)
	}
	state, loadErr := LoadState(r.root())
	if loadErr != nil {
		t.Fatalf("LoadState after recovery ENOSPC: %v", loadErr)
	}
	if state.Pending == nil || state.Pending.Manifest.PackageID != manifest.PackageID {
		t.Fatalf("recovery ENOSPC lost pending attempt: %+v", state.Pending)
	}
	if _, err := Recover(r.root(), Options{Command: "bn sync recover --qualification", Now: r.clock.Now}); err != nil {
		t.Fatalf("Recover after capacity returned: %v", err)
	}
	requireRecoveredImportState(t, r, manifest)
	requireDuplicateRedelivery(t, r, dir)
}

func requireRecoveredImportState(t *testing.T, r *replicas, manifest *vaultsync.Manifest) {
	t.Helper()
	state, err := LoadState(r.root())
	if err != nil {
		t.Fatalf("LoadState after recovery: %v", err)
	}
	if state.Pending != nil {
		t.Fatalf("recovery left a pending attempt: %+v", state.Pending)
	}
	record, recorded := state.Applied[manifest.PackageID]
	if !recorded || record.Receipt == nil {
		t.Fatalf("recovery recorded %+v, want the acknowledged application", record)
	}
}

func requireDuplicateRedelivery(t *testing.T, r *replicas, dir string) {
	t.Helper()
	before := commitCount(t, r.pair.Local)
	redelivery, err := r.apply(t, dir)
	if err != nil {
		t.Fatalf("redelivery after recovery: %v", err)
	}
	if redelivery == nil || !redelivery.Duplicate || redelivery.Receipt == nil {
		t.Fatalf("redelivery = %+v, want the recorded receipt without another application", redelivery)
	}
	if after := commitCount(t, r.pair.Local); after != before {
		t.Fatalf("redelivery added a commit after recovery: %d -> %d", before, after)
	}
}
