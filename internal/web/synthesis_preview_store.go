package web

import (
	"strings"
	"sync"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

// synthesisPreviewTTL is how long a reviewed note stays applicable. It is far
// shorter than the context TTL because a preview describes a destination that
// must still be free, not a body of research.
const synthesisPreviewTTL = 5 * time.Minute

const (
	// synthesisPreviewMaxRecords caps how many unredeemed receipts stay
	// resident. Each Preview mints one and each Apply spends one, so only
	// abandoned receipts accumulate.
	synthesisPreviewMaxRecords = 32

	// synthesisPreviewMaxBytes caps the planned note bytes held across every
	// receipt. One plan carries a draft of at most
	// noteops.MaxSynthesisDraftBytes, so this bounds resident receipt memory at
	// eight worst-case drafts.
	synthesisPreviewMaxBytes = 8 * noteops.MaxSynthesisDraftBytes
)

// synthesisPreviewRecord is what apply redeems. It carries the planned note
// itself rather than the request that produced it, so apply writes the exact
// bytes the user reviewed instead of rebuilding them from data a client could
// have changed.
type synthesisPreviewRecord struct {
	ID        string
	ExpiresAt time.Time
	// ContextID names the packet the draft was written against. It is identity
	// only: the plan already pins every source revision, so an apply does not
	// need the context record to still be alive.
	ContextID    string
	ConfigDigest string
	Plan         *noteops.SynthesisPlan
}

// retainedBytes weighs one receipt by the planned note it carries, which is
// everything about it that scales with what the user wrote.
func (r synthesisPreviewRecord) retainedBytes() int64 {
	if r.Plan == nil {
		return 0
	}
	return int64(len(r.Plan.Content))
}

// synthesisPreviewStore holds one-use receipts in server memory.
type synthesisPreviewStore struct {
	ttl        time.Duration
	maxRecords int
	maxBytes   int64

	mu         sync.Mutex
	now        func() time.Time
	records    map[string]synthesisPreviewRecord
	bytes      int64
	sweepEvery time.Duration
	sweeping   bool
}

func newSynthesisPreviewStore(ttl time.Duration) *synthesisPreviewStore {
	if ttl <= 0 {
		ttl = synthesisPreviewTTL
	}
	return &synthesisPreviewStore{
		ttl:        ttl,
		maxRecords: synthesisPreviewMaxRecords,
		maxBytes:   synthesisPreviewMaxBytes,
		now:        time.Now,
		records:    make(map[string]synthesisPreviewRecord),
	}
}

func (s *synthesisPreviewStore) store(record synthesisPreviewRecord) (synthesisPreviewRecord, error) {
	if s == nil {
		return synthesisPreviewRecord{}, noteops.Errorf(noteops.CodeInternal, "synthesis: preview store is unavailable")
	}
	id, err := generateSynthesisID("preview")
	if err != nil {
		return synthesisPreviewRecord{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now().UTC()
	record.ID = id
	record.ExpiresAt = now.Add(s.ttl)

	s.pruneExpiredLocked(now)
	s.evictForLocked(record.retainedBytes())
	s.records[id] = record
	s.bytes += record.retainedBytes()
	s.startSweeperLocked()
	return record, nil
}

// consume removes the receipt before returning it, so a replay can never reach
// apply twice even when the first apply fails.
func (s *synthesisPreviewStore) consume(id string) (synthesisPreviewRecord, error) {
	if s == nil {
		return synthesisPreviewRecord{}, noteops.Errorf(noteops.CodePreviewRequired, "synthesis: preview required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return synthesisPreviewRecord{}, noteops.Errorf(noteops.CodePreviewRequired, "synthesis: preview required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now().UTC()
	s.pruneExpiredLocked(now)
	record, ok := s.records[id]
	if !ok {
		return synthesisPreviewRecord{}, noteops.Errorf(noteops.CodePreviewRequired, "synthesis: preview is missing or expired")
	}
	s.removeLocked(id)
	if !now.Before(record.ExpiresAt) {
		return synthesisPreviewRecord{}, noteops.Errorf(noteops.CodePreviewRequired, "synthesis: preview is missing or expired")
	}
	if record.Plan == nil {
		return synthesisPreviewRecord{}, noteops.Errorf(noteops.CodeInternal, "synthesis: preview carries no plan")
	}
	return record, nil
}

func (s *synthesisPreviewStore) pruneExpiredLocked(now time.Time) {
	for id, record := range s.records {
		if !now.Before(record.ExpiresAt) {
			s.removeLocked(id)
		}
	}
}

// evictForLocked makes room for one incoming receipt by dropping the receipts
// closest to expiry first. An evicted receipt is the preview_required the
// browser already answers by previewing again, and it is spent before any file
// is touched either way, so eviction can never cost a written note.
func (s *synthesisPreviewStore) evictForLocked(incoming int64) {
	for len(s.records) > 0 && (len(s.records)+1 > s.maxRecords || s.bytes+incoming > s.maxBytes) {
		s.removeLocked(s.nearestExpiryLocked())
	}
}

func (s *synthesisPreviewStore) nearestExpiryLocked() string {
	oldest := ""
	var expires time.Time
	for id, record := range s.records {
		if oldest == "" || record.ExpiresAt.Before(expires) || (record.ExpiresAt.Equal(expires) && id < oldest) {
			oldest, expires = id, record.ExpiresAt
		}
	}
	return oldest
}

func (s *synthesisPreviewStore) removeLocked(id string) {
	record, ok := s.records[id]
	if !ok {
		return
	}
	s.bytes -= record.retainedBytes()
	delete(s.records, id)
}

// startSweeper enables background expiry. It is separate from construction so a
// test driving a fake clock keeps the store entirely caller-driven; the server
// always enables it.
func (s *synthesisPreviewStore) startSweeper(interval time.Duration) {
	if s == nil || interval <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepEvery = interval
	s.startSweeperLocked()
}

func (s *synthesisPreviewStore) startSweeperLocked() {
	if s.sweeping || s.sweepEvery <= 0 || len(s.records) == 0 {
		return
	}
	s.sweeping = true
	go s.sweep(s.sweepEvery)
}

// sweep releases aged-out receipts without waiting for another request, then
// stops once the store is empty; the next stored receipt starts a fresh
// sweeper.
func (s *synthesisPreviewStore) sweep(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		s.pruneExpiredLocked(s.now().UTC())
		empty := len(s.records) == 0
		if empty {
			s.sweeping = false
		}
		s.mu.Unlock()
		if empty {
			return
		}
	}
}

// setNow replaces the store's clock. Only tests call it, and they go through
// the mutex because a running sweeper reads the clock from its own goroutine.
func (s *synthesisPreviewStore) setNow(clock func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = clock
}

func (s *synthesisPreviewStore) size() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

func (s *synthesisPreviewStore) retained() int64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bytes
}
