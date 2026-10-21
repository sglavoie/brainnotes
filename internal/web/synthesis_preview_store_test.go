package web

import (
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/noteops"
)

func newSynthesisPreviewTestRecord(size int) synthesisPreviewRecord {
	return synthesisPreviewRecord{Plan: &noteops.SynthesisPlan{Content: make([]byte, size)}}
}

// TestSynthesisPreviewStore_ConsumeReleasesBytes pins that a redeemed receipt
// stops counting against the store's budget, so an ordinary create loop never
// pushes the store toward eviction.
func TestSynthesisPreviewStore_ConsumeReleasesBytes(t *testing.T) {
	store := newSynthesisPreviewStore(time.Minute)

	stored, err := store.store(newSynthesisPreviewTestRecord(1 << 10))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if want := int64(1 << 10); store.retained() != want {
		t.Fatalf("retained = %d, want %d", store.retained(), want)
	}

	if _, err := store.consume(stored.ID); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if store.size() != 0 {
		t.Fatalf("size = %d after consume, want 0", store.size())
	}
	if store.retained() != 0 {
		t.Fatalf("retained = %d after consume, want 0", store.retained())
	}
}

// TestSynthesisPreviewStore_EvictsNearestExpiryOverRecordCap proves abandoned
// receipts cannot accumulate. An evicted receipt is the preview_required the
// browser already answers by previewing again, and no file was ever touched.
func TestSynthesisPreviewStore_EvictsNearestExpiryOverRecordCap(t *testing.T) {
	store := newSynthesisPreviewStore(time.Minute)
	store.maxRecords = 3
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	store.setNow(func() time.Time { return now })

	ids := make([]string, 0, 5)
	for range 5 {
		stored, err := store.store(newSynthesisPreviewTestRecord(64))
		if err != nil {
			t.Fatalf("store: %v", err)
		}
		ids = append(ids, stored.ID)
		now = now.Add(time.Second)
	}

	if store.size() != 3 {
		t.Fatalf("size = %d, want 3", store.size())
	}
	if want := int64(3 * 64); store.retained() != want {
		t.Fatalf("retained = %d, want %d", store.retained(), want)
	}
	for _, id := range ids[:2] {
		if _, err := store.consume(id); noteops.CodeOf(err) != noteops.CodePreviewRequired {
			t.Fatalf("evicted id %s code = %q, want preview_required", id, noteops.CodeOf(err))
		}
	}
	for _, id := range ids[2:] {
		if _, err := store.consume(id); err != nil {
			t.Fatalf("retained id %s: %v", id, err)
		}
	}
}

func TestSynthesisPreviewStore_EvictsOverByteCap(t *testing.T) {
	store := newSynthesisPreviewStore(time.Minute)
	store.maxRecords = 64
	store.maxBytes = 3 << 10
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	store.setNow(func() time.Time { return now })

	for range 6 {
		if _, err := store.store(newSynthesisPreviewTestRecord(1 << 10)); err != nil {
			t.Fatalf("store: %v", err)
		}
		now = now.Add(time.Second)
	}
	if store.size() != 3 {
		t.Fatalf("size = %d, want 3", store.size())
	}
	if store.retained() != store.maxBytes {
		t.Fatalf("retained = %d, want %d", store.retained(), store.maxBytes)
	}
}

func TestSynthesisPreviewStore_ConcurrentStoresStayWithinCapacity(t *testing.T) {
	store := newSynthesisPreviewStore(time.Minute)
	store.maxRecords = 8
	store.maxBytes = 8 << 10

	done := make(chan struct{})
	var watcher sync.WaitGroup
	watcher.Add(1)
	go func() {
		defer watcher.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			if size := store.size(); size > 8 {
				t.Errorf("observed size = %d, want at most 8", size)
				return
			}
			if retained := store.retained(); retained > 8<<10 {
				t.Errorf("observed retained = %d, want at most %d", retained, 8<<10)
				return
			}
		}
	}()

	var writers sync.WaitGroup
	for range 64 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			if _, err := store.store(newSynthesisPreviewTestRecord(1 << 10)); err != nil {
				t.Errorf("store: %v", err)
			}
		}()
	}
	writers.Wait()
	close(done)
	watcher.Wait()

	if store.size() != 8 {
		t.Fatalf("size = %d, want 8", store.size())
	}
	if want := int64(8 << 10); store.retained() != want {
		t.Fatalf("retained = %d, want %d", store.retained(), want)
	}
}

// TestSynthesisPreviewStore_SweeperReleasesIdleReceipts proves a receipt nobody
// redeems is released on its own: nothing stores or consumes after the receipts
// land, and size() does not prune.
func TestSynthesisPreviewStore_SweeperReleasesIdleReceipts(t *testing.T) {
	store := newSynthesisPreviewStore(20 * time.Millisecond)
	store.startSweeper(time.Millisecond)

	for range 3 {
		if _, err := store.store(newSynthesisPreviewTestRecord(1 << 10)); err != nil {
			t.Fatalf("store: %v", err)
		}
	}
	waitForEmptyStore(t, store.size, "preview receipts")
	if store.retained() != 0 {
		t.Fatalf("retained = %d after the sweep, want 0", store.retained())
	}

	if _, err := store.store(newSynthesisPreviewTestRecord(1 << 10)); err != nil {
		t.Fatalf("store after the sweeper stopped: %v", err)
	}
	waitForEmptyStore(t, store.size, "a preview receipt stored after the sweeper stopped")
}
