package web

import "time"

// previewStoreMaxRecords caps how many unredeemed receipts one preview store
// keeps resident. Each preview mints one receipt and each apply spends one, so
// only abandoned receipts accumulate; without a cap a client that previews in a
// loop and never applies grows server memory for a whole TTL window.
const previewStoreMaxRecords = 64

// evictOldestPreviewsLocked makes room for one incoming receipt by dropping the
// receipts closest to expiry first, until fewer than maxRecords remain. Every
// receipt in a store shares one TTL, so the nearest expiry is also the oldest
// preview. An evicted receipt is the preview_required the browser already
// answers by previewing again, and it is spent before any file is touched, so
// eviction can never cost a write. The caller holds the store's mutex.
func evictOldestPreviewsLocked[R any](records map[string]R, maxRecords int, expiresAt func(R) time.Time) {
	if maxRecords <= 0 {
		maxRecords = previewStoreMaxRecords
	}
	for len(records) > 0 && len(records)+1 > maxRecords {
		delete(records, nearestPreviewExpiry(records, expiresAt))
	}
}

func nearestPreviewExpiry[R any](records map[string]R, expiresAt func(R) time.Time) string {
	oldest := ""
	var expires time.Time
	for id, record := range records {
		at := expiresAt(record)
		if oldest == "" || at.Before(expires) || (at.Equal(expires) && id < oldest) {
			oldest, expires = id, at
		}
	}
	return oldest
}
