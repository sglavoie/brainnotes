package web

import (
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/cockpit"
	"github.com/sglavoie/brainnotes/internal/noteops"
)

// fixedClock returns a controllable time source for driving fix preview expiry
// deterministically, mirroring the store's own now func() time.Time field.
func fixedClock(t *time.Time) func() time.Time {
	return func() time.Time { return *t }
}

func TestFixPreviewStore_ConsumeRedeemsOnce(t *testing.T) {
	base := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	now := base
	store := newFixPreviewStore(fixPreviewTTL)
	store.now = fixedClock(&now)

	task := cockpit.Task{ID: "task-1", Signature: "sig-1"}
	receipt, err := store.store(task, cockpit.ActionKind("fix"))
	if err != nil {
		t.Fatalf("store: unexpected error: %v", err)
	}

	record, err := store.consume(receipt.ID)
	if err != nil {
		t.Fatalf("consume: unexpected error: %v", err)
	}
	if record.TaskID != task.ID || record.Signature != task.Signature {
		t.Fatalf("consume: got record %+v, want TaskID=%q Signature=%q", record, task.ID, task.Signature)
	}

	// A second consume of the same id must fail: the receipt is one-use.
	if _, err := store.consume(receipt.ID); err == nil {
		t.Fatal("consume: expected error on second redemption, got nil")
	} else if code := noteops.CodeOf(err); code != noteops.CodePreviewRequired {
		t.Fatalf("consume: got code %q, want %q", code, noteops.CodePreviewRequired)
	}
}

func TestFixPreviewStore_ConsumeExpiredReceipt(t *testing.T) {
	base := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	now := base
	store := newFixPreviewStore(fixPreviewTTL)
	store.now = fixedClock(&now)

	receipt, err := store.store(cockpit.Task{ID: "task-1", Signature: "sig-1"}, cockpit.ActionKind("fix"))
	if err != nil {
		t.Fatalf("store: unexpected error: %v", err)
	}

	// Advance past the receipt's expiry.
	now = receipt.ExpiresAt.Add(time.Second)

	if _, err := store.consume(receipt.ID); err == nil {
		t.Fatal("consume: expected error on expired receipt, got nil")
	} else if code := noteops.CodeOf(err); code != noteops.CodePreviewRequired {
		t.Fatalf("consume: got code %q, want %q", code, noteops.CodePreviewRequired)
	}
}

func TestFixPreviewStore_ConsumeUnknownID(t *testing.T) {
	base := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	now := base
	store := newFixPreviewStore(fixPreviewTTL)
	store.now = fixedClock(&now)

	if _, err := store.consume("does-not-exist"); err == nil {
		t.Fatal("consume: expected error on unknown id, got nil")
	} else if code := noteops.CodeOf(err); code != noteops.CodePreviewRequired {
		t.Fatalf("consume: got code %q, want %q", code, noteops.CodePreviewRequired)
	}
}
