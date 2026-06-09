package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	reviewStateVersion = 1
	reviewStateRelPath = ".bnotes/review.json"
)

type reviewStateAction string

const (
	reviewStateActionDone    reviewStateAction = "done"
	reviewStateActionSnooze  reviewStateAction = "snooze"
	reviewStateActionClear   reviewStateAction = "clear"
	reviewSnoozeTomorrow     string            = "tomorrow"
	reviewSnoozeNextWeek     string            = "week"
	reviewRecordStateDone    string            = "done"
	reviewRecordStateSnoozed string            = "snoozed"
)

type reviewStateStore struct {
	root string
	now  func() time.Time

	mu sync.Mutex
}

type reviewStateFile struct {
	Version int                          `json:"version"`
	Items   map[string]reviewStateRecord `json:"items"`
}

type reviewStateRecord struct {
	ID           string     `json:"id"`
	Signature    string     `json:"signature"`
	State        string     `json:"state"`
	SnoozedUntil *time.Time `json:"snoozed_until,omitempty"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

func newReviewStateStore(root string) *reviewStateStore {
	return &reviewStateStore{
		root: root,
		now:  time.Now,
	}
}

func (s *reviewStateStore) markDone(item reviewItem) error {
	return s.applyAction(item, reviewStateActionDone, "")
}

func (s *reviewStateStore) snooze(item reviewItem, snooze string) error {
	return s.applyAction(item, reviewStateActionSnooze, snooze)
}

func (s *reviewStateStore) clear(item reviewItem) error {
	return s.applyAction(item, reviewStateActionClear, "")
}

func (s *reviewStateStore) applyAction(item reviewItem, action reviewStateAction, snooze string) error {
	if err := validateReviewStateItem(item); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, _, err := s.loadLocked()
	if err != nil {
		return err
	}

	switch action {
	case reviewStateActionDone:
		state.Items[item.ID] = reviewStateRecord{
			ID:        item.ID,
			Signature: item.Signature,
			State:     reviewRecordStateDone,
			UpdatedAt: s.currentTime(),
		}
	case reviewStateActionSnooze:
		deadline, err := reviewSnoozeDeadline(s.currentTime(), snooze)
		if err != nil {
			return err
		}
		state.Items[item.ID] = reviewStateRecord{
			ID:           item.ID,
			Signature:    item.Signature,
			State:        reviewRecordStateSnoozed,
			SnoozedUntil: &deadline,
			UpdatedAt:    s.currentTime(),
		}
	case reviewStateActionClear:
		delete(state.Items, item.ID)
		for id, record := range state.Items {
			if record.Signature == item.Signature {
				delete(state.Items, id)
			}
		}
	default:
		return errorsf("unknown review state action %q", action)
	}

	return s.saveLocked(state)
}

func (s *reviewStateStore) filterQueue(queue reviewQueue) (reviewQueue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, loaded, err := s.loadLocked()
	if err != nil {
		return reviewQueue{}, err
	}
	if !loaded || len(state.Items) == 0 {
		queue.Counts.Active = len(queue.Items)
		queue.Counts.DoneHidden = 0
		queue.Counts.SnoozedHidden = 0
		queue.Counts.TotalGenerated = len(queue.Items)
		return queue, nil
	}

	generatedByID := make(map[string]reviewItem, len(queue.Items))
	for _, item := range queue.Items {
		if item.ID != "" {
			generatedByID[item.ID] = item
		}
	}

	now := s.currentTime()
	changed := false
	for id, record := range state.Items {
		item, ok := generatedByID[id]
		if !ok || record.Signature != item.Signature || !record.isActiveSuppression(now) {
			delete(state.Items, id)
			changed = true
		}
	}

	filtered := make([]reviewItem, 0, len(queue.Items))
	doneHidden := 0
	snoozedHidden := 0
	for _, item := range queue.Items {
		record, ok := state.Items[item.ID]
		if !ok {
			filtered = append(filtered, item)
			continue
		}
		switch record.State {
		case reviewRecordStateDone:
			doneHidden++
		case reviewRecordStateSnoozed:
			snoozedHidden++
		default:
			filtered = append(filtered, item)
		}
	}

	queue.Items = filtered
	queue.Counts.Active = len(filtered)
	queue.Counts.DoneHidden = doneHidden
	queue.Counts.SnoozedHidden = snoozedHidden
	queue.Counts.TotalGenerated = len(queue.Items) + doneHidden + snoozedHidden

	if changed {
		if err := s.saveLocked(state); err != nil {
			return reviewQueue{}, err
		}
	}
	return queue, nil
}

func (s *reviewStateStore) load() (reviewStateFile, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *reviewStateStore) loadLocked() (reviewStateFile, bool, error) {
	path, err := s.path()
	if err != nil {
		return reviewStateFile{}, false, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return newReviewStateFile(), false, nil
		}
		return reviewStateFile{}, false, fmt.Errorf("web: read review state %q: %w", path, err)
	}

	var state reviewStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return reviewStateFile{}, false, fmt.Errorf("web: parse review state %q: %w", path, err)
	}
	if state.Version == 0 && len(state.Items) == 0 {
		state.Version = reviewStateVersion
	}
	if state.Version != reviewStateVersion {
		return reviewStateFile{}, false, fmt.Errorf("web: unsupported review state version %d", state.Version)
	}
	if state.Items == nil {
		state.Items = map[string]reviewStateRecord{}
	}
	for id, record := range state.Items {
		if record.ID == "" {
			record.ID = id
			state.Items[id] = record
		}
	}
	return state, true, nil
}

func (s *reviewStateStore) saveLocked(state reviewStateFile) error {
	path, err := s.path()
	if err != nil {
		return err
	}
	if state.Version == 0 {
		state.Version = reviewStateVersion
	}
	if state.Items == nil {
		state.Items = map[string]reviewStateRecord{}
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("web: encode review state: %w", err)
	}
	data = append(data, '\n')
	return writeReviewStateAtomic(path, data, 0o644)
}

func newReviewStateFile() reviewStateFile {
	return reviewStateFile{
		Version: reviewStateVersion,
		Items:   map[string]reviewStateRecord{},
	}
}

func (s *reviewStateStore) path() (string, error) {
	if s == nil {
		return "", errorsf("nil review state store")
	}
	if strings.TrimSpace(s.root) == "" {
		return "", errorsf("empty review state root")
	}
	return filepath.Join(s.root, filepath.FromSlash(reviewStateRelPath)), nil
}

func (s *reviewStateStore) currentTime() time.Time {
	if s == nil || s.now == nil {
		return time.Now()
	}
	return s.now()
}

func (r reviewStateRecord) isActiveSuppression(now time.Time) bool {
	switch r.State {
	case reviewRecordStateDone:
		return strings.TrimSpace(r.Signature) != ""
	case reviewRecordStateSnoozed:
		return strings.TrimSpace(r.Signature) != "" && r.SnoozedUntil != nil && r.SnoozedUntil.After(now)
	default:
		return false
	}
}

func validateReviewStateItem(item reviewItem) error {
	if strings.TrimSpace(item.ID) == "" {
		return errorsf("review item id is required")
	}
	if strings.TrimSpace(item.Signature) == "" {
		return errorsf("review item signature is required")
	}
	return nil
}

func reviewSnoozeDeadline(now time.Time, snooze string) (time.Time, error) {
	days := 0
	switch strings.TrimSpace(snooze) {
	case reviewSnoozeTomorrow:
		days = 1
	case reviewSnoozeNextWeek:
		days = 7
	default:
		return time.Time{}, errorsf("unknown review snooze option %q", snooze)
	}
	target := now.AddDate(0, 0, days)
	year, month, day := target.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, target.Location()), nil
}

func writeReviewStateAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("web: mkdir review state dir %q: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".review-*")
	if err != nil {
		return fmt.Errorf("web: create review state temp in %q: %w", dir, err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("web: write review state temp %q: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("web: sync review state temp %q: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("web: close review state temp %q: %w", tmpPath, err)
	}
	if perm == 0 {
		perm = 0o644
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		cleanup()
		return fmt.Errorf("web: chmod review state temp %q: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("web: rename review state temp %q -> %q: %w", tmpPath, path, err)
	}
	return nil
}
