package web

import (
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/cockpit"
	"github.com/sglavoie/brainnotes/internal/noteops"
)

// previewCapCase drives one preview store through a receipt flood. Each store
// mints its own receipt type, so the case reduces them to an id and a size.
type previewCapCase struct {
	name    string
	setup   func(clock func() time.Time, maxRecords int)
	store   func(t *testing.T) string
	consume func(id string) error
	size    func() int
}

func previewCapCases() []previewCapCase {
	var bulk *bulkPreviewStore
	var fix *fixPreviewStore
	var property *propertyPreviewStore
	return []previewCapCase{
		{
			name: "bulk",
			setup: func(clock func() time.Time, maxRecords int) {
				bulk = newBulkPreviewStore(bulkPreviewTTL)
				bulk.now, bulk.maxRecords = clock, maxRecords
			},
			store: func(t *testing.T) string {
				receipt, err := bulk.store(bulkManagementRequest{Action: "move"}, nil, 0)
				if err != nil {
					t.Fatalf("store: %v", err)
				}
				return receipt.ID
			},
			consume: func(id string) error { _, err := bulk.consume(id); return err },
			size:    func() int { return len(bulk.records) },
		},
		{
			name: "fix",
			setup: func(clock func() time.Time, maxRecords int) {
				fix = newFixPreviewStore(fixPreviewTTL)
				fix.now, fix.maxRecords = clock, maxRecords
			},
			store: func(t *testing.T) string {
				receipt, err := fix.store(cockpit.Task{ID: "task", Signature: "sig"}, cockpit.ActionKind("fix"))
				if err != nil {
					t.Fatalf("store: %v", err)
				}
				return receipt.ID
			},
			consume: func(id string) error { _, err := fix.consume(id); return err },
			size:    func() int { return len(fix.records) },
		},
		{
			name: "property",
			setup: func(clock func() time.Time, maxRecords int) {
				property = newPropertyPreviewStore(propertyPreviewTTL)
				property.now, property.maxRecords = clock, maxRecords
			},
			store: func(t *testing.T) string {
				receipt, err := property.store(propertyPreviewRecord{Kind: propertyPreviewNotes})
				if err != nil {
					t.Fatalf("store: %v", err)
				}
				return receipt.ID
			},
			consume: func(id string) error { _, err := property.consume(id, propertyPreviewNotes); return err },
			size:    func() int { return len(property.records) },
		},
	}
}

// TestPreviewStoresEvictOldestReceiptAtCap pins the bound every preview store
// shares: a flood of abandoned previews stays at maxRecords, the oldest receipt
// is the one dropped, and the newest still redeems.
func TestPreviewStoresEvictOldestReceiptAtCap(t *testing.T) {
	for _, tc := range previewCapCases() {
		t.Run(tc.name, func(t *testing.T) { assertPreviewStoreEvictsOldest(t, tc) })
	}
}

func assertPreviewStoreEvictsOldest(t *testing.T, tc previewCapCase) {
	t.Helper()
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	tc.setup(func() time.Time { return now }, 3)

	ids := make([]string, 0, 5)
	for range 5 {
		ids = append(ids, tc.store(t))
		now = now.Add(time.Second)
	}
	if got := tc.size(); got != 3 {
		t.Fatalf("resident receipts = %d, want 3", got)
	}
	for _, evicted := range ids[:2] {
		if code := noteops.CodeOf(tc.consume(evicted)); code != noteops.CodePreviewRequired {
			t.Fatalf("evicted receipt code = %q, want %q", code, noteops.CodePreviewRequired)
		}
	}
	for _, kept := range ids[2:] {
		if err := tc.consume(kept); err != nil {
			t.Fatalf("retained receipt did not redeem: %v", err)
		}
	}
}

// TestPreviewStoresDefaultToSharedCap guards the constructors: a store built
// for the server must carry the shared bound rather than an unbounded zero.
func TestPreviewStoresDefaultToSharedCap(t *testing.T) {
	if got := newBulkPreviewStore(0).maxRecords; got != previewStoreMaxRecords {
		t.Fatalf("bulk maxRecords = %d, want %d", got, previewStoreMaxRecords)
	}
	if got := newFixPreviewStore(0).maxRecords; got != previewStoreMaxRecords {
		t.Fatalf("fix maxRecords = %d, want %d", got, previewStoreMaxRecords)
	}
	if got := newPropertyPreviewStore(0).maxRecords; got != previewStoreMaxRecords {
		t.Fatalf("property maxRecords = %d, want %d", got, previewStoreMaxRecords)
	}
}
