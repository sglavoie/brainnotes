package vaultimport

import (
	"os"
	"testing"

	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

func requireUnpublishedApplication(t *testing.T, r *replicas, dir string) *Result {
	t.Helper()
	result, err := r.apply(t, dir)
	if err == nil {
		t.Fatal("publishing a receipt into a blocked channel was reported as a success")
	}
	if result == nil {
		t.Fatalf("an applied import was reported as if nothing had happened: %v", err)
	}
	if result.Receipt == nil {
		t.Fatal("the applied import retained no receipt")
	}
	if result.ReceiptPath != "" {
		t.Fatalf("the unpublished receipt is reported at %q", result.ReceiptPath)
	}
	return result
}

// blockOutgoingChannel puts a regular file where the outgoing channel directory
// belongs, so publishing a receipt fails for a reason no permission bit has to
// supply — a test running as root would sail through a mode change.
func blockOutgoingChannel(t *testing.T, root string) {
	t.Helper()
	outgoing := vaultsync.OutgoingDir(root)
	if err := os.RemoveAll(outgoing); err != nil {
		t.Fatalf("remove outgoing channel: %v", err)
	}
	if err := os.WriteFile(outgoing, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatalf("block outgoing channel: %v", err)
	}
}

func containsLine(lines []string, want string) bool {
	for _, line := range lines {
		if line == want {
			return true
		}
	}
	return false
}
