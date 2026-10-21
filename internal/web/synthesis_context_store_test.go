package web

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/noteops"
)

func TestSynthesisContextStore_LookupIsReusable(t *testing.T) {
	store := newSynthesisContextStore(time.Hour)
	stored, err := store.store(synthesisContextRecord{PurposeName: "synthesis"})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if stored.ID == "" {
		t.Fatal("stored id is empty")
	}
	if len(stored.ID) != 64 {
		t.Fatalf("id length = %d, want 64 hex characters", len(stored.ID))
	}

	for attempt := range 3 {
		record, err := store.lookup(stored.ID)
		if err != nil {
			t.Fatalf("lookup %d: %v", attempt, err)
		}
		if record.PurposeName != "synthesis" {
			t.Fatalf("purpose = %q, want synthesis", record.PurposeName)
		}
	}
	if store.size() != 1 {
		t.Fatalf("size = %d, want 1 after repeated lookups", store.size())
	}
}

func TestSynthesisContextStore_IdentifiersAreDistinct(t *testing.T) {
	store := newSynthesisContextStore(time.Hour)
	seen := make(map[string]struct{}, 16)
	for range 16 {
		stored, err := store.store(synthesisContextRecord{})
		if err != nil {
			t.Fatalf("store: %v", err)
		}
		if _, exists := seen[stored.ID]; exists {
			t.Fatalf("duplicate context id %q", stored.ID)
		}
		seen[stored.ID] = struct{}{}
	}
}

func TestSynthesisContextStore_UnknownAndEmptyIdentifiers(t *testing.T) {
	store := newSynthesisContextStore(time.Hour)

	if _, err := store.lookup("  "); noteops.CodeOf(err) != noteops.CodeInvalidRequest {
		t.Fatalf("empty id code = %q, want invalid_request", noteops.CodeOf(err))
	}
	if _, err := store.lookup("deadbeef"); noteops.CodeOf(err) != noteops.CodeNotFound {
		t.Fatalf("unknown id code = %q, want not_found", noteops.CodeOf(err))
	}

	var nilStore *synthesisContextStore
	if _, err := nilStore.lookup("deadbeef"); noteops.CodeOf(err) != noteops.CodeInternal {
		t.Fatalf("nil store code = %q, want internal_error", noteops.CodeOf(err))
	}
	if nilStore.size() != 0 {
		t.Fatal("nil store size is not zero")
	}
}

func TestSynthesisContextStore_ExpiryIsNotFound(t *testing.T) {
	store := newSynthesisContextStore(time.Hour)
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	store.setNow(func() time.Time { return now })

	stored, err := store.store(synthesisContextRecord{})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if want := now.Add(time.Hour); !stored.ExpiresAt.Equal(want) {
		t.Fatalf("expires_at = %s, want %s", stored.ExpiresAt, want)
	}

	now = now.Add(time.Hour - time.Second)
	if _, err := store.lookup(stored.ID); err != nil {
		t.Fatalf("lookup just before expiry: %v", err)
	}

	now = now.Add(time.Second)
	if _, err := store.lookup(stored.ID); noteops.CodeOf(err) != noteops.CodeNotFound {
		t.Fatalf("expired code = %q, want not_found", noteops.CodeOf(err))
	}
}

// TestSynthesisContextStore_PruneOnStore proves the map does not grow without
// bound: a record that has aged out is dropped by the next write even though
// nobody ever looked it up.
func TestSynthesisContextStore_PruneOnStore(t *testing.T) {
	store := newSynthesisContextStore(time.Minute)
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	store.setNow(func() time.Time { return now })

	for range 5 {
		if _, err := store.store(synthesisContextRecord{}); err != nil {
			t.Fatalf("store: %v", err)
		}
	}
	if store.size() != 5 {
		t.Fatalf("size = %d, want 5", store.size())
	}

	now = now.Add(2 * time.Minute)
	if _, err := store.store(synthesisContextRecord{}); err != nil {
		t.Fatalf("store after expiry: %v", err)
	}
	if store.size() != 1 {
		t.Fatalf("size = %d, want 1 after pruning", store.size())
	}
}

// TestSynthesisContextStore_EvictsNearestExpiryOverRecordCap proves the record
// cap is enforced deterministically: the packets closest to expiry go, and the
// newest ones a user is plausibly still working with stay.
func TestSynthesisContextStore_EvictsNearestExpiryOverRecordCap(t *testing.T) {
	store := newSynthesisContextStore(time.Hour)
	store.maxRecords = 3
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	store.setNow(func() time.Time { return now })

	packet := []byte("packet bytes")
	ids := make([]string, 0, 5)
	for range 5 {
		stored, err := store.store(synthesisContextRecord{Packet: packet})
		if err != nil {
			t.Fatalf("store: %v", err)
		}
		ids = append(ids, stored.ID)
		now = now.Add(time.Second)
	}

	if store.size() != 3 {
		t.Fatalf("size = %d, want 3", store.size())
	}
	if want := int64(3 * len(packet)); store.retained() != want {
		t.Fatalf("retained = %d, want %d", store.retained(), want)
	}
	for _, id := range ids[:2] {
		if _, err := store.lookup(id); noteops.CodeOf(err) != noteops.CodeNotFound {
			t.Fatalf("evicted id %s code = %q, want not_found", id, noteops.CodeOf(err))
		}
	}
	for _, id := range ids[2:] {
		if _, err := store.lookup(id); err != nil {
			t.Fatalf("retained id %s: %v", id, err)
		}
	}
}

// TestSynthesisContextStore_EvictsOverByteCap proves a handful of near-1 MiB
// packets cannot pile up under the record cap, and pins the one deliberate
// exception: a record wider than the whole budget is still admitted alone,
// because refusing it would make the packet unreachable rather than short-lived.
func TestSynthesisContextStore_EvictsOverByteCap(t *testing.T) {
	store := newSynthesisContextStore(time.Hour)
	store.maxRecords = 64
	store.maxBytes = 3 << 10
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	store.setNow(func() time.Time { return now })

	for range 6 {
		if _, err := store.store(synthesisContextRecord{Packet: make([]byte, 1<<10)}); err != nil {
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

	oversized, err := store.store(synthesisContextRecord{Packet: make([]byte, 8<<10)})
	if err != nil {
		t.Fatalf("store oversized packet: %v", err)
	}
	if store.size() != 1 {
		t.Fatalf("size = %d, want 1 after an oversized packet cleared the store", store.size())
	}
	if _, err := store.lookup(oversized.ID); err != nil {
		t.Fatalf("lookup oversized packet: %v", err)
	}
}

// TestSynthesisContextStore_ConcurrentStoresStayWithinCapacity drives more
// simultaneous writers than the store may hold and watches the bounds from a
// separate goroutine, so a capacity check that only held between requests would
// fail here.
func TestSynthesisContextStore_ConcurrentStoresStayWithinCapacity(t *testing.T) {
	store := newSynthesisContextStore(time.Hour)
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
			if _, err := store.store(synthesisContextRecord{Packet: make([]byte, 1<<10)}); err != nil {
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

// TestSynthesisContextStore_SweeperReleasesIdleRecords proves expiry no longer
// waits for a later request: nothing here stores or looks anything up after the
// packets land, and size() does not prune, so only the background sweep can
// release them.
func TestSynthesisContextStore_SweeperReleasesIdleRecords(t *testing.T) {
	store := newSynthesisContextStore(20 * time.Millisecond)
	store.startSweeper(time.Millisecond)

	for range 3 {
		if _, err := store.store(synthesisContextRecord{Packet: make([]byte, 1<<10)}); err != nil {
			t.Fatalf("store: %v", err)
		}
	}
	waitForEmptyStore(t, store.size, "context packets")
	if store.retained() != 0 {
		t.Fatalf("retained = %d after the sweep, want 0", store.retained())
	}

	// The sweeper stops with the store empty, so a later packet has to start a
	// fresh one rather than sit unswept forever.
	if _, err := store.store(synthesisContextRecord{Packet: make([]byte, 1<<10)}); err != nil {
		t.Fatalf("store after the sweeper stopped: %v", err)
	}
	waitForEmptyStore(t, store.size, "a context packet stored after the sweeper stopped")
}

func waitForEmptyStore(t *testing.T, size func() int, subject string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if size() == 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("%s were still resident %s after expiry", subject, 5*time.Second)
}

func TestSynthesisContextStore_ConcurrentAccess(t *testing.T) {
	store := newSynthesisContextStore(time.Hour)

	const workers = 16
	ids := make([]string, workers)
	var wg sync.WaitGroup
	for index := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stored, err := store.store(synthesisContextRecord{PurposeName: "synthesis"})
			if err != nil {
				t.Errorf("store: %v", err)
				return
			}
			ids[index] = stored.ID
		}()
	}
	wg.Wait()
	if store.size() != workers {
		t.Fatalf("size = %d, want %d", store.size(), workers)
	}

	for range 4 {
		for _, id := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := store.lookup(id); err != nil {
					t.Errorf("lookup %s: %v", id, err)
				}
			}()
		}
	}
	wg.Wait()
}

// TestSynthesisContextRecord_NotesOrder pins the ordering the manifest reports:
// anchor first, then supporters exactly as the user chose them.
func TestSynthesisContextRecord_NotesOrder(t *testing.T) {
	record := synthesisContextRecord{
		Anchor: synthesisNoteIdentity{Key: "anchor"},
		Supporters: []synthesisNoteIdentity{
			{Key: "second"},
			{Key: "first"},
		},
	}
	got := record.notes()
	want := []string{"anchor", "second", "first"}
	if len(got) != len(want) {
		t.Fatalf("notes length = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index].Key != want[index] {
			t.Fatalf("notes[%d] = %q, want %q", index, got[index].Key, want[index])
		}
	}
}

func TestSynthesisVaultID_StableAndOpaque(t *testing.T) {
	root := t.TempDir()

	first := synthesisVaultID(root)
	if len(first) != 32 {
		t.Fatalf("vault id length = %d, want 32", len(first))
	}
	if first != synthesisVaultID(root) {
		t.Fatal("vault id is not stable across calls")
	}
	if first != synthesisVaultID(root+"/") {
		t.Fatal("vault id changed for a trailing separator")
	}
	if synthesisVaultID(t.TempDir()) == first {
		t.Fatal("two vault roots produced the same id")
	}
	if strings.Contains(first, root) {
		t.Fatal("vault id leaks the vault path")
	}
}
