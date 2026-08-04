package brief

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
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

type briefStateFile struct {
	Version         int                          `json:"version"`
	DailySelections map[string]DailySelection    `json:"daily_selections"`
	NoteExposures   map[string]time.Time         `json:"note_exposures"`
	PairExposures   map[string]briefPairExposure `json:"pair_exposures"`
	Visits          map[string]time.Time         `json:"visits"`
	Opens           []briefOpenRecord            `json:"opens"`
}

type briefPairExposure struct {
	SourceRelPath    string    `json:"source_rel_path"`
	CandidateRelPath string    `json:"candidate_rel_path"`
	ExposedAt        time.Time `json:"exposed_at"`
}

type briefOpenRecord struct {
	CardID   CardID    `json:"card_id"`
	OpenedAt time.Time `json:"opened_at"`
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

// CardIDFor returns the deterministic identifier for one daily slot card.
func CardIDFor(date LocalDate, slot SlotKind, sourceRelPath, candidateRelPath string) (CardID, error) {
	date, err := normalizeLocalDate(date)
	if err != nil {
		return "", err
	}
	if !validSlotKind(slot) {
		return "", fmt.Errorf("brief: unknown slot %q", slot)
	}
	sourceRelPath, err = normalizeBriefPath(sourceRelPath)
	if err != nil {
		return "", fmt.Errorf("brief: source path: %w", err)
	}

	candidateRelPath = textnorm.NFC(filepath.ToSlash(candidateRelPath))
	switch slot {
	case SlotConnect:
		candidateRelPath, err = normalizeBriefPath(candidateRelPath)
		if err != nil {
			return "", fmt.Errorf("brief: candidate path: %w", err)
		}
		if sourceRelPath == candidateRelPath {
			return "", errors.New("brief: connect source and candidate paths must differ")
		}
	default:
		if candidateRelPath != "" {
			return "", fmt.Errorf("brief: slot %q must not include a candidate path", slot)
		}
	}

	sum := sha256.Sum256([]byte(string(date) + "\x00" + string(slot) + "\x00" + sourceRelPath + "\x00" + candidateRelPath))
	return CardID(hex.EncodeToString(sum[:])), nil
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

func normalizeDailySelection(selection DailySelection) (DailySelection, error) {
	date, err := normalizeLocalDate(selection.Date)
	if err != nil {
		return DailySelection{}, err
	}
	cards, err := normalizeCardsForDate(date, selection.Cards)
	if err != nil {
		return DailySelection{}, err
	}
	return DailySelection{
		Date:  date,
		Cards: cards,
	}, nil
}

func normalizeCardsForDate(date LocalDate, cards []Card) ([]Card, error) {
	normalized := make(map[SlotKind]Card, len(cards))
	for _, card := range cards {
		card, err := normalizeCardForDate(date, card)
		if err != nil {
			return nil, err
		}
		if _, exists := normalized[card.Slot]; exists {
			return nil, fmt.Errorf("brief: duplicate slot %q in one selection", card.Slot)
		}
		normalized[card.Slot] = card
	}
	out := make([]Card, 0, len(normalized))
	for _, slot := range SlotOrder {
		card, ok := normalized[slot]
		if ok {
			out = append(out, card)
		}
	}
	if len(out) != len(normalized) {
		return nil, errors.New("brief: selection contains unknown slots")
	}
	return out, nil
}

func normalizeCardForDate(date LocalDate, card Card) (Card, error) {
	if !validSlotKind(card.Slot) {
		return Card{}, fmt.Errorf("brief: unknown slot %q", card.Slot)
	}
	sourceRelPath, err := normalizeBriefPath(card.Source.RelPath)
	if err != nil {
		return Card{}, fmt.Errorf("brief: source path: %w", err)
	}
	card.Source.RelPath = sourceRelPath

	candidateRelPath := ""
	switch card.Slot {
	case SlotConnect:
		if card.Candidate == nil {
			return Card{}, errors.New("brief: connect card requires a candidate")
		}
		candidateRelPath, err = normalizeBriefPath(card.Candidate.RelPath)
		if err != nil {
			return Card{}, fmt.Errorf("brief: candidate path: %w", err)
		}
		if candidateRelPath == sourceRelPath {
			return Card{}, errors.New("brief: connect source and candidate paths must differ")
		}
		card.Candidate.RelPath = candidateRelPath
	default:
		if card.Candidate != nil {
			return Card{}, fmt.Errorf("brief: slot %q must not include a candidate", card.Slot)
		}
	}
	expectedID, err := CardIDFor(date, card.Slot, sourceRelPath, candidateRelPath)
	if err != nil {
		return Card{}, err
	}
	if card.ID == "" {
		return Card{}, errors.New("brief: missing card ID")
	}
	if card.ID != expectedID {
		return Card{}, fmt.Errorf(
			"brief: card %q ID %q does not match expected %q",
			card.Slot,
			card.ID,
			expectedID,
		)
	}
	if err := validateCardIDFormat(card.ID); err != nil {
		return Card{}, err
	}
	card.Metadata.Reasons = append([]string(nil), card.Metadata.Reasons...)
	return card, nil
}

func validSlotKind(slot SlotKind) bool {
	switch slot {
	case SlotContinue, SlotRediscover, SlotConnect:
		return true
	default:
		return false
	}
}

func normalizeLocalDate(value LocalDate) (LocalDate, error) {
	text := strings.TrimSpace(string(value))
	if text == "" {
		return "", errors.New("brief: empty local date")
	}
	parsed, err := time.Parse("2006-01-02", text)
	if err != nil {
		return "", fmt.Errorf("brief: invalid local date %q: %w", value, err)
	}
	normalized := parsed.Format("2006-01-02")
	if normalized != text {
		return "", fmt.Errorf("brief: invalid local date %q", value)
	}
	return LocalDate(normalized), nil
}

func normalizeBriefPair(sourceRelPath, candidateRelPath string) (string, string, string, error) {
	sourceRelPath, err := normalizeBriefPath(sourceRelPath)
	if err != nil {
		return "", "", "", fmt.Errorf("brief: source path: %w", err)
	}
	candidateRelPath, err = normalizeBriefPath(candidateRelPath)
	if err != nil {
		return "", "", "", fmt.Errorf("brief: candidate path: %w", err)
	}
	if sourceRelPath == candidateRelPath {
		return "", "", "", errors.New("brief: source and candidate paths must differ")
	}
	sum := sha256.Sum256([]byte(sourceRelPath + "\x00" + candidateRelPath))
	return sourceRelPath, candidateRelPath, hex.EncodeToString(sum[:]), nil
}

func normalizeBriefPath(value string) (string, error) {
	if value == "" {
		return "", errors.New("empty vault-relative path")
	}
	if strings.ContainsRune(value, '\x00') {
		return "", errors.New("vault-relative path contains NUL")
	}
	if strings.Contains(value, `\`) {
		return "", fmt.Errorf("vault-relative path %q must use forward slashes", value)
	}
	value = textnorm.NFC(filepath.ToSlash(value))
	if filepath.IsAbs(value) || path.IsAbs(value) || filepath.VolumeName(value) != "" || looksLikeWindowsDrivePath(value) {
		return "", fmt.Errorf("path %q must be vault-relative", value)
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("path %q escapes the vault root", value)
	}
	if cleaned != value {
		return "", fmt.Errorf("path %q is not a normalized vault-relative path", value)
	}
	return value, nil
}

func looksLikeWindowsDrivePath(value string) bool {
	if len(value) < 2 || value[1] != ':' {
		return false
	}
	first := value[0]
	return (first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z')
}

func validateCardIDFormat(id CardID) error {
	text := string(id)
	if len(text) != sha256.Size*2 {
		return fmt.Errorf("brief: malformed card ID %q", id)
	}
	for _, r := range text {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		default:
			return fmt.Errorf("brief: malformed card ID %q", id)
		}
	}
	return nil
}

func (store *FileStateStore) prepareWriteLocked(at time.Time) (briefStateFile, map[string]struct{}, error) {
	store.state = nil
	store.stateExists = false
	state, _, err := store.loadLocked()
	if err != nil {
		return briefStateFile{}, nil, err
	}
	paths, err := store.notePathsLocked(true)
	if err != nil {
		return briefStateFile{}, nil, err
	}
	state = cloneBriefState(state)
	pruneBriefState(&state, paths, at)
	return state, paths, nil
}

func pruneBriefState(state *briefStateFile, paths map[string]struct{}, at time.Time) {
	if state == nil {
		return
	}
	cutoff := at.Add(-HistoryRetention)
	for dateKey, selection := range state.DailySelections {
		date, err := normalizeLocalDate(LocalDate(dateKey))
		if err != nil {
			delete(state.DailySelections, dateKey)
			continue
		}
		dateTime, err := parseLocalDateIn(date, at.Location())
		if err != nil || dateTime.Before(startOfLocalDay(cutoff)) {
			delete(state.DailySelections, dateKey)
			continue
		}
		cards := make([]Card, 0, len(selection.Cards))
		for _, card := range selection.Cards {
			if !containsBriefPath(paths, card.Source.RelPath) {
				continue
			}
			if card.Candidate != nil && !containsBriefPath(paths, card.Candidate.RelPath) {
				continue
			}
			cards = append(cards, card)
		}
		if len(cards) == 0 {
			delete(state.DailySelections, dateKey)
			continue
		}
		selection.Cards = cards
		selection.Date = date
		state.DailySelections[dateKey] = selection
	}
	for relPath, ts := range state.NoteExposures {
		if !containsBriefPath(paths, relPath) || ts.Before(cutoff) {
			delete(state.NoteExposures, relPath)
		}
	}
	for pairID, record := range state.PairExposures {
		if !containsBriefPath(paths, record.SourceRelPath) ||
			!containsBriefPath(paths, record.CandidateRelPath) ||
			record.ExposedAt.Before(cutoff) {
			delete(state.PairExposures, pairID)
		}
	}
	for visitDate := range state.Visits {
		parsed, err := parseLocalDateIn(LocalDate(visitDate), at.Location())
		if err != nil || parsed.Before(startOfLocalDay(cutoff)) {
			delete(state.Visits, visitDate)
		}
	}
	prunedOpens := make([]briefOpenRecord, 0, len(state.Opens))
	for _, open := range state.Opens {
		if open.OpenedAt.Before(cutoff) {
			continue
		}
		prunedOpens = append(prunedOpens, open)
	}
	state.Opens = prunedOpens
}

func containsBriefPath(paths map[string]struct{}, wanted string) bool {
	_, ok := paths[wanted]
	return ok
}

func (store *FileStateStore) loadLocked() (briefStateFile, bool, error) {
	if store == nil {
		return briefStateFile{}, false, errors.New("brief: nil state store")
	}
	if store.state != nil {
		return *store.state, store.stateExists, nil
	}
	statePath, err := store.statePath()
	if err != nil {
		return briefStateFile{}, false, err
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			state := newBriefState()
			store.state = &state
			return state, false, nil
		}
		return briefStateFile{}, false, fmt.Errorf("brief: read state %q: %w", statePath, err)
	}

	var state briefStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return briefStateFile{}, false, fmt.Errorf("%w: parse %q: %v", ErrCorruptState, statePath, err)
	}
	if state.Version != briefStateVersion {
		return briefStateFile{}, false, fmt.Errorf(
			"%w: got %d want %d",
			ErrUnsupportedStateVersion,
			state.Version,
			briefStateVersion,
		)
	}
	if state.DailySelections == nil {
		state.DailySelections = map[string]DailySelection{}
	}
	if state.NoteExposures == nil {
		state.NoteExposures = map[string]time.Time{}
	}
	if state.PairExposures == nil {
		state.PairExposures = map[string]briefPairExposure{}
	}
	if state.Visits == nil {
		state.Visits = map[string]time.Time{}
	}
	if state.Opens == nil {
		state.Opens = []briefOpenRecord{}
	}
	if err := validateBriefState(state); err != nil {
		return briefStateFile{}, false, err
	}
	store.state = &state
	store.stateExists = true
	return state, true, nil
}

func validateBriefState(state briefStateFile) error {
	for dateKey, selection := range state.DailySelections {
		date, err := normalizeLocalDate(LocalDate(dateKey))
		if err != nil {
			return fmt.Errorf("%w: selection key %q: %v", ErrCorruptState, dateKey, err)
		}
		if selection.Date == "" {
			selection.Date = date
		}
		if selection.Date != date {
			return fmt.Errorf(
				"%w: selection %q date payload %q does not match its key",
				ErrCorruptState,
				date,
				selection.Date,
			)
		}
		cards, err := normalizeCardsForDate(date, selection.Cards)
		if err != nil {
			return fmt.Errorf("%w: selection %q: %v", ErrCorruptState, date, err)
		}
		selection.Cards = cards
	}
	for relPath, exposedAt := range state.NoteExposures {
		normalized, err := normalizeBriefPath(relPath)
		if err != nil {
			return fmt.Errorf("%w: note exposure %q: %v", ErrCorruptState, relPath, err)
		}
		if normalized != relPath {
			return fmt.Errorf("%w: note exposure %q is not normalized", ErrCorruptState, relPath)
		}
		if exposedAt.IsZero() {
			return fmt.Errorf("%w: note exposure %q has no timestamp", ErrCorruptState, relPath)
		}
	}
	for pairID, record := range state.PairExposures {
		normalizedSource, normalizedCandidate, expectedID, err := normalizeBriefPair(record.SourceRelPath, record.CandidateRelPath)
		if err != nil {
			return fmt.Errorf("%w: pair exposure %q: %v", ErrCorruptState, pairID, err)
		}
		if pairID != expectedID {
			return fmt.Errorf("%w: pair exposure %q does not match source/candidate", ErrCorruptState, pairID)
		}
		if normalizedSource != record.SourceRelPath || normalizedCandidate != record.CandidateRelPath {
			return fmt.Errorf("%w: pair exposure %q contains non-normalized paths", ErrCorruptState, pairID)
		}
		if record.ExposedAt.IsZero() {
			return fmt.Errorf("%w: pair exposure %q has no timestamp", ErrCorruptState, pairID)
		}
	}
	for visitDate, visitedAt := range state.Visits {
		normalized, err := normalizeLocalDate(LocalDate(visitDate))
		if err != nil {
			return fmt.Errorf("%w: visit date %q: %v", ErrCorruptState, visitDate, err)
		}
		if LocalDate(visitDate) != normalized {
			return fmt.Errorf("%w: visit date %q is not normalized", ErrCorruptState, visitDate)
		}
		if visitedAt.IsZero() {
			return fmt.Errorf("%w: visit date %q has no timestamp", ErrCorruptState, visitDate)
		}
	}
	for i, open := range state.Opens {
		if err := validateCardIDFormat(open.CardID); err != nil {
			return fmt.Errorf("%w: open[%d]: %v", ErrCorruptState, i, err)
		}
		if open.OpenedAt.IsZero() {
			return fmt.Errorf("%w: open[%d] has no timestamp", ErrCorruptState, i)
		}
	}
	return nil
}

func newBriefState() briefStateFile {
	return briefStateFile{
		Version:         briefStateVersion,
		DailySelections: map[string]DailySelection{},
		NoteExposures:   map[string]time.Time{},
		PairExposures:   map[string]briefPairExposure{},
		Visits:          map[string]time.Time{},
		Opens:           []briefOpenRecord{},
	}
}

func cloneBriefState(state briefStateFile) briefStateFile {
	clone := briefStateFile{
		Version:         state.Version,
		DailySelections: make(map[string]DailySelection, len(state.DailySelections)),
		NoteExposures:   make(map[string]time.Time, len(state.NoteExposures)),
		PairExposures:   make(map[string]briefPairExposure, len(state.PairExposures)),
		Visits:          make(map[string]time.Time, len(state.Visits)),
		Opens:           make([]briefOpenRecord, len(state.Opens)),
	}
	for date, selection := range state.DailySelections {
		clone.DailySelections[date] = cloneDailySelection(selection)
	}
	for relPath, exposedAt := range state.NoteExposures {
		clone.NoteExposures[relPath] = exposedAt
	}
	for pairID, record := range state.PairExposures {
		clone.PairExposures[pairID] = record
	}
	for date, visitedAt := range state.Visits {
		clone.Visits[date] = visitedAt
	}
	copy(clone.Opens, state.Opens)
	return clone
}

func cloneDailySelection(selection DailySelection) DailySelection {
	cloned := DailySelection{
		Date:  selection.Date,
		Cards: make([]Card, len(selection.Cards)),
	}
	for i, card := range selection.Cards {
		cloned.Cards[i] = cloneCard(card)
	}
	return cloned
}

func cloneCard(card Card) Card {
	cloned := card
	cloned.Metadata.Reasons = append([]string(nil), card.Metadata.Reasons...)
	if card.Candidate != nil {
		candidate := *card.Candidate
		cloned.Candidate = &candidate
	}
	return cloned
}

func (store *FileStateStore) saveLocked(state briefStateFile) error {
	state.Version = briefStateVersion
	statePath, err := store.statePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("brief: encode state: %w", err)
	}
	data = append(data, '\n')
	if err := writeBriefStateAtomic(statePath, data, 0o644); err != nil {
		return err
	}
	snapshot := cloneBriefState(state)
	store.state = &snapshot
	store.stateExists = true
	return nil
}

func (store *FileStateStore) notePathsLocked(refresh bool) (map[string]struct{}, error) {
	if !refresh && store.notePaths != nil {
		return store.notePaths, nil
	}
	root, err := store.rootPath()
	if err != nil {
		return nil, err
	}
	paths := make(map[string]struct{})
	err = filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filePath == root {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || strings.HasPrefix(entry.Name(), ".") || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return nil
		}
		relative, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		normalized, err := normalizeBriefPath(textnorm.NFC(filepath.ToSlash(relative)))
		if err != nil {
			return fmt.Errorf("brief: inspect note path %q: %w", relative, err)
		}
		paths[normalized] = struct{}{}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("brief: scan vault notes: %w", err)
	}
	store.notePaths = paths
	return paths, nil
}

func (store *FileStateStore) statePath() (string, error) {
	root, err := store.rootPath()
	if err != nil {
		return "", err
	}
	directory := filepath.Join(root, ".bnotes")
	if info, err := os.Lstat(directory); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("brief: state directory %q must be a real directory", directory)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("brief: inspect state directory %q: %w", directory, err)
	}
	statePath := filepath.Join(root, filepath.FromSlash(briefStateRelPath))
	if info, err := os.Lstat(statePath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", fmt.Errorf("brief: state path %q must be a regular file", statePath)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("brief: inspect state path %q: %w", statePath, err)
	}
	return statePath, nil
}

func (store *FileStateStore) rootPath() (string, error) {
	if store == nil {
		return "", errors.New("brief: nil state store")
	}
	if strings.TrimSpace(store.root) == "" {
		return "", errors.New("brief: empty state root")
	}
	root, err := filepath.Abs(store.root)
	if err != nil {
		return "", fmt.Errorf("brief: resolve state root %q: %w", store.root, err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("brief: resolve state root %q: %w", store.root, err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("brief: stat state root %q: %w", root, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("brief: state root %q is not a directory", root)
	}
	return root, nil
}

func (store *FileStateStore) currentTime() time.Time {
	if store == nil || store.now == nil {
		return time.Now()
	}
	return store.now()
}

func writeBriefStateAtomic(statePath string, data []byte, permissions os.FileMode) error {
	directory := filepath.Dir(statePath)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("brief: mkdir state directory %q: %w", directory, err)
	}
	resolvedDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return fmt.Errorf("brief: resolve state directory %q: %w", directory, err)
	}
	if filepath.Clean(resolvedDirectory) != filepath.Clean(directory) {
		return fmt.Errorf("brief: state directory %q must not resolve through a symlink", directory)
	}

	temporary, err := os.CreateTemp(directory, ".brief-*")
	if err != nil {
		return fmt.Errorf("brief: create state temp in %q: %w", directory, err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() { _ = os.Remove(temporaryPath) }
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		cleanup()
		return fmt.Errorf("brief: write state temp %q: %w", temporaryPath, err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		cleanup()
		return fmt.Errorf("brief: sync state temp %q: %w", temporaryPath, err)
	}
	if err := temporary.Close(); err != nil {
		cleanup()
		return fmt.Errorf("brief: close state temp %q: %w", temporaryPath, err)
	}
	if permissions == 0 {
		permissions = 0o644
	}
	if err := os.Chmod(temporaryPath, permissions); err != nil {
		cleanup()
		return fmt.Errorf("brief: chmod state temp %q: %w", temporaryPath, err)
	}
	if err := os.Rename(temporaryPath, statePath); err != nil {
		cleanup()
		return fmt.Errorf("brief: rename state temp %q -> %q: %w", temporaryPath, statePath, err)
	}
	return nil
}

func parseLocalDateIn(date LocalDate, location *time.Location) (time.Time, error) {
	date, err := normalizeLocalDate(date)
	if err != nil {
		return time.Time{}, err
	}
	if location == nil {
		location = time.Local
	}
	parsed, err := time.ParseInLocation("2006-01-02", string(date), location)
	if err != nil {
		return time.Time{}, fmt.Errorf("brief: parse local date %q: %w", date, err)
	}
	return parsed, nil
}

func rollingActivityForState(state briefStateFile, at time.Time) (RollingActivity, error) {
	summary7, err := activitySummaryForWindow(state, at, 7)
	if err != nil {
		return RollingActivity{}, err
	}
	summary14, err := activitySummaryForWindow(state, at, 14)
	if err != nil {
		return RollingActivity{}, err
	}
	return RollingActivity{
		Last7Days:  summary7,
		Last14Days: summary14,
	}, nil
}

func activitySummaryForWindow(state briefStateFile, at time.Time, windowDays int) (ActivitySummary, error) {
	today := startOfLocalDay(at)
	visitDaySet := make(map[string]struct{}, len(state.Visits))
	openCount := 0
	for visitDate := range state.Visits {
		day, err := parseLocalDateIn(LocalDate(visitDate), at.Location())
		if err != nil {
			return ActivitySummary{}, err
		}
		if dayWithinWindow(day, today, windowDays) {
			visitDaySet[visitDate] = struct{}{}
		}
	}
	for _, open := range state.Opens {
		day := startOfLocalDay(open.OpenedAt.In(at.Location()))
		if dayWithinWindow(day, today, windowDays) {
			openCount++
		}
	}
	return ActivitySummary{
		WindowDays: windowDays,
		VisitDays:  len(visitDaySet),
		OpenCount:  openCount,
	}, nil
}

func dayWithinWindow(day, today time.Time, windowDays int) bool {
	if windowDays <= 0 {
		return false
	}
	if day.After(today) {
		return false
	}
	delta := int(today.Sub(day).Hours() / 24)
	return delta >= 0 && delta < windowDays
}

func startOfLocalDay(ts time.Time) time.Time {
	location := ts.Location()
	if location == nil {
		location = time.Local
	}
	local := ts.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
}
