package vaultrecovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/checkpoint"
	"github.com/sglavoie/sglavoie_brainnotes/internal/restorestate"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultexport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultfixture"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultimport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

type reviewPair struct {
	pair        *vaultfixture.Pair
	clock       *vaultfixture.Clock
	local, peer *vaultsync.Enrollment
}

func newReviewPair(t *testing.T) *reviewPair {
	t.Helper()
	pair := vaultfixture.NewPair(t, nil)
	clock := vaultfixture.NewClock(t)
	local, err := vaultsync.Establish(pair.Local.Root, vaultsync.Options{Label: "mac", Now: clock.Now})
	if err != nil {
		t.Fatalf("Establish: %v", err)
	}
	clock.Tick()
	peer, err := vaultsync.Join(pair.Peer.Root, local.Invite(), vaultsync.Options{Label: "pi", Now: clock.Now})
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	clock.Tick()
	local, err = vaultsync.RecordPeer(pair.Local.Root, vaultsync.Peer{DeviceID: peer.DeviceID, Label: peer.Label})
	if err != nil {
		t.Fatalf("RecordPeer: %v", err)
	}
	for _, root := range []string{pair.Local.Root, pair.Peer.Root} {
		if _, err := vaultexport.Export(context.Background(), vaultexport.Options{Root: root, Now: clock.Tick}); err != nil {
			t.Fatalf("baseline export for %s: %v", root, err)
		}
	}
	return &reviewPair{pair: pair, clock: clock, local: local, peer: peer}
}

func (r *reviewPair) prepare(t *testing.T, root string) *Inventory {
	t.Helper()
	operationID := strings.Repeat("a", 64)
	first, err := vaultsync.PrepareRestoreReviewContext(context.Background(), root, operationID, r.clock.Tick)
	if err != nil {
		t.Fatalf("PrepareRestoreReviewContext: %v", err)
	}
	again, err := vaultsync.PrepareRestoreReviewContext(context.Background(), root, operationID, r.clock.Tick)
	if err != nil {
		t.Fatalf("PrepareRestoreReviewContext again: %v", err)
	}
	if first.ProposedDeviceID != again.ProposedDeviceID {
		t.Fatal("repeated preparation rotated the proposed identity again")
	}
	inventory, err := BuildInventory(root)
	if err != nil {
		t.Fatalf("BuildInventory: %v", err)
	}
	if !inventory.Restored || inventory.DeviceID == inventory.EffectiveDeviceID {
		t.Fatalf("restored inventory did not rotate identity: %+v", inventory)
	}
	return inventory
}

func TestOneRestoredPeerRequiresMatchingInventoriesAndReenrollsBothSides(t *testing.T) {
	r := newReviewPair(t)
	stale := stalePackage(t, r)
	localBefore := r.prepare(t, r.pair.Local.Root)
	peerBefore := mustInventory(t, r.pair.Peer.Root)
	localPlan, peerPlan := reviewPlans(t, r, localBefore, peerBefore)
	if len(localPlan.Differences) != 0 || len(peerPlan.Differences) != 0 {
		t.Fatalf("identical replicas differed: %+v / %+v", localPlan.Differences, peerPlan.Differences)
	}
	applyPlans(t, r, localPlan, peerPlan)
	localAfter := mustEnrollment(t, r.pair.Local.Root)
	peerAfter := mustEnrollment(t, r.pair.Peer.Root)
	assertReviewedEnrollment(t, r, localBefore, localPlan, localAfter, peerAfter)
	assertStaleCausalStateRejected(t, r, stale, localAfter, peerAfter)
	assertRebaselinedSequences(t, r, peerBefore)
	assertReviewArchives(t, r, localPlan, peerPlan)
}

func assertReviewedEnrollment(t *testing.T, r *reviewPair, before *Inventory, plan *Plan, local, peer *vaultsync.Enrollment) {
	t.Helper()
	if local.DeviceID != before.EffectiveDeviceID || local.DeviceID == r.local.DeviceID {
		t.Fatalf("restored identity = %s, want proposed %s", local.DeviceID, before.EffectiveDeviceID)
	}
	if peer.DeviceID != r.peer.DeviceID {
		t.Fatalf("surviving peer rotated identity from %s to %s", r.peer.DeviceID, peer.DeviceID)
	}
	if local.Epoch != peer.Epoch || local.Epoch != plan.TargetEpoch {
		t.Fatalf("epochs = %d/%d, want %d", local.Epoch, peer.Epoch, plan.TargetEpoch)
	}
	if enrolled, ok := local.Peer(peer.DeviceID); !ok || enrolled.JoinedEpoch != local.Epoch {
		t.Fatalf("restored peer enrollment = %+v, %v", enrolled, ok)
	}
	if enrolled, ok := peer.Peer(local.DeviceID); !ok || enrolled.JoinedEpoch != peer.Epoch {
		t.Fatalf("surviving peer enrollment = %+v, %v", enrolled, ok)
	}
}

func assertStaleCausalStateRejected(t *testing.T, r *reviewPair, stale staleArtifacts, receiver, producer *vaultsync.Enrollment) {
	t.Helper()
	if err := stale.manifest.CheckEnrollment(receiver); !errors.Is(err, vaultsync.ErrStaleEpoch) {
		t.Fatalf("old package after review = %v, want stale epoch", err)
	}
	dir, err := vaultsync.Publish(vaultsync.IncomingDir(r.pair.Local.Root), stale.manifest, stale.payloads)
	if err != nil {
		t.Fatalf("publish stale tombstone: %v", err)
	}
	if _, err := vaultimport.Apply(r.pair.Local.Root, dir, vaultimport.Options{Now: r.clock.Tick}); !errors.Is(err, vaultsync.ErrStaleEpoch) {
		t.Fatalf("import stale tombstone = %v, want stale epoch", err)
	}
	if _, err := os.Stat(filepath.Join(r.pair.Local.Root, "Beta.md")); err != nil {
		t.Fatalf("stale tombstone removed Beta.md: %v", err)
	}
	if err := stale.receipt.VerifyAgainst(stale.manifest, producer); !errors.Is(err, vaultsync.ErrStaleEpoch) && !errors.Is(err, vaultsync.ErrUnknownPeer) {
		t.Fatalf("old receipt after review = %v, want stale epoch or retired origin", err)
	}
}

func assertRebaselinedSequences(t *testing.T, r *reviewPair, peerBefore *Inventory) {
	t.Helper()
	localExport, err := vaultexport.LoadState(r.pair.Local.Root)
	if err != nil {
		t.Fatalf("load local export: %v", err)
	}
	peerExport, err := vaultexport.LoadState(r.pair.Peer.Root)
	if err != nil {
		t.Fatalf("load peer export: %v", err)
	}
	if localExport.Sequence != 0 {
		t.Fatalf("restored sequence = %d, want fresh stream", localExport.Sequence)
	}
	if peerExport.Sequence != peerBefore.ExportSequence {
		t.Fatalf("survivor sequence = %d, want preserved %d", peerExport.Sequence, peerBefore.ExportSequence)
	}
}

func assertReviewArchives(t *testing.T, r *reviewPair, localPlan, peerPlan *Plan) {
	t.Helper()
	for _, root := range []string{r.pair.Local.Root, r.pair.Peer.Root} {
		if review, err := vaultsync.LoadRestoreReview(root); err != nil || review != nil {
			t.Fatalf("restore marker after apply = %+v, %v", review, err)
		}
		archive := filepath.Join(root, filepath.FromSlash(vaultsync.StateDir()), archiveName, localPlan.SHA256)
		if root == r.pair.Peer.Root {
			archive = filepath.Join(root, filepath.FromSlash(vaultsync.StateDir()), archiveName, peerPlan.SHA256)
		}
		if _, err := os.Stat(archive); err != nil {
			t.Fatalf("causal archive missing: %v", err)
		}
	}
}

func TestBothRestoredPeersReceiveFreshIdentitiesAndStreams(t *testing.T) {
	r := newReviewPair(t)
	localBefore := r.prepare(t, r.pair.Local.Root)
	peerBefore := r.prepare(t, r.pair.Peer.Root)
	localPlan, peerPlan := reviewPlans(t, r, localBefore, peerBefore)
	applyPlans(t, r, localPlan, peerPlan)
	local := mustEnrollment(t, r.pair.Local.Root)
	peer := mustEnrollment(t, r.pair.Peer.Root)
	assertBothRestoredEnrollment(t, r, localBefore, peerBefore, local, peer)
	assertFreshProducerStates(t, r)
}

func assertBothRestoredEnrollment(t *testing.T, r *reviewPair, localBefore, peerBefore *Inventory, local, peer *vaultsync.Enrollment) {
	t.Helper()
	if local.DeviceID != localBefore.EffectiveDeviceID || peer.DeviceID != peerBefore.EffectiveDeviceID {
		t.Fatalf("effective identities not installed: %s/%s", local.DeviceID, peer.DeviceID)
	}
	if local.DeviceID == r.local.DeviceID || peer.DeviceID == r.peer.DeviceID || local.Epoch != peer.Epoch {
		t.Fatalf("restored enrollment reused causal identity: %+v / %+v", local, peer)
	}
}

func assertFreshProducerStates(t *testing.T, r *reviewPair) {
	t.Helper()
	for _, root := range []string{r.pair.Local.Root, r.pair.Peer.Root} {
		state, err := vaultexport.LoadState(root)
		if err != nil {
			t.Fatalf("load export state: %v", err)
		}
		if state.Sequence != 0 || state.Commit != "" || state.Baseline != "" {
			t.Fatalf("restored producer did not start a fresh baseline: %+v", state)
		}
	}
}

type staleArtifacts struct {
	manifest *vaultsync.Manifest
	payloads map[string][]byte
	receipt  *vaultsync.Receipt
}

func stalePackage(t *testing.T, r *reviewPair) staleArtifacts {
	t.Helper()
	builder, err := vaultsync.NewBuilder(r.peer, 99, r.clock.Tick())
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	prior := vaultsync.Existing(vaultsync.HashBytes([]byte(vaultfixture.DefaultFiles()["Beta.md"])))
	pkg, payloads, err := builder.BeginOperation("", r.clock.Tick()).Delete("Beta.md", prior).Build()
	if err != nil {
		t.Fatalf("Build stale package: %v", err)
	}
	receipt, err := vaultsync.Acknowledge(r.local, pkg, vaultsync.Application{
		RecoveryEntry: "2026-09-19T00-00-00Z-restore",
		Applied:       []vaultsync.AppliedPath{{Path: "Beta.md", Action: vaultsync.ActionDelete}},
		Now:           r.clock.Tick,
	})
	if err != nil {
		t.Fatalf("Acknowledge stale package: %v", err)
	}
	return staleArtifacts{manifest: pkg, payloads: payloads, receipt: receipt}
}

func mustInventory(t *testing.T, root string) *Inventory {
	t.Helper()
	inventory, err := BuildInventory(root)
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	return inventory
}

func reviewPlans(t *testing.T, r *reviewPair, local, peer *Inventory) (*Plan, *Plan) {
	t.Helper()
	localPlan, err := BuildPlan(r.pair.Local.Root, peer, r.clock.Tick)
	if err != nil {
		t.Fatalf("local plan: %v", err)
	}
	peerPlan, err := BuildPlan(r.pair.Peer.Root, local, r.clock.Tick)
	if err != nil {
		t.Fatalf("peer plan: %v", err)
	}
	return localPlan, peerPlan
}

func applyPlans(t *testing.T, r *reviewPair, local, peer *Plan) {
	t.Helper()
	if err := ApplyPlan(r.pair.Local.Root, local, r.clock.Tick); err != nil {
		t.Fatalf("apply local: %v", err)
	}
	if err := ApplyPlan(r.pair.Peer.Root, peer, r.clock.Tick); err != nil {
		t.Fatalf("apply peer: %v", err)
	}
}

func mustEnrollment(t *testing.T, root string) *vaultsync.Enrollment {
	t.Helper()
	enrollment, err := vaultsync.Load(root)
	if err != nil {
		t.Fatalf("load enrollment: %v", err)
	}
	return enrollment
}

func TestDifferencesMustBeReconciledOfflineBeforeApply(t *testing.T) {
	r := newReviewPair(t)
	before := r.prepare(t, r.pair.Local.Root)
	r.pair.Peer.Write(t, "Alpha.md", "newer peer body\n")
	peer, err := BuildInventory(r.pair.Peer.Root)
	if err != nil {
		t.Fatalf("peer inventory: %v", err)
	}
	plan, err := BuildPlan(r.pair.Local.Root, peer, r.clock.Tick)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.Differences) != 1 || plan.Differences[0].Path != "Alpha.md" {
		t.Fatalf("differences = %+v", plan.Differences)
	}
	if err := ApplyPlan(r.pair.Local.Root, plan, r.clock.Tick); err == nil || !strings.Contains(err.Error(), "reconcile them offline") {
		t.Fatalf("ApplyPlan difference error = %v", err)
	}
	after, err := BuildInventory(r.pair.Local.Root)
	if err != nil {
		t.Fatalf("inventory after refusal: %v", err)
	}
	if after.Digest != before.Digest {
		t.Fatal("refused review changed the restored inventory")
	}
}

func TestSequenceRegressionIsVisibleInTheReviewPlan(t *testing.T) {
	r := newReviewPair(t)
	local := r.prepare(t, r.pair.Local.Root)
	peer, err := BuildInventory(r.pair.Peer.Root)
	if err != nil {
		t.Fatalf("peer inventory: %v", err)
	}
	peer.Applied[local.DeviceID] = local.ExportSequence + 7
	peer.Digest, err = peer.digest()
	if err != nil {
		t.Fatalf("digest peer inventory: %v", err)
	}
	plan, err := BuildPlan(r.pair.Local.Root, peer, r.clock.Tick)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.Regressions) != 1 || !strings.Contains(plan.Regressions[0], "after peer observed") {
		t.Fatalf("regressions = %+v", plan.Regressions)
	}
}

func TestDistinctRestoreOperationReplacesAStoredProposedIdentity(t *testing.T) {
	r := newReviewPair(t)
	first, err := vaultsync.PrepareRestoreReviewContext(context.Background(), r.pair.Local.Root, strings.Repeat("a", 64), r.clock.Tick)
	if err != nil {
		t.Fatalf("first restore operation: %v", err)
	}
	second, err := vaultsync.PrepareRestoreReviewContext(context.Background(), r.pair.Local.Root, strings.Repeat("b", 64), r.clock.Tick)
	if err != nil {
		t.Fatalf("second restore operation: %v", err)
	}
	if first.ProposedDeviceID == second.ProposedDeviceID || second.RestoreOperation != strings.Repeat("b", 64) {
		t.Fatalf("new restore reused marker: first=%+v second=%+v", first, second)
	}
}

func TestApplyResumeRefusesPortableContentChangedAfterDecision(t *testing.T) {
	r := newReviewPair(t)
	local := r.prepare(t, r.pair.Local.Root)
	peer := mustInventory(t, r.pair.Peer.Root)
	plan, _ := reviewPlans(t, r, local, peer)
	held, err := checkpoint.Hold(r.pair.Local.Root, checkpoint.Options{Reason: "test reviewed decision"})
	if err != nil {
		t.Fatalf("hold checkpoint: %v", err)
	}
	if _, err := prepareApply(held.Context(context.Background()), r.pair.Local.Root, plan, r.clock.Tick); err != nil {
		t.Fatalf("record decision: %v", err)
	}
	if err := held.Release(); err != nil {
		t.Fatalf("release checkpoint: %v", err)
	}
	r.pair.Local.Write(t, "Alpha.md", "edited after review\n")
	if err := ApplyPlan(r.pair.Local.Root, plan, r.clock.Tick); err == nil || !strings.Contains(err.Error(), "content changed after review") {
		t.Fatalf("resume after edit = %v", err)
	}
	if enrollment := mustEnrollment(t, r.pair.Local.Root); enrollment.Epoch == plan.TargetEpoch {
		t.Fatal("resume enrolled content that was not reviewed")
	}
}

func TestApplyRefusesWhileContentRestoreIsPending(t *testing.T) {
	r := newReviewPair(t)
	local := r.prepare(t, r.pair.Local.Root)
	plan, _ := reviewPlans(t, r, local, mustInventory(t, r.pair.Peer.Root))
	archive := filepath.Join(t.TempDir(), "pending.tar.gz")
	if err := os.WriteFile(archive, []byte("staged restore"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := restorestate.Begin(r.pair.Local.Root, archive, false); err != nil {
		t.Fatalf("begin pending restore: %v", err)
	}
	if err := ApplyPlan(r.pair.Local.Root, plan, r.clock.Tick); !errors.Is(err, checkpoint.ErrRestorePending) {
		t.Fatalf("apply during pending restore = %v", err)
	}
}

func TestCompletedPlanRetryIsANoOp(t *testing.T) {
	r := newReviewPair(t)
	local := r.prepare(t, r.pair.Local.Root)
	plan, _ := reviewPlans(t, r, local, mustInventory(t, r.pair.Peer.Root))
	if err := ApplyPlan(r.pair.Local.Root, plan, r.clock.Tick); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if err := ApplyPlan(r.pair.Local.Root, plan, r.clock.Tick); err != nil {
		t.Fatalf("retry completed apply: %v", err)
	}
	if review, err := vaultsync.LoadRestoreReview(r.pair.Local.Root); err != nil || review != nil {
		t.Fatalf("completed retry recreated quarantine: %+v, %v", review, err)
	}
}

func TestSurvivorSpendsPendingProducerSequenceAcrossReenrollment(t *testing.T) {
	r := newReviewPair(t)
	spent := reservePendingSequence(t, r.pair.Peer.Root)
	local := r.prepare(t, r.pair.Local.Root)
	peer := mustInventory(t, r.pair.Peer.Root)
	if peer.ExportSequence != spent {
		t.Fatalf("review sequence = %d, want pending %d", peer.ExportSequence, spent)
	}
	localPlan, peerPlan := reviewPlans(t, r, local, peer)
	applyPlans(t, r, localPlan, peerPlan)
	state, err := vaultexport.LoadState(r.pair.Peer.Root)
	if err != nil {
		t.Fatalf("load survivor producer: %v", err)
	}
	if state.Sequence != spent || state.Pending != nil {
		t.Fatalf("survivor reused reservation: %+v", state)
	}
}

func TestOlderCheckpointRequiresReconciliationThenReconnectsAsBaseline(t *testing.T) {
	r := newReviewPair(t)
	restoreOlderCheckpoint(t, r)
	peer := mustInventory(t, r.pair.Peer.Root)
	plan, _ := reviewPlans(t, r, mustInventory(t, r.pair.Local.Root), peer)
	if len(plan.Differences) != 2 {
		t.Fatalf("older restore differences = %+v", plan.Differences)
	}
	if err := ApplyPlan(r.pair.Local.Root, plan, r.clock.Tick); err == nil {
		t.Fatal("accepted older content before reconciliation")
	}
	reconcileFromSurvivor(t, r)
	local := mustInventory(t, r.pair.Local.Root)
	localPlan, peerPlan := reviewPlans(t, r, local, mustInventory(t, r.pair.Peer.Root))
	applyPlans(t, r, localPlan, peerPlan)
	reconnectBaseline(t, r)
}
