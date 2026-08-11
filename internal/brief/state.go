package brief

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	briefStateVersion = 1
	briefStateRelPath = ".bnotes/brief.json"
)

var (
	// ErrCorruptState identifies state that cannot be decoded or does not
	// satisfy the versioned brief-state contract.
	ErrCorruptState = errors.New("brief: corrupt state")
	// ErrUnsupportedStateVersion identifies a readable state file written with a
	// schema version this binary does not understand.
	ErrUnsupportedStateVersion = errors.New("brief: unsupported state version")
)

// StateStoreOption configures a FileStateStore.
type StateStoreOption func(*FileStateStore)

// WithClock injects the clock used for write-time pruning and defaulted
// timestamps in deterministic tests.
func WithClock(now func() time.Time) StateStoreOption {
	return func(store *FileStateStore) {
		store.now = now
	}
}

// FileStateStore persists Daily Brief state for one vault in .bnotes/brief.json.
type FileStateStore struct {
	root string
	now  func() time.Time

	mu          sync.Mutex
	state       *briefStateFile
	stateExists bool
	notePaths   map[string]struct{}
}

var _ StateStore = (*FileStateStore)(nil)

// NewFileStateStore returns a state store rooted at root. The .bnotes
// directory and brief.json are created lazily on the first explicit write.
func NewFileStateStore(root string, options ...StateStoreOption) *FileStateStore {
	store := &FileStateStore{root: root, now: time.Now}
	for _, option := range options {
		if option != nil {
			option(store)
		}
	}
	return store
}

// Validate loads and validates the current state without mutating it.
func (store *FileStateStore) Validate() error {
	if store == nil {
		return errors.New("brief: nil state store")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	_, _, err := store.loadLocked()
	return err
}

// ReadDailySelection returns the persisted selection for one local date.
func (store *FileStateStore) ReadDailySelection(date LocalDate) (DailySelection, bool, error) {
	if store == nil {
		return DailySelection{}, false, errors.New("brief: nil state store")
	}
	date, err := normalizeLocalDate(date)
	if err != nil {
		return DailySelection{}, false, err
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	state, _, err := store.loadLocked()
	if err != nil {
		return DailySelection{}, false, err
	}
	selection, ok := state.DailySelections[string(date)]
	if !ok {
		return DailySelection{}, false, nil
	}
	return cloneDailySelection(selection), true, nil
}

// SaveDailySelection atomically persists one local-date selection.
func (store *FileStateStore) SaveDailySelection(selection DailySelection) error {
	if store == nil {
		return errors.New("brief: nil state store")
	}
	now := store.currentTime()
	return store.saveDailySelectionAt(selection, now)
}

func (store *FileStateStore) saveDailySelectionAt(selection DailySelection, at time.Time) error {
	selection, err := normalizeDailySelection(selection)
	if err != nil {
		return err
	}
	if at.IsZero() {
		return errors.New("brief: save timestamp must not be zero")
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	state, _, err := store.prepareWriteLocked(at)
	if err != nil {
		return err
	}
	state.DailySelections[string(selection.Date)] = cloneDailySelection(selection)
	recordExposures(state, selection.Cards, at)
	return store.saveLocked(state)
}

// ReplaceDailySelection atomically updates one or more slots for date while
// preserving untouched slots.
func (store *FileStateStore) ReplaceDailySelection(date LocalDate, cards []Card) (DailySelection, error) {
	if store == nil {
		return DailySelection{}, errors.New("brief: nil state store")
	}
	date, err := normalizeLocalDate(date)
	if err != nil {
		return DailySelection{}, err
	}
	replacements, err := normalizeCardsForDate(date, cards)
	if err != nil {
		return DailySelection{}, err
	}
	if len(replacements) == 0 {
		return DailySelection{}, errors.New("brief: replacement requires at least one card")
	}
	now := store.currentTime()
	if now.IsZero() {
		return DailySelection{}, errors.New("brief: replacement timestamp must not be zero")
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	state, _, err := store.prepareWriteLocked(now)
	if err != nil {
		return DailySelection{}, err
	}
	selection, ok := state.DailySelections[string(date)]
	if !ok {
		return DailySelection{}, fmt.Errorf("brief: no daily selection for %s", date)
	}
	normalizedExisting, err := normalizeCardsForDate(date, selection.Cards)
	if err != nil {
		return DailySelection{}, err
	}
	bySlot := make(map[SlotKind]Card, len(SlotOrder))
	for _, card := range normalizedExisting {
		bySlot[card.Slot] = card
	}
	for _, card := range replacements {
		bySlot[card.Slot] = card
	}
	updatedCards := make([]Card, 0, len(bySlot))
	for _, slot := range SlotOrder {
		if card, ok := bySlot[slot]; ok {
			updatedCards = append(updatedCards, card)
		}
	}
	updatedSelection, err := normalizeDailySelection(DailySelection{Date: date, Cards: updatedCards})
	if err != nil {
		return DailySelection{}, err
	}
	state.DailySelections[string(date)] = cloneDailySelection(updatedSelection)
	recordExposures(state, replacements, now)
	if err := store.saveLocked(state); err != nil {
		return DailySelection{}, err
	}
	return cloneDailySelection(updatedSelection), nil
}

// RecordVisit idempotently records that the brief was displayed for one date.
func (store *FileStateStore) RecordVisit(date LocalDate, at time.Time) (RollingActivity, error) {
	if store == nil {
		return RollingActivity{}, errors.New("brief: nil state store")
	}
	date, err := normalizeLocalDate(date)
	if err != nil {
		return RollingActivity{}, err
	}
	if at.IsZero() {
		return RollingActivity{}, errors.New("brief: visit timestamp must not be zero")
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	state, _, err := store.prepareWriteLocked(at)
	if err != nil {
		return RollingActivity{}, err
	}
	if _, exists := state.Visits[string(date)]; !exists {
		state.Visits[string(date)] = at
		if err := store.saveLocked(state); err != nil {
			return RollingActivity{}, err
		}
	}
	return rollingActivityForState(state, at)
}

// RecordOpen records one successful card-open event.
func (store *FileStateStore) RecordOpen(cardID CardID, at time.Time) (RollingActivity, error) {
	if store == nil {
		return RollingActivity{}, errors.New("brief: nil state store")
	}
	if err := validateCardIDFormat(cardID); err != nil {
		return RollingActivity{}, err
	}
	if at.IsZero() {
		return RollingActivity{}, errors.New("brief: open timestamp must not be zero")
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	state, _, err := store.prepareWriteLocked(at)
	if err != nil {
		return RollingActivity{}, err
	}
	state.Opens = append(state.Opens, briefOpenRecord{CardID: cardID, OpenedAt: at})
	if err := store.saveLocked(state); err != nil {
		return RollingActivity{}, err
	}
	return rollingActivityForState(state, at)
}

// LastNoteExposure reports when noteRelPath was most recently exposed.
func (store *FileStateStore) LastNoteExposure(noteRelPath string) (time.Time, bool, error) {
	if store == nil {
		return time.Time{}, false, errors.New("brief: nil state store")
	}
	noteRelPath, err := normalizeBriefPath(noteRelPath)
	if err != nil {
		return time.Time{}, false, err
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	state, _, err := store.loadLocked()
	if err != nil {
		return time.Time{}, false, err
	}
	last, ok := state.NoteExposures[noteRelPath]
	if !ok {
		return time.Time{}, false, nil
	}
	return last, true, nil
}

// LastNoteOpen reports when noteRelPath was most recently opened from a brief card.
func (store *FileStateStore) LastNoteOpen(noteRelPath string) (time.Time, bool, error) {
	if store == nil {
		return time.Time{}, false, errors.New("brief: nil state store")
	}
	noteRelPath, err := normalizeBriefPath(noteRelPath)
	if err != nil {
		return time.Time{}, false, err
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	state, _, err := store.loadLocked()
	if err != nil {
		return time.Time{}, false, err
	}
	cardTargets := make(map[CardID]string)
	for _, selection := range state.DailySelections {
		for _, card := range selection.Cards {
			target, ok := openTargetRelPath(card)
			if !ok {
				continue
			}
			cardTargets[card.ID] = target
		}
	}
	var last time.Time
	found := false
	for _, open := range state.Opens {
		target, ok := cardTargets[open.CardID]
		if !ok || target != noteRelPath {
			continue
		}
		if !found || open.OpenedAt.After(last) {
			last = open.OpenedAt
			found = true
		}
	}
	if !found {
		return time.Time{}, false, nil
	}
	return last, true, nil
}

func openTargetRelPath(card Card) (string, bool) {
	switch card.Slot {
	case SlotConnect:
		if card.Candidate == nil || card.Candidate.RelPath == "" {
			return "", false
		}
		return card.Candidate.RelPath, true
	default:
		if card.Source.RelPath == "" {
			return "", false
		}
		return card.Source.RelPath, true
	}
}

// LastPairExposure reports when a source/candidate pair was most recently exposed.
func (store *FileStateStore) LastPairExposure(sourceRelPath, candidateRelPath string) (time.Time, bool, error) {
	if store == nil {
		return time.Time{}, false, errors.New("brief: nil state store")
	}
	sourceRelPath, candidateRelPath, pairID, err := normalizeBriefPair(sourceRelPath, candidateRelPath)
	if err != nil {
		return time.Time{}, false, err
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	state, _, err := store.loadLocked()
	if err != nil {
		return time.Time{}, false, err
	}
	record, ok := state.PairExposures[pairID]
	if !ok {
		return time.Time{}, false, nil
	}
	if record.SourceRelPath != sourceRelPath || record.CandidateRelPath != candidateRelPath {
		return time.Time{}, false, fmt.Errorf("%w: pair %q payload does not match its key", ErrCorruptState, pairID)
	}
	return record.ExposedAt, true, nil
}

// RollingActivity returns rolling seven-day and fourteen-day local aggregates.
func (store *FileStateStore) RollingActivity(at time.Time) (RollingActivity, error) {
	if store == nil {
		return RollingActivity{}, errors.New("brief: nil state store")
	}
	if at.IsZero() {
		return RollingActivity{}, errors.New("brief: activity timestamp must not be zero")
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	state, _, err := store.loadLocked()
	if err != nil {
		return RollingActivity{}, err
	}
	return rollingActivityForState(state, at)
}

func recordExposures(state briefStateFile, cards []Card, at time.Time) {
	for _, card := range cards {
		switch card.Slot {
		case SlotRediscover:
			state.NoteExposures[card.Source.RelPath] = at
		case SlotConnect:
			if card.Candidate == nil {
				continue
			}
			_, _, pairID, err := normalizeBriefPair(card.Source.RelPath, card.Candidate.RelPath)
			if err != nil {
				continue
			}
			state.PairExposures[pairID] = briefPairExposure{
				SourceRelPath:    card.Source.RelPath,
				CandidateRelPath: card.Candidate.RelPath,
				ExposedAt:        at,
			}
		}
	}
}
