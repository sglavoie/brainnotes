package vaultrecovery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/checkpoint"
)

// TestReviewApplyReportsAFailedCheckpointRelease proves an acceptance that
// otherwise succeeded still says the checkpoint record outlived it: the next
// writer would otherwise wait on a hold nobody owns, with nothing having said so.
func TestReviewApplyReportsAFailedCheckpointRelease(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop root from removing the record")
	}
	r := newReviewPair(t)
	root := r.pair.Local.Root
	local := r.prepare(t, root)
	plan, _ := reviewPlans(t, r, local, mustInventory(t, r.pair.Peer.Root))
	recordDir := checkpointRecordDir(t, root)

	// Once the acceptance is complete, the record can no longer be removed.
	previous := applyCrashHook
	applyCrashHook = func(point string) {
		if point == crashCompleted {
			if err := os.Chmod(recordDir, 0o555); err != nil { // #nosec G302 -- read-only on purpose
				t.Errorf("make the record directory read-only: %v", err)
			}
		}
	}
	t.Cleanup(func() {
		applyCrashHook = previous
		_ = os.Chmod(recordDir, 0o755) // #nosec G302 -- restores the fixture's own mode
	})

	err := ApplyPlan(root, plan, r.clock.Tick)
	if err == nil || !strings.Contains(err.Error(), "release checkpoint") {
		t.Fatalf("ApplyPlan error = %v, want the checkpoint release failure", err)
	}
	if err := os.Chmod(recordDir, 0o755); err != nil { // #nosec G302 -- restores the fixture's own mode
		t.Fatal(err)
	}
	requireAccepted(t, root, plan, local)
}

func checkpointRecordDir(t *testing.T, root string) string {
	t.Helper()
	record, err := checkpoint.RecordPath(root)
	if err != nil {
		t.Fatalf("RecordPath: %v", err)
	}
	return filepath.Dir(record)
}
