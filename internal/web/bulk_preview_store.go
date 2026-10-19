package web

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

const bulkPreviewTTL = 5 * time.Minute

type bulkPreviewStore struct {
	ttl        time.Duration
	maxRecords int
	now        func() time.Time

	mu      sync.Mutex
	records map[string]bulkPreviewRecord
}

type bulkPreviewReceipt struct {
	ID        string
	ExpiresAt time.Time
}

type bulkPreviewRecord struct {
	ID           string
	ExpiresAt    time.Time
	Fingerprint  bulkManagementRequestFingerprint
	Destinations []bulkPreviewPlannedDestination
	Affected     []bulkPreviewAffectedState
	TrashPlans   []noteops.TrashDestinationPlan
	// Generation is the configuration generation the preview was planned
	// against. Move and property actions read the configuration, so an apply
	// after it changed would carry out a plan nobody was shown.
	Generation int64
}

type bulkPreviewPlannedDestination struct {
	Key     string
	AbsPath string
	RelPath string
}

type bulkPreviewAffectedState struct {
	Role    string
	AbsPath string
	RelPath string
	Exists  bool
	Digest  string
}

func newBulkPreviewStore(ttl time.Duration) *bulkPreviewStore {
	if ttl <= 0 {
		ttl = bulkPreviewTTL
	}
	return &bulkPreviewStore{
		ttl:        ttl,
		maxRecords: previewStoreMaxRecords,
		now:        time.Now,
		records:    make(map[string]bulkPreviewRecord),
	}
}

func (s *bulkPreviewStore) store(req bulkManagementRequest, result *noteops.BulkPreviewResult, generation int64) (bulkPreviewReceipt, error) {
	if s == nil {
		return bulkPreviewReceipt{}, noteops.Errorf(noteops.CodeInternal, "bulk preview store is unavailable")
	}
	states, err := bulkPreviewAffectedStates(result)
	if err != nil {
		return bulkPreviewReceipt{}, err
	}
	id, err := generateBulkPreviewID()
	if err != nil {
		return bulkPreviewReceipt{}, err
	}
	now := s.now().UTC()
	receipt := bulkPreviewReceipt{
		ID:        id,
		ExpiresAt: now.Add(s.ttl),
	}
	record := bulkPreviewRecord{
		ID:           id,
		ExpiresAt:    receipt.ExpiresAt,
		Fingerprint:  req.fingerprint(),
		Destinations: bulkPreviewDestinations(result),
		Affected:     states,
		TrashPlans:   bulkPreviewTrashPlans(result),
		Generation:   generation,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.pruneExpiredLocked(now)
	evictOldestPreviewsLocked(s.records, s.maxRecords, func(r bulkPreviewRecord) time.Time { return r.ExpiresAt })
	s.records[id] = record
	return receipt, nil
}

func (s *bulkPreviewStore) consume(id string) (bulkPreviewRecord, error) {
	if s == nil {
		return bulkPreviewRecord{}, noteops.Errorf(noteops.CodePreviewRequired, "bulk preview required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return bulkPreviewRecord{}, noteops.Errorf(noteops.CodePreviewRequired, "bulk preview required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now().UTC()
	s.pruneExpiredLocked(now)
	record, ok := s.records[id]
	if !ok {
		return bulkPreviewRecord{}, noteops.Errorf(noteops.CodePreviewRequired, "bulk preview is missing or expired")
	}
	delete(s.records, id)
	if !now.Before(record.ExpiresAt) {
		return bulkPreviewRecord{}, noteops.Errorf(noteops.CodePreviewRequired, "bulk preview is missing or expired")
	}
	return record, nil
}

func (s *bulkPreviewStore) pruneExpiredLocked(now time.Time) {
	for id, record := range s.records {
		if !now.Before(record.ExpiresAt) {
			delete(s.records, id)
		}
	}
}

func bulkPreviewAffectedStates(result *noteops.BulkPreviewResult) ([]bulkPreviewAffectedState, error) {
	if result == nil {
		return nil, nil
	}
	states := make([]bulkPreviewAffectedState, 0)
	for _, item := range result.Results {
		for _, affected := range item.Affected {
			state, err := bulkPreviewAffectedStateForPath(affected)
			if err != nil {
				return nil, err
			}
			states = append(states, state)
		}
	}
	return states, nil
}

func bulkPreviewCurrentAffectedStates(expected []bulkPreviewAffectedState) ([]bulkPreviewAffectedState, error) {
	current := make([]bulkPreviewAffectedState, 0, len(expected))
	for _, state := range expected {
		exists, digest, err := bulkPreviewFileDigest(state.AbsPath)
		if err != nil {
			return nil, err
		}
		next := state
		next.Exists = exists
		next.Digest = digest
		current = append(current, next)
	}
	return current, nil
}

func bulkPreviewAffectedStateForPath(affected noteops.BulkAffectedPath) (bulkPreviewAffectedState, error) {
	relPath := strings.TrimSpace(filepath.ToSlash(affected.Path.RelPath))
	absPath := strings.TrimSpace(affected.Path.AbsPath)
	if relPath == "" || absPath == "" {
		return bulkPreviewAffectedState{}, noteops.Errorf(noteops.CodeInternal, "bulk preview affected path is empty")
	}

	exists, digest, err := bulkPreviewFileDigest(absPath)
	if err != nil {
		return bulkPreviewAffectedState{}, err
	}
	if exists != affected.Exists {
		return bulkPreviewAffectedState{}, noteops.Errorf(noteops.CodePreviewStale, "bulk preview is stale")
	}
	return bulkPreviewAffectedState{
		Role:    strings.TrimSpace(affected.Role),
		AbsPath: filepath.Clean(absPath),
		RelPath: relPath,
		Exists:  exists,
		Digest:  digest,
	}, nil
}

func bulkPreviewFileDigest(absPath string) (bool, string, error) {
	info, err := os.Lstat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, "", nil
		}
		return false, "", noteops.WrapError(noteops.CodePreviewStale, "stat bulk preview path", err)
	}
	if !info.Mode().IsRegular() {
		return true, "", noteops.Errorf(noteops.CodePreviewStale, "bulk preview path is no longer a regular file")
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return true, "", noteops.WrapError(noteops.CodePreviewStale, "read bulk preview path", err)
	}
	sum := sha256.Sum256(data)
	return true, hex.EncodeToString(sum[:]), nil
}

func bulkPreviewDestinations(result *noteops.BulkPreviewResult) []bulkPreviewPlannedDestination {
	if result == nil {
		return nil
	}
	destinations := make([]bulkPreviewPlannedDestination, 0)
	for _, item := range result.Results {
		if strings.TrimSpace(item.Destination.RelPath) == "" {
			continue
		}
		destinations = append(destinations, bulkPreviewPlannedDestination{
			Key:     item.Key,
			AbsPath: filepath.Clean(item.Destination.AbsPath),
			RelPath: strings.TrimSpace(filepath.ToSlash(item.Destination.RelPath)),
		})
	}
	return destinations
}

func bulkPreviewTrashPlans(result *noteops.BulkPreviewResult) []noteops.TrashDestinationPlan {
	if result == nil {
		return nil
	}
	plans := make([]noteops.TrashDestinationPlan, 0)
	for _, item := range result.Results {
		if item.Trash == nil {
			continue
		}
		plan := *item.Trash
		plan.CollisionIDs = append([]string(nil), item.Trash.CollisionIDs...)
		plans = append(plans, plan)
	}
	return plans
}

func bulkPreviewRecordMatchesRequest(record bulkPreviewRecord, req bulkManagementRequest) error {
	current := req.fingerprint()
	if bulkPreviewRequestFieldsMatch(record.Fingerprint, current) {
		return nil
	}
	if bulkPreviewRequestSelectionAndFieldsMatch(record.Fingerprint, current) {
		return noteops.Errorf(noteops.CodePreviewStale, "bulk preview is stale")
	}
	return noteops.Errorf(noteops.CodePreviewRequired, "bulk preview does not match the request")
}

func bulkPreviewRequestFieldsMatch(a, b bulkManagementRequestFingerprint) bool {
	return reflect.DeepEqual(a, b)
}

func bulkPreviewRequestSelectionAndFieldsMatch(a, b bulkManagementRequestFingerprint) bool {
	if a.Action != b.Action ||
		!reflect.DeepEqual(a.SelectedKeys, b.SelectedKeys) ||
		a.MoveFolder != b.MoveFolder ||
		a.FrontmatterProperty != b.FrontmatterProperty ||
		a.FrontmatterOp != b.FrontmatterOp ||
		a.FrontmatterValue != b.FrontmatterValue ||
		a.FrontmatterType != b.FrontmatterType {
		return false
	}
	return true
}

func bulkPreviewStatesMatch(expected, current []bulkPreviewAffectedState) bool {
	return reflect.DeepEqual(expected, current)
}

func generateBulkPreviewID() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", noteops.WrapError(noteops.CodeInternal, "generate bulk preview id", err)
	}
	return hex.EncodeToString(buf[:]), nil
}
