package web

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

// fixPreviewTTL bounds how long a cockpit fix preview receipt stays valid. It
// mirrors bulkPreviewTTL: a preview is a short-lived promise that the vault has
// not changed since the user saw the diff.
const fixPreviewTTL = 5 * time.Minute

// fixPreviewStore issues one-use, expiry-checked receipts for cockpit fix
// previews. It generalizes the bulk preview receipt pattern to fixes: a preview
// records the task identity (id + signature) and the action it previewed, and an
// apply must present a matching, unexpired receipt. Because a cockpit task
// signature embeds the on-disk hash of every affected note, comparing the stored
// signature against the freshly generated task's signature detects any change to
// the underlying files — the fix equivalent of the bulk store's digest check.
type fixPreviewStore struct {
	ttl time.Duration
	now func() time.Time

	mu      sync.Mutex
	records map[string]fixPreviewRecord
}

// fixPreviewReceipt is handed back to the client after a successful preview. The
// client echoes ID on apply to redeem the one-use receipt.
type fixPreviewReceipt struct {
	ID        string
	ExpiresAt time.Time
}

// fixPreviewRecord is the server-side memory of a single previewed fix.
type fixPreviewRecord struct {
	ID        string
	ExpiresAt time.Time
	TaskID    string
	Signature string
	Action    cockpit.ActionKind
}

func newFixPreviewStore(ttl time.Duration) *fixPreviewStore {
	if ttl <= 0 {
		ttl = fixPreviewTTL
	}
	return &fixPreviewStore{
		ttl:     ttl,
		now:     time.Now,
		records: make(map[string]fixPreviewRecord),
	}
}

// store records a previewed fix and returns its receipt. task carries the
// identity (id + signature) that a later apply must still match.
func (s *fixPreviewStore) store(task cockpit.Task, action cockpit.ActionKind) (fixPreviewReceipt, error) {
	if s == nil {
		return fixPreviewReceipt{}, noteops.Errorf(noteops.CodeInternal, "fix preview store is unavailable")
	}
	id, err := generateFixPreviewID()
	if err != nil {
		return fixPreviewReceipt{}, err
	}
	now := s.currentTime()
	receipt := fixPreviewReceipt{
		ID:        id,
		ExpiresAt: now.Add(s.ttl),
	}
	record := fixPreviewRecord{
		ID:        id,
		ExpiresAt: receipt.ExpiresAt,
		TaskID:    task.ID,
		Signature: task.Signature,
		Action:    action,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.pruneExpiredLocked(now)
	s.records[id] = record
	return receipt, nil
}

// consume redeems a receipt exactly once. It returns a preview_required error
// when the id is empty, unknown, or expired so a stale or replayed apply can
// never mutate the vault.
func (s *fixPreviewStore) consume(id string) (fixPreviewRecord, error) {
	if s == nil {
		return fixPreviewRecord{}, noteops.Errorf(noteops.CodePreviewRequired, "fix preview required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fixPreviewRecord{}, noteops.Errorf(noteops.CodePreviewRequired, "fix preview required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.currentTime()
	s.pruneExpiredLocked(now)
	record, ok := s.records[id]
	if !ok {
		return fixPreviewRecord{}, noteops.Errorf(noteops.CodePreviewRequired, "fix preview is missing or expired")
	}
	if !now.Before(record.ExpiresAt) {
		delete(s.records, id)
		return fixPreviewRecord{}, noteops.Errorf(noteops.CodePreviewRequired, "fix preview is missing or expired")
	}
	delete(s.records, id)
	return record, nil
}

func (s *fixPreviewStore) pruneExpiredLocked(now time.Time) {
	for id, record := range s.records {
		if !now.Before(record.ExpiresAt) {
			delete(s.records, id)
		}
	}
}

func (s *fixPreviewStore) currentTime() time.Time {
	if s == nil || s.now == nil {
		return time.Now().UTC()
	}
	return s.now().UTC()
}

func generateFixPreviewID() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", noteops.WrapError(noteops.CodeInternal, "generate fix preview id", err)
	}
	return hex.EncodeToString(buf[:]), nil
}
