package web

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

const propertyPreviewTTL = 5 * time.Minute

// propertyPreviewKind separates the receipt families so a note receipt can
// never be redeemed at the schema endpoint, or a saved-view receipt at either of
// them. Every preview-gated config or template write mints its own kind rather
// than sharing one: the receipt names which endpoint may redeem it, so the
// families cannot be crossed even by a caller holding a valid identifier.
type propertyPreviewKind string

const (
	propertyPreviewNotes    propertyPreviewKind = "notes"
	propertyPreviewSchema   propertyPreviewKind = "schema"
	propertyPreviewView     propertyPreviewKind = "view"
	propertyPreviewSetting  propertyPreviewKind = "setting"
	propertyPreviewTemplate propertyPreviewKind = "template"
)

// propertyPreviewStore holds short-lived, one-use receipts. A receipt carries
// the request it was issued for, the configuration generation it was planned
// under, and the digest of every file the plan touches, so apply can refuse a
// plan whose world moved on instead of writing against a stale preview.
type propertyPreviewStore struct {
	ttl time.Duration
	now func() time.Time

	mu      sync.Mutex
	records map[string]propertyPreviewRecord
}

type propertyPreviewReceipt struct {
	ID        string
	ExpiresAt time.Time
}

type propertyPreviewRecord struct {
	ID         string
	ExpiresAt  time.Time
	Kind       propertyPreviewKind
	Generation int64
	Mutation   propertyMutationRequest
	Schema     propertySchemaRequest
	View       viewMutationRequest
	Setting    settingMutationRequest
	Template   templateMutationRequest
	Files      []propertyPreviewFileState
}

// propertyPreviewFileState pins one path to the exact bytes the preview was
// computed from.
type propertyPreviewFileState struct {
	RelPath string
	AbsPath string
	Digest  string
}

func newPropertyPreviewStore(ttl time.Duration) *propertyPreviewStore {
	if ttl <= 0 {
		ttl = propertyPreviewTTL
	}
	return &propertyPreviewStore{
		ttl:     ttl,
		now:     time.Now,
		records: make(map[string]propertyPreviewRecord),
	}
}

func (s *propertyPreviewStore) store(record propertyPreviewRecord) (propertyPreviewReceipt, error) {
	if s == nil {
		return propertyPreviewReceipt{}, noteops.Errorf(noteops.CodeInternal, "properties: preview store is unavailable")
	}
	id, err := generatePropertyPreviewID()
	if err != nil {
		return propertyPreviewReceipt{}, err
	}
	now := s.now().UTC()
	record.ID = id
	record.ExpiresAt = now.Add(s.ttl)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.pruneExpiredLocked(now)
	s.records[id] = record
	return propertyPreviewReceipt{ID: id, ExpiresAt: record.ExpiresAt}, nil
}

// consume removes the receipt before returning it, so a replay of the same
// identifier can never reach apply twice even when the first apply fails.
func (s *propertyPreviewStore) consume(id string, kind propertyPreviewKind) (propertyPreviewRecord, error) {
	if s == nil {
		return propertyPreviewRecord{}, noteops.Errorf(noteops.CodePreviewRequired, "properties: preview required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return propertyPreviewRecord{}, noteops.Errorf(noteops.CodePreviewRequired, "properties: preview required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now().UTC()
	s.pruneExpiredLocked(now)
	record, ok := s.records[id]
	if !ok {
		return propertyPreviewRecord{}, noteops.Errorf(noteops.CodePreviewRequired, "properties: preview is missing or expired")
	}
	delete(s.records, id)
	if !now.Before(record.ExpiresAt) {
		return propertyPreviewRecord{}, noteops.Errorf(noteops.CodePreviewRequired, "properties: preview is missing or expired")
	}
	if record.Kind != kind {
		return propertyPreviewRecord{}, noteops.Errorf(noteops.CodePreviewRequired, "properties: preview does not match this action")
	}
	return record, nil
}

func (s *propertyPreviewStore) pruneExpiredLocked(now time.Time) {
	for id, record := range s.records {
		if !now.Before(record.ExpiresAt) {
			delete(s.records, id)
		}
	}
}

// propertyPreviewFilesUnchanged reports whether every recorded path still holds
// the exact bytes the preview was planned against.
func propertyPreviewFilesUnchanged(expected []propertyPreviewFileState) error {
	for _, state := range expected {
		digest, err := propertyFileDigest(state.AbsPath)
		if err != nil {
			return err
		}
		if digest != state.Digest {
			return noteops.Errorf(noteops.CodePreviewStale, "properties: %s changed since the preview", state.RelPath)
		}
	}
	return nil
}

// propertyFileDigest returns the hex SHA-256 of a regular file. A missing file
// digests to the empty string so an absent path stays comparable.
func propertyFileDigest(absPath string) (string, error) {
	info, err := os.Lstat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", noteops.WrapError(noteops.CodePreviewStale, "properties: stat preview path", err)
	}
	if !info.Mode().IsRegular() {
		return "", noteops.Errorf(noteops.CodePreviewStale, "properties: preview path is no longer a regular file")
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", noteops.WrapError(noteops.CodePreviewStale, "properties: read preview path", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func generatePropertyPreviewID() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", noteops.WrapError(noteops.CodeInternal, "properties: generate preview id", err)
	}
	return hex.EncodeToString(buf[:]), nil
}
