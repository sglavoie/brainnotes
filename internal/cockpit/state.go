package cockpit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultstate"
)

const (
	// stateVersion is the on-disk schema version for .bnotes/cockpit.json.
	stateVersion = 1
	// stateRelPath is the vault-relative cockpit workflow-state file.
	stateRelPath = ".bnotes/cockpit.json"
	// reviewStateRelPath is the legacy Daily Review state file. Compatible
	// records are migrated from it on first read; the file itself is never
	// deleted so the old web review view keeps working during the transition.
	reviewStateRelPath = ".bnotes/review.json"
)

// Workflow states a task can be suppressed into.
const (
	stateDone    = "done"
	stateSnoozed = "snoozed"
)

// Snooze options accepted by Snooze. They mirror the Daily Review vocabulary so
// migrated records keep the same meaning.
const (
	SnoozeTomorrow = "tomorrow"
	SnoozeNextWeek = "week"
)

type stateAction string

const (
	actionDone   stateAction = "done"
	actionSnooze stateAction = "snooze"
	actionClear  stateAction = "clear"
)

// StateStore persists per-task workflow state (done / snoozed) for a single
// vault under .bnotes/cockpit.json. It mirrors the Daily Review state store's
// behavior — atomic writes, stale-signature pruning, snooze expiry — so the
// cockpit and the legacy review view stay consistent, and it migrates readable
// records out of .bnotes/review.json the first time it loads.
type StateStore struct {
	root string
	now  func() time.Time

	mu sync.Mutex
}

type stateFile struct {
	Version int                    `json:"version"`
	Items   map[string]stateRecord `json:"items"`
}

type stateRecord struct {
	ID           string     `json:"id"`
	Signature    string     `json:"signature"`
	State        string     `json:"state"`
	SnoozedUntil *time.Time `json:"snoozed_until,omitempty"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// stateCleanupError reports that queue filtering completed from readable state,
// but the best-effort write that removes stale records did not complete. Callers
// can still use the returned Queue: only the on-disk cleanup is in doubt.
type stateCleanupError struct {
	err error
}

func (e *stateCleanupError) Error() string {
	if e == nil || e.err == nil {
		return "cockpit: save pruned state"
	}
	return fmt.Sprintf("cockpit: save pruned state: %v", e.err)
}

func (e *stateCleanupError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// IsStateCleanupError reports whether err means FilterQueue produced a valid
// filtered queue but could not persist its removal of stale workflow records.
func IsStateCleanupError(err error) bool {
	var cleanup *stateCleanupError
	return errors.As(err, &cleanup)
}

// NewStateStore returns a store rooted at the vault root. The state file lives
// under root/.bnotes/cockpit.json and is created lazily on the first write.
func NewStateStore(root string) *StateStore {
	return &StateStore{
		root: root,
		now:  time.Now,
	}
}

// MarkDone suppresses a task as completed until its underlying content changes.
func (s *StateStore) MarkDone(task Task) error {
	return s.applyAction(task, actionDone, "")
}

// Snooze suppresses a task until the start of the day named by option
// (SnoozeTomorrow or SnoozeNextWeek).
func (s *StateStore) Snooze(task Task, option string) error {
	return s.applyAction(task, actionSnooze, option)
}

// Clear removes any stored state for a task (and any stale record that shares
// its signature) so it returns to the active queue.
func (s *StateStore) Clear(task Task) error {
	return s.applyAction(task, actionClear, "")
}

func (s *StateStore) applyAction(task Task, action stateAction, snooze string) error {
	if err := validateStateTask(task); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, _, err := s.loadLocked()
	if err != nil {
		return err
	}

	switch action {
	case actionDone:
		state.Items[task.ID] = stateRecord{
			ID:        task.ID,
			Signature: task.Signature,
			State:     stateDone,
			UpdatedAt: s.currentTime(),
		}
	case actionSnooze:
		deadline, err := snoozeDeadline(s.currentTime(), snooze)
		if err != nil {
			return err
		}
		state.Items[task.ID] = stateRecord{
			ID:           task.ID,
			Signature:    task.Signature,
			State:        stateSnoozed,
			SnoozedUntil: &deadline,
			UpdatedAt:    s.currentTime(),
		}
	case actionClear:
		delete(state.Items, task.ID)
		for id, record := range state.Items {
			if record.Signature == task.Signature {
				delete(state.Items, id)
			}
		}
	default:
		return fmt.Errorf("cockpit: unknown state action %q", action)
	}

	return s.saveLocked(state)
}

// FilterQueue hides done and snoozed tasks from the queue and reports how many
// were suppressed. It prunes records that no longer match a generated task
// (removed, content changed, or snooze expired), persisting the pruned state so
// stale entries do not accumulate.
func (s *StateStore) FilterQueue(queue Queue) (Queue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, loaded, err := s.loadLocked()
	if err != nil {
		return Queue{}, err
	}
	if !loaded || len(state.Items) == 0 {
		queue.Counts.Active = len(queue.Tasks)
		queue.Counts.DoneHidden = 0
		queue.Counts.SnoozedHidden = 0
		queue.Counts.TotalGenerated = len(queue.Tasks)
		return queue, nil
	}

	generatedByID := make(map[string]Task, len(queue.Tasks))
	for _, task := range queue.Tasks {
		if task.ID != "" {
			generatedByID[task.ID] = task
		}
	}

	now := s.currentTime()
	changed := false
	for id, record := range state.Items {
		task, ok := generatedByID[id]
		if !ok || record.Signature != task.Signature || !record.isActiveSuppression(now) {
			delete(state.Items, id)
			changed = true
		}
	}

	filtered := make([]Task, 0, len(queue.Tasks))
	doneHidden := 0
	snoozedHidden := 0
	for _, task := range queue.Tasks {
		record, ok := state.Items[task.ID]
		if !ok {
			filtered = append(filtered, task)
			continue
		}
		switch record.State {
		case stateDone:
			doneHidden++
		case stateSnoozed:
			snoozedHidden++
		default:
			filtered = append(filtered, task)
		}
	}

	queue.Tasks = filtered
	queue.Counts.Active = len(filtered)
	queue.Counts.DoneHidden = doneHidden
	queue.Counts.SnoozedHidden = snoozedHidden
	queue.Counts.TotalGenerated = len(filtered) + doneHidden + snoozedHidden

	if changed {
		if err := s.saveLocked(state); err != nil {
			return queue, &stateCleanupError{err: err}
		}
	}
	return queue, nil
}

// CountScoped classifies the given tasks by their persisted workflow state and
// returns the resulting Counts (Active/DoneHidden/SnoozedHidden/TotalGenerated).
// It is read-only: it loads state without persisting and, unlike FilterQueue,
// never prunes, so callers must run it after FilterQueue has already reconciled
// and pruned the store over the full queue. Each task is counted as done or
// snoozed only when a stored record matches its id and signature and is still an
// active suppression (mirroring FilterQueue); every other task counts as active.
// Callers pass whatever subset they want counted (e.g. tasks under a folder), so
// this stays decoupled from how membership is decided.
func (s *StateStore) CountScoped(tasks []Task) (Counts, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, loaded, err := s.loadLocked()
	if err != nil {
		return Counts{}, err
	}

	now := s.currentTime()
	active, done, snoozed := 0, 0, 0
	for _, task := range tasks {
		if loaded {
			if record, ok := state.Items[task.ID]; ok && record.Signature == task.Signature && record.isActiveSuppression(now) {
				switch record.State {
				case stateDone:
					done++
					continue
				case stateSnoozed:
					snoozed++
					continue
				}
			}
		}
		active++
	}

	return Counts{
		Active:         active,
		DoneHidden:     done,
		SnoozedHidden:  snoozed,
		TotalGenerated: active + done + snoozed,
	}, nil
}

// load exposes the current state for tests and callers that need to inspect
// persisted records. The bool reports whether any state (from cockpit.json or a
// migration) is present.
func (s *StateStore) load() (stateFile, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *StateStore) loadLocked() (stateFile, bool, error) {
	path, err := s.statePath()
	if err != nil {
		return stateFile{}, false, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			migrated := s.migrateFromReviewLocked()
			return migrated, len(migrated.Items) > 0, nil
		}
		return stateFile{}, false, fmt.Errorf("cockpit: read state %q: %w", path, err)
	}

	var state stateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return stateFile{}, false, fmt.Errorf("cockpit: parse state %q: %w", path, err)
	}
	if state.Version == 0 && len(state.Items) == 0 {
		state.Version = stateVersion
	}
	if state.Version != stateVersion {
		return stateFile{}, false, fmt.Errorf("cockpit: unsupported state version %d", state.Version)
	}
	if state.Items == nil {
		state.Items = map[string]stateRecord{}
	}
	for id, record := range state.Items {
		if record.ID == "" {
			record.ID = id
			state.Items[id] = record
		}
	}
	return state, true, nil
}

// migrateFromReviewLocked seeds a fresh state file from readable, compatible
// records in the legacy .bnotes/review.json. It is best-effort: a missing,
// unreadable, or corrupt review file yields an empty state rather than an error,
// so a broken legacy file can never block the cockpit. The review file is left
// on disk untouched. Records that survive here are still subject to
// stale-signature pruning on the next FilterQueue.
func (s *StateStore) migrateFromReviewLocked() stateFile {
	out := newStateFile()

	reviewPath := filepath.Join(s.root, filepath.FromSlash(reviewStateRelPath))
	data, err := os.ReadFile(reviewPath)
	if err != nil {
		return out
	}
	var review stateFile
	if err := json.Unmarshal(data, &review); err != nil {
		return out
	}
	for id, record := range review.Items {
		if record.ID == "" {
			record.ID = id
		}
		if !migratableRecord(record) {
			continue
		}
		out.Items[record.ID] = record
	}
	return out
}

func (s *StateStore) saveLocked(state stateFile) error {
	path, err := s.statePath()
	if err != nil {
		return err
	}
	if state.Version == 0 {
		state.Version = stateVersion
	}
	if state.Items == nil {
		state.Items = map[string]stateRecord{}
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("cockpit: encode state: %w", err)
	}
	data = append(data, '\n')
	return writeStateAtomic(s.root, path, data, 0o644)
}

func newStateFile() stateFile {
	return stateFile{
		Version: stateVersion,
		Items:   map[string]stateRecord{},
	}
}

func (s *StateStore) statePath() (string, error) {
	if s == nil {
		return "", fmt.Errorf("cockpit: nil state store")
	}
	if strings.TrimSpace(s.root) == "" {
		return "", fmt.Errorf("cockpit: empty state root")
	}
	return filepath.Join(s.root, filepath.FromSlash(stateRelPath)), nil
}

func (s *StateStore) currentTime() time.Time {
	if s == nil || s.now == nil {
		return time.Now()
	}
	return s.now()
}

func (r stateRecord) isActiveSuppression(now time.Time) bool {
	switch r.State {
	case stateDone:
		return strings.TrimSpace(r.Signature) != ""
	case stateSnoozed:
		return strings.TrimSpace(r.Signature) != "" && r.SnoozedUntil != nil && r.SnoozedUntil.After(now)
	default:
		return false
	}
}

// migratableRecord reports whether a legacy review record is complete enough to
// carry into cockpit state: it needs an id, a signature, and a recognized
// state, plus a deadline when snoozed.
func migratableRecord(record stateRecord) bool {
	if strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.Signature) == "" {
		return false
	}
	switch record.State {
	case stateDone:
		return true
	case stateSnoozed:
		return record.SnoozedUntil != nil
	default:
		return false
	}
}

func validateStateTask(task Task) error {
	if strings.TrimSpace(task.ID) == "" {
		return fmt.Errorf("cockpit: task id is required")
	}
	if strings.TrimSpace(task.Signature) == "" {
		return fmt.Errorf("cockpit: task signature is required")
	}
	return nil
}

// snoozeDeadline resolves a snooze option to midnight at the start of the target
// day, in the reference time's location.
func snoozeDeadline(now time.Time, option string) (time.Time, error) {
	days := 0
	switch strings.TrimSpace(option) {
	case SnoozeTomorrow:
		days = 1
	case SnoozeNextWeek:
		days = 7
	default:
		return time.Time{}, fmt.Errorf("cockpit: unknown snooze option %q", option)
	}
	target := now.AddDate(0, 0, days)
	year, month, day := target.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, target.Location()), nil
}

// writeStateAtomic hands the state file to the shared durable-state writer,
// which owns the location refusal, the path-continuous replacement, and the
// exact-path snapshot. The cockpit queue is computed from the vault on every
// load, so it still renders under a refused root; what it cannot do is remember
// a snooze or a dismissal there.
func writeStateAtomic(vaultRoot, path string, data []byte, perm os.FileMode) error {
	return vaultstate.Save(vaultRoot, "cockpit-state", path, data, perm)
}
