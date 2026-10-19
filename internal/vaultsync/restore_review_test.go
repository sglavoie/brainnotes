package vaultsync

import (
	"context"
	"strings"
	"testing"
)

// TestARestoredMarkerFromAnotherOperationIsRemintedInAnyPhase covers a
// checkpoint taken while a re-enrollment was being accepted. Restoring it puts
// back a marker in phase "applying" that belongs to a restore operation which no
// longer exists; refusing to replace a non-pending marker would make every retry
// of the restore restore the same stuck marker and fail again. The same
// operation is still a retry and keeps its proposed identity.
func TestARestoredMarkerFromAnotherOperationIsRemintedInAnyPhase(t *testing.T) {
	r := enroll(t)
	root := r.pair.Local.Root
	ctx := context.Background()
	stuck := stuckApplyingMarker(t, r, strings.Repeat("a", 64))

	again, err := PrepareRestoreReviewContext(ctx, root, strings.Repeat("a", 64), r.clock.Tick)
	if err != nil || again.Phase != ReviewApplying || again.ProposedDeviceID != stuck.ProposedDeviceID {
		t.Fatalf("a retry of the same operation did not keep its marker: %+v, %v", again, err)
	}

	fresh, err := PrepareRestoreReviewContext(ctx, root, strings.Repeat("b", 64), r.clock.Tick)
	if err != nil {
		t.Fatalf("a new restore over a stuck marker from another operation: %v", err)
	}
	if fresh.Phase != ReviewPending || fresh.PlanSHA256 != "" || fresh.RestoreOperation != strings.Repeat("b", 64) {
		t.Fatalf("the stale decision survived the remint: %+v", fresh)
	}
	if fresh.ProposedDeviceID == stuck.ProposedDeviceID {
		t.Fatal("the remint reused the stale marker's proposed identity")
	}
}

// stuckApplyingMarker leaves the marker an acceptance interrupted after its
// decision would leave, for the restore operation named.
func stuckApplyingMarker(t *testing.T, r *replicas, operationID string) *RestoreReview {
	t.Helper()
	ctx := context.Background()
	if _, err := PrepareRestoreReviewContext(ctx, r.pair.Local.Root, operationID, r.clock.Tick); err != nil {
		t.Fatalf("prepare the marker the checkpoint captured: %v", err)
	}
	stuck, err := UpdateRestoreReviewContext(ctx, r.pair.Local.Root, func(review *RestoreReview) error {
		review.Phase = ReviewApplying
		review.PlanSHA256 = strings.Repeat("d", 64)
		review.TargetEpoch = review.PreviousEpoch + 1
		review.PeerDeviceID = r.peer.DeviceID
		return nil
	})
	if err != nil {
		t.Fatalf("advance the captured marker into applying: %v", err)
	}
	return stuck
}

// TestARestoredSurvivorMarkerIsReminted is the same boundary for the other kind
// of stale marker: a survivor's, captured while it applied its half of a review.
// A restore turns this device into a restored one, so the marker must rotate.
func TestARestoredSurvivorMarkerIsReminted(t *testing.T) {
	r := enroll(t)
	root := r.pair.Local.Root
	ctx := context.Background()
	if _, err := PreparePeerReviewContext(ctx, root, r.clock.Tick); err != nil {
		t.Fatalf("prepare the survivor marker: %v", err)
	}
	fresh, err := PrepareRestoreReviewContext(ctx, root, strings.Repeat("c", 64), r.clock.Tick)
	if err != nil {
		t.Fatalf("restore over a survivor marker: %v", err)
	}
	if !fresh.Restored || fresh.ProposedDeviceID == r.local.DeviceID {
		t.Fatalf("the restored device kept the survivor's identity: %+v", fresh)
	}
}
