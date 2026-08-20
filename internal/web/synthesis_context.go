package web

import (
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// synthesisContextTTL is how long a context packet stays usable. Unlike a
// preview receipt it is reusable for its whole life: one packet backs repeated
// generation, preview, and apply attempts.
const synthesisContextTTL = time.Hour

const (
	// synthesisContextMaxRecords caps how many packets stay resident. Each
	// Prepare mints one, so a real curation session stays far below the
	// ceiling while a client that mints them in a loop cannot grow the map
	// without bound.
	synthesisContextMaxRecords = 32

	// synthesisContextMaxBytes caps the packet bytes held across every record.
	// One packet reaches recall.MaxCuratedBundleBytes at worst, so this bounds
	// resident context memory at eight worst-case packets rather than at
	// however many the record cap happens to allow.
	synthesisContextMaxBytes = 8 * recall.MaxCuratedBundleBytes
)

// synthesisSweepInterval is how often an idle store drops records that have
// aged out. Expiry is otherwise only observed by a later request, so an
// abandoned tab would leave its packet resident for the whole process life.
const synthesisSweepInterval = time.Minute

// synthesisNoteIdentity is one note of a context packet: the routable identity
// the browser uses plus the exact content hash the packet was built from. The
// hash never reaches the client — it exists so every later step can prove the
// file still holds the bytes the packet describes.
type synthesisNoteIdentity struct {
	Key     string `json:"key"`
	Title   string `json:"title"`
	RelPath string `json:"path"`
	Route   string `json:"route"`
	Folder  string `json:"folder"`
	SHA256  string `json:"-"`
}

// synthesisContextRecord is the whole server-side memory of one context packet.
// It is never written to disk: the packet contains complete note bodies, and the
// workflow contract keeps them in memory only, for the lifetime of the process.
type synthesisContextRecord struct {
	ID          string
	ExpiresAt   time.Time
	PurposeName string
	Purpose     recall.ContextPurpose
	Prompt      string
	Anchor      synthesisNoteIdentity
	Supporters  []synthesisNoteIdentity
	Packet      []byte
	Manifest    recall.ContextManifest
	// ConfigDigest is the hex SHA-256 of brainnotes.toml as it was on disk when
	// the packet was built, not the published configuration's hash: a file that
	// fails to reload leaves the last valid configuration published, and the
	// freshness check has to notice that edit too.
	ConfigDigest string
	Generation   int64
}

// notes returns the ordered identities the packet was built from: the anchor
// first, then supporters in the order the user chose.
func (r synthesisContextRecord) notes() []synthesisNoteIdentity {
	ordered := make([]synthesisNoteIdentity, 0, len(r.Supporters)+1)
	ordered = append(ordered, r.Anchor)
	ordered = append(ordered, r.Supporters...)
	return ordered
}

// retainedBytes weighs one record by what actually occupies memory: the packet
// and the prompt embedded in it. Identities and the manifest are small and
// fixed-shape.
func (r synthesisContextRecord) retainedBytes() int64 {
	return int64(len(r.Packet) + len(r.Prompt))
}

// synthesisContextStore holds live context packets in server memory. Records
// vanish on expiry or restart and are never persisted.
type synthesisContextStore struct {
	ttl        time.Duration
	maxRecords int
	maxBytes   int64

	mu         sync.Mutex
	now        func() time.Time
	records    map[string]synthesisContextRecord
	bytes      int64
	sweepEvery time.Duration
	sweeping   bool
}

func newSynthesisContextStore(ttl time.Duration) *synthesisContextStore {
	if ttl <= 0 {
		ttl = synthesisContextTTL
	}
	return &synthesisContextStore{
		ttl:        ttl,
		maxRecords: synthesisContextMaxRecords,
		maxBytes:   synthesisContextMaxBytes,
		now:        time.Now,
		records:    make(map[string]synthesisContextRecord),
	}
}

func (s *synthesisContextStore) store(record synthesisContextRecord) (synthesisContextRecord, error) {
	if s == nil {
		return synthesisContextRecord{}, noteops.Errorf(noteops.CodeInternal, "synthesis: context store is unavailable")
	}
	id, err := generateSynthesisID("context")
	if err != nil {
		return synthesisContextRecord{}, err
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

// lookup returns a live record without consuming it. An unknown or expired
// identifier is a not-found: the browser keeps its local draft and rebuilds the
// context rather than retrying the same identifier.
func (s *synthesisContextStore) lookup(id string) (synthesisContextRecord, error) {
	if s == nil {
		return synthesisContextRecord{}, noteops.Errorf(noteops.CodeInternal, "synthesis: context store is unavailable")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return synthesisContextRecord{}, noteops.Errorf(noteops.CodeInvalidRequest, "synthesis: context_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now().UTC()
	s.pruneExpiredLocked(now)
	record, ok := s.records[id]
	if !ok {
		return synthesisContextRecord{}, noteops.Errorf(noteops.CodeNotFound, "synthesis: context is unknown or expired")
	}
	return record, nil
}

func (s *synthesisContextStore) pruneExpiredLocked(now time.Time) {
	for id, record := range s.records {
		if !now.Before(record.ExpiresAt) {
			s.removeLocked(id)
		}
	}
}

// evictForLocked makes room for one incoming record by dropping the records
// closest to expiry first, which is deterministic and sheds the packet least
// likely to still be in use. An evicted context is the 404 the browser already
// answers by rebuilding, so bounding memory costs no new client behavior. A
// record larger than the whole byte budget is still admitted once the store is
// empty: refusing it would make the packet unreachable rather than merely
// short-lived.
func (s *synthesisContextStore) evictForLocked(incoming int64) {
	for len(s.records) > 0 && (len(s.records)+1 > s.maxRecords || s.bytes+incoming > s.maxBytes) {
		s.removeLocked(s.nearestExpiryLocked())
	}
}

func (s *synthesisContextStore) nearestExpiryLocked() string {
	oldest := ""
	var expires time.Time
	for id, record := range s.records {
		if oldest == "" || record.ExpiresAt.Before(expires) || (record.ExpiresAt.Equal(expires) && id < oldest) {
			oldest, expires = id, record.ExpiresAt
		}
	}
	return oldest
}

func (s *synthesisContextStore) removeLocked(id string) {
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
func (s *synthesisContextStore) startSweeper(interval time.Duration) {
	if s == nil || interval <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepEvery = interval
	s.startSweeperLocked()
}

func (s *synthesisContextStore) startSweeperLocked() {
	if s.sweeping || s.sweepEvery <= 0 || len(s.records) == 0 {
		return
	}
	s.sweeping = true
	go s.sweep(s.sweepEvery)
}

// sweep releases aged-out records without waiting for another request, then
// stops once the store is empty. A goroutine per idle store would outlive every
// server the process ever built, and an empty store has nothing left to expire;
// the next stored record starts a fresh sweeper.
func (s *synthesisContextStore) sweep(interval time.Duration) {
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
func (s *synthesisContextStore) setNow(clock func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = clock
}

func (s *synthesisContextStore) size() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

func (s *synthesisContextStore) retained() int64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bytes
}

// generateSynthesisID mints one unguessable identifier. kind only names the
// identifier in a failure message; context and preview identifiers share the
// same width and source of randomness.
func generateSynthesisID(kind string) (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", noteops.WrapError(noteops.CodeInternal, "synthesis: generate "+kind+" id", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// synthesisVaultID derives a stable opaque identifier for one vault root. The
// browser namespaces its saved workbench state under it, so it has to survive a
// restart while never carrying the path itself.
func synthesisVaultID(root string) string {
	canonical := strings.TrimSpace(root)
	if abs, err := filepath.Abs(canonical); err == nil {
		canonical = abs
	}
	if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
		canonical = resolved
	}
	return vault.HashBytes([]byte(canonical))[:32]
}
