package recall

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
)

const (
	recallStateVersion = 1
	recallStateRelPath = ".bnotes/recall.json"
)

var (
	// ErrCorruptState identifies state that cannot be decoded or whose records
	// violate the versioned recall-state contract.
	ErrCorruptState = errors.New("recall: corrupt state")
	// ErrUnsupportedStateVersion identifies a readable state file written with
	// a schema version this binary does not understand.
	ErrUnsupportedStateVersion = errors.New("recall: unsupported state version")
)

// FeedbackState is an explicit disposition for a source/candidate pair.
type FeedbackState string

const (
	FeedbackUseful    FeedbackState = "useful"
	FeedbackDismissed FeedbackState = "dismissed"
	FeedbackLater     FeedbackState = "later"
	FeedbackClear     FeedbackState = "clear"
)

// ActiveFeedback is one active explicit disposition for a candidate note.
// Until is set only when State is FeedbackLater.
type ActiveFeedback struct {
	CandidateRelPath string
	State            FeedbackState
	Until            time.Time
}

// Valid reports whether state is accepted by SetFeedback.
func (state FeedbackState) Valid() bool {
	switch state {
	case FeedbackUseful, FeedbackDismissed, FeedbackLater, FeedbackClear:
		return true
	default:
		return false
	}
}

// StateStoreOption configures a recall StateStore.
type StateStoreOption func(*StateStore)

// WithClock injects the clock used for feedback, snooze, and open timestamps.
func WithClock(now func() time.Time) StateStoreOption {
	return func(store *StateStore) {
		store.now = now
	}
}

// WithNotePaths supplies the immutable note-path snapshot used by read-only
// recall queries. It is intended for callers that already loaded the vault and
// need reads to use that exact resilient snapshot without walking the filesystem
// again. Explicit writes still rescan the vault so pruning uses current on-disk
// note existence. Paths are copied and validated when the store is built.
func WithNotePaths(paths []string) StateStoreOption {
	paths = append([]string(nil), paths...)
	return func(store *StateStore) {
		store.notePathsFixed = true
		store.notePaths = make(map[string]struct{}, len(paths))
		for _, value := range paths {
			normalized, err := normalizeRecallPath(value)
			if err != nil {
				store.notePathsErr = fmt.Errorf("recall: note path snapshot: %w", err)
				store.notePaths = nil
				return
			}
			store.notePaths[normalized] = struct{}{}
		}
	}
}

// StateStore persists recall feedback for one vault. It owns recall.json write
// serialization: every mutation takes the vault path lock and holds it across
// the fresh read-modify-write, so stores in this process and other bn processes
// contend on the same flock. Reads stay lazy and cache one validated snapshot,
// which lets a ranker request many pair signals without rereading recall.json
// per candidate.
type StateStore struct {
	root string
	now  func() time.Time

	mu             sync.Mutex
	state          *recallStateFile
	stateExists    bool
	notePaths      map[string]struct{}
	notePathsFixed bool
	notePathsErr   error
}

type recallStateFile struct {
	Version int                          `json:"version"`
	Records map[string]recallStateRecord `json:"records"`
}

type recallStateRecord struct {
	SourceRelPath    string     `json:"source_rel_path"`
	CandidateRelPath string     `json:"candidate_rel_path"`
	Rating           string     `json:"rating"`
	SnoozedUntil     *time.Time `json:"snoozed_until,omitempty"`
	LastOpenedAt     *time.Time `json:"last_opened_at,omitempty"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// NewStateStore returns a recall state store rooted at root. The .bnotes
// directory and recall.json are created lazily on the first explicit write.
func NewStateStore(root string, options ...StateStoreOption) *StateStore {
	store := &StateStore{root: root, now: time.Now}
	for _, option := range options {
		if option != nil {
			option(store)
		}
	}
	return store
}

// Validate loads and validates the current state without creating or changing
// any file. Callers can use it to surface corruption even when a query has no
// candidate pairs and therefore never calls Signal.
func (store *StateStore) Validate() error {
	if store == nil {
		return errors.New("recall: nil state store")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.notePathsErr != nil {
		return store.notePathsErr
	}
	_, _, err := store.loadLocked()
	return err
}

// ActiveFeedbackForSource returns the active explicit feedback recorded for a
// source note, ordered by candidate path. Missing notes, expired snoozes, and
// open-history-only records are omitted without changing persisted state.
func (store *StateStore) ActiveFeedbackForSource(sourcePath string) ([]ActiveFeedback, error) {
	if store == nil {
		return nil, errors.New("recall: nil state store")
	}
	sourcePath, err := normalizeRecallPath(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("recall: source path: %w", err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	state, _, err := store.loadLocked()
	if err != nil {
		return nil, err
	}
	paths, err := store.notePathsLocked(false)
	if err != nil {
		return nil, err
	}
	if !containsRecallPath(paths, sourcePath) {
		return nil, nil
	}

	now := store.currentTime()
	feedback := make([]ActiveFeedback, 0)
	for _, record := range state.Records {
		if record.SourceRelPath != sourcePath || !containsRecallPath(paths, record.CandidateRelPath) {
			continue
		}
		entry := ActiveFeedback{CandidateRelPath: record.CandidateRelPath}
		switch record.Rating {
		case string(FeedbackUseful):
			entry.State = FeedbackUseful
		case string(FeedbackDismissed):
			entry.State = FeedbackDismissed
		default:
			if record.SnoozedUntil == nil || !record.SnoozedUntil.After(now) {
				continue
			}
			entry.State = FeedbackLater
			entry.Until = *record.SnoozedUntil
		}
		feedback = append(feedback, entry)
	}
	sort.Slice(feedback, func(i, j int) bool {
		return feedback[i].CandidateRelPath < feedback[j].CandidateRelPath
	})
	if len(feedback) == 0 {
		return nil, nil
	}
	return feedback, nil
}

// Signal implements RecallSignalSource. Missing notes and expired snoozes are
// ignored in the returned signal, but this read-only method never prunes or
// persists the loaded state.
func (store *StateStore) Signal(sourcePath, candidatePath string) (RecallSignal, error) {
	if store == nil {
		return RecallSignal{}, errors.New("recall: nil state store")
	}
	sourcePath, candidatePath, pairID, err := normalizeRecallPair(sourcePath, candidatePath)
	if err != nil {
		return RecallSignal{}, err
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	state, _, err := store.loadLocked()
	if err != nil {
		return RecallSignal{}, err
	}
	record, ok := state.Records[pairID]
	if !ok {
		return RecallSignal{}, nil
	}
	paths, err := store.notePathsLocked(false)
	if err != nil {
		return RecallSignal{}, err
	}
	if !containsRecallPath(paths, sourcePath) || !containsRecallPath(paths, candidatePath) {
		return RecallSignal{}, nil
	}

	signal := RecallSignal{
		Useful:    record.Rating == string(FeedbackUseful),
		Dismissed: record.Rating == string(FeedbackDismissed),
	}
	if record.SnoozedUntil != nil && record.SnoozedUntil.After(store.currentTime()) {
		signal.SnoozedUntil = *record.SnoozedUntil
	}
	if record.LastOpenedAt != nil {
		signal.LastOpenedAt = *record.LastOpenedAt
	}
	return signal, nil
}

// SetFeedback writes an explicit useful, dismissed, later, or clear state for
// a pair. Later requires a future deadline; every other state rejects one.
func (store *StateStore) SetFeedback(
	sourcePath string,
	candidatePath string,
	feedback FeedbackState,
	until time.Time,
) error {
	if store == nil {
		return errors.New("recall: nil state store")
	}
	if !feedback.Valid() {
		return fmt.Errorf("recall: unknown feedback state %q", feedback)
	}
	sourcePath, candidatePath, pairID, err := normalizeRecallPair(sourcePath, candidatePath)
	if err != nil {
		return err
	}
	now := store.currentTime()
	if feedback == FeedbackLater {
		if until.IsZero() {
			return errors.New("recall: later feedback requires a snooze deadline")
		}
		if !until.After(now) {
			return fmt.Errorf("recall: snooze deadline %s must be in the future", until.Format(time.RFC3339))
		}
	} else if !until.IsZero() {
		return fmt.Errorf("recall: feedback state %q does not accept a snooze deadline", feedback)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	return store.mutateLocked(now, func(state *recallStateFile, paths map[string]struct{}) error {
		if err := requireRecallPairExists(paths, sourcePath, candidatePath); err != nil {
			return err
		}

		record := state.Records[pairID]
		record.SourceRelPath = sourcePath
		record.CandidateRelPath = candidatePath
		switch feedback {
		case FeedbackUseful, FeedbackDismissed:
			record.Rating = string(feedback)
			record.SnoozedUntil = nil
		case FeedbackLater:
			record.Rating = ""
			deadline := until
			record.SnoozedUntil = &deadline
		case FeedbackClear:
			record.Rating = ""
			record.SnoozedUntil = nil
		}
		record.UpdatedAt = now
		if recallRecordEmpty(record) {
			delete(state.Records, pairID)
		} else {
			state.Records[pairID] = record
		}
		return nil
	})
}

// RecordOpened records that the candidate was selected interactively from the
// source note while preserving any explicit feedback for the same pair.
func (store *StateStore) RecordOpened(sourcePath, candidatePath string) error {
	if store == nil {
		return errors.New("recall: nil state store")
	}
	sourcePath, candidatePath, pairID, err := normalizeRecallPair(sourcePath, candidatePath)
	if err != nil {
		return err
	}
	now := store.currentTime()

	store.mu.Lock()
	defer store.mu.Unlock()

	return store.mutateLocked(now, func(state *recallStateFile, paths map[string]struct{}) error {
		if err := requireRecallPairExists(paths, sourcePath, candidatePath); err != nil {
			return err
		}
		record := state.Records[pairID]
		record.SourceRelPath = sourcePath
		record.CandidateRelPath = candidatePath
		record.LastOpenedAt = &now
		record.UpdatedAt = now
		state.Records[pairID] = record
		return nil
	})
}

// PairID returns the deterministic identifier for a normalized, directed
// source/candidate pair.
func PairID(sourcePath, candidatePath string) (string, error) {
	_, _, pairID, err := normalizeRecallPair(sourcePath, candidatePath)
	return pairID, err
}

func normalizeRecallPair(sourcePath, candidatePath string) (string, string, string, error) {
	sourcePath, err := normalizeRecallPath(sourcePath)
	if err != nil {
		return "", "", "", fmt.Errorf("recall: source path: %w", err)
	}
	candidatePath, err = normalizeRecallPath(candidatePath)
	if err != nil {
		return "", "", "", fmt.Errorf("recall: candidate path: %w", err)
	}
	if sourcePath == candidatePath {
		return "", "", "", errors.New("recall: source and candidate paths must differ")
	}
	sum := sha256.Sum256([]byte(sourcePath + "\x00" + candidatePath))
	return sourcePath, candidatePath, hex.EncodeToString(sum[:]), nil
}

func normalizeRecallPath(value string) (string, error) {
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
